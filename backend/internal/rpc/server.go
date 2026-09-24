package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/notify"
)

type MethodHandler func(ctx context.Context, req Request) (any, *RPCError)

type Server struct {
	reader     *bufio.Reader
	writer     io.Writer
	writeMu    sync.Mutex
	handlers   map[string]MethodHandler
	middleware func(ctx context.Context, method string, next MethodHandler, req Request) (any, *RPCError)

	// writerDead is set (under writeMu) after the first write failure or
	// timeout. Once dead the stdio channel is unusable: every subsequent
	// writeMessage returns ErrStdioDead without blocking on writeMu, and
	// Run() terminates so the process can exit (main calls log.Fatalf).
	writerDead bool
	deadOnce   sync.Once
	dead       chan struct{}

	startedAt    time.Time
	framesRead   int64
	initReceived bool
}

// writeTimeout bounds a single write when the underlying writer supports
// deadlines (*os.File, net.Conn). A wedged pipe must fail fast instead of
// blocking writeMu forever — the old goroutine+select design leaked a
// goroutine holding the lock, degrading the whole process.
const writeTimeout = 5 * time.Second

// maxFrameSize caps a single RPC frame body. Without an upper bound a
// malformed Content-Length header triggers make([]byte, n) with an
// arbitrarily large n, OOMing the process.
const maxFrameSize = 64 << 20 // 64 MiB

// ErrStdioDead is returned by writeMessage once the stdio writer has failed
// or timed out. The caller must treat the connection as permanently lost.
var ErrStdioDead = errors.New("rpc: stdio writer dead")

// deadlineWriter is implemented by *os.File (stdout in production) and
// net.Conn, both of which support per-write deadlines.
type deadlineWriter interface {
	SetWriteDeadline(time.Time) error
}

func NewServer(in io.Reader, out io.Writer) *Server {
	return &Server{
		reader:    bufio.NewReader(in),
		writer:    out,
		handlers:  make(map[string]MethodHandler),
		dead:      make(chan struct{}),
		startedAt: time.Now(),
	}
}

// markDead permanently fails the stdio writer. Called from writeMessage on
// write failure/timeout; idempotent via sync.Once.
func (s *Server) markDead() {
	s.deadOnce.Do(func() { close(s.dead) })
}

func (s *Server) Register(method string, handler MethodHandler) {
	s.handlers[method] = handler
}

// SetMiddleware installs a global middleware that wraps every handler
// dispatch. The middleware receives the method name so it can whitelist
// methods that don't require Cell Active (auth/*, initialize, etc.).
func (s *Server) SetMiddleware(mw func(ctx context.Context, method string, next MethodHandler, req Request) (any, *RPCError)) {
	s.middleware = mw
}

// longRunningMethods lists methods that may block for an extended period
// (e.g. streaming LLM runs). These are dispatched in a goroutine so that
// short control-plane requests (chat/cancel, editor/*) can be processed
// concurrently on the main read loop.
var longRunningMethods = map[string]bool{
	"chat/send":  true,
	"initialize": true,
}

// dispatchAsync returns true when the handler should run off the read loop.
// sidebar/* must not block frame processing — a slow sidebar call (e.g.
// listConversations waiting on engine lock during chat/send) would otherwise
// stall every other RPC including unrelated sidebar pages in the packaged app.
func dispatchAsync(method string) bool {
	if longRunningMethods[method] {
		return true
	}
	return strings.HasPrefix(method, "sidebar/") || strings.HasPrefix(method, "cell/")
}

func (s *Server) Run(ctx context.Context) error {
	// pendingLong tracks at most one outstanding long-running handler so
	// we can wait for it on shutdown without a full WaitGroup.
	var pendingLong sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			pendingLong.Wait()
			return nil
		case <-s.dead:
			// The stdio writer failed or timed out - the peer is gone.
			// Fail fast so the process can exit (main calls log.Fatalf).
			slog.Error("[rpc] stdio writer dead, terminating")
			pendingLong.Wait()
			return fmt.Errorf("rpc: stdio writer dead: %w", ErrStdioDead)
		default:
		}

		body, err := s.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				uptime := time.Since(s.startedAt)
				slog.Info("[rpc] readLoop.EOF",
					"uptime_ms", uptime.Milliseconds(),
					"frames_read", s.framesRead,
					"init_received", s.initReceived,
				)
				if !s.initReceived {
					slog.Warn("[rpc] stdin closed before initialize RPC — Electron likely killed/closed pipe prematurely",
						"uptime_ms", uptime.Milliseconds(),
					)
				}
				pendingLong.Wait()
				return nil
			}
			slog.Warn("[rpc] readLoop.readFrame.error", "err", err)
			return fmt.Errorf("rpc: read frame: %w", err)
		}
		s.framesRead++

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			preview := string(body)
			if len(preview) > 200 {
				runes := []rune(preview)
				if len(runes) > 200 {
					preview = string(runes[:200]) + "..."
				}
			}
			slog.Warn("[rpc] readLoop.parse.error", "err", err, "body_preview", preview)
			_ = s.writeResponse(Response{
				JSONRPC: jsonRPCVersion,
				Error: &RPCError{
					Code:    -32700,
					Message: "parse error: " + err.Error(),
				},
			})
			continue
		}
		// Log every chat/send frame as INFO so we can prove it arrived and
		// was parsed successfully; other methods stay at Debug to avoid
		// noise.
		if req.Method == "chat/send" {
			slog.Info("[rpc] readLoop.frame.chat_send",
				"trace_id", extractTraceID(req.Params),
				"params_bytes", len(req.Params))
		}
		if req.JSONRPC != jsonRPCVersion {
			_ = s.writeResponse(Response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Error: &RPCError{
					Code:    -32600,
					Message: "invalid request: jsonrpc must be 2.0, got " + req.JSONRPC,
				},
			})
			continue
		}

		if req.Method == "initialize" {
			s.initReceived = true
		}

		handler, ok := s.handlers[req.Method]
		if !ok {
			_ = s.writeResponse(Response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Error: &RPCError{
					Code:    -32601,
					Message: "Method not found",
				},
			})
			continue
		}

		if dispatchAsync(req.Method) {
			reqCtx, reqCancel := context.WithCancel(ctx)
			pendingLong.Add(1)
			go func(h MethodHandler, r Request, cancel context.CancelFunc) {
				defer pendingLong.Done()
				defer cancel()
				s.dispatchHandler(reqCtx, h, r)
			}(handler, req, reqCancel)
		} else {
			s.dispatchHandler(ctx, handler, req)
		}
	}
}

func genRequestID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

const slowRPCThreshold = 100 * time.Millisecond

// extractTraceID pulls the `_traceId` field from JSON-encoded params for
// diagnostic log correlation. Returns empty string if absent.
func extractTraceID(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var probe struct {
		TraceID string `json:"_traceId"`
	}
	if err := json.Unmarshal(params, &probe); err != nil {
		return ""
	}
	return probe.TraceID
}

func (s *Server) dispatchHandler(ctx context.Context, handler MethodHandler, req Request) {
	reqID := genRequestID()
	traceID := extractTraceID(req.Params)
	start := time.Now()

	// 构建带请求身份字段的 logger，存入 ctx 供下游 handler 取用（L(ctx)）。
	reqLogger := slog.Default().With(
		"method", req.Method,
		"request_id", reqID,
		"trace_id", traceID,
	)
	ctx = WithLogger(ctx, reqLogger)

	// Log dispatch entry at INFO for chat/send so it's always visible in
	// diagnostic terminals; other methods stay at DEBUG.
	if req.Method == "chat/send" {
		reqLogger.Info("[rpc] dispatch.enter", "params_bytes", len(req.Params))
	} else {
		reqLogger.Debug("[rpc] →")
	}

	var result any
	var rpcErr *RPCError

	func() {
		defer func() {
			if r := recover(); r != nil {
				reqLogger.Error("[rpc] panic", "panic", r)
				// reqID is in the message, not a side field: it is the only
				// thing that ties this response to the slog line above, and a
				// field the bridge does not forward cannot tie anything.
				rpcErr = &RPCError{
					Code:    -32603,
					Message: fmt.Sprintf("internal panic (request %s): %v", reqID, r),
				}
			}
		}()
		if s.middleware != nil {
			result, rpcErr = s.middleware(ctx, req.Method, handler, req)
		} else {
			result, rpcErr = handler(ctx, req)
		}
	}()

	elapsed := time.Since(start)
	if rpcErr != nil {
		reqLogger.Warn("[rpc] ←", "elapsed", elapsed, "error_code", rpcErr.Code, "reason", rpcErr.Reason, "error", rpcErr.Message)
	} else if req.Method == "chat/send" {
		reqLogger.Info("[rpc] dispatch.exit", "elapsed", elapsed)
	} else if elapsed >= slowRPCThreshold {
		reqLogger.Info("[rpc] ← slow", "elapsed", elapsed)
	} else {
		reqLogger.Debug("[rpc] ←", "elapsed", elapsed)
	}

	if len(req.ID) == 0 {
		return
	}

	resp := Response{
		JSONRPC: jsonRPCVersion,
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	}
	if err := s.writeResponse(resp); err != nil {
		slog.Error("[rpc] write response failed", "method", req.Method, "request_id", reqID, "error", err)
	}
}

// Notify pushes a JSON-RPC notification to the IDE extension.
//
// This is the only funnel, so it is where the zero Method is caught. A Method
// can only be constructed inside the notify package, so the sole way to reach
// here with an empty name is an uninitialized variable — which would otherwise
// put `"method": ""` on the wire, land in the extension's default branch, and
// read as an unhandled notification rather than as the assignment someone
// forgot.
func (s *Server) Notify(method notify.Method, params any) error {
	if method.IsZero() {
		return fmt.Errorf("rpc: notify with zero notify.Method (a var was declared but never assigned)")
	}
	msg := Notification{
		JSONRPC: jsonRPCVersion,
		Method:  method.Wire(),
		Params:  params,
	}
	err := s.writeMessage(msg)
	if method == notify.ChatStream && err != nil {
		slog.Warn("[rpc] notify.chat_stream.ERROR", "err", err)
	}
	return err
}

func (s *Server) writeResponse(resp Response) error {
	return s.writeMessage(resp)
}

func (s *Server) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("rpc: marshal message: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writerDead {
		return ErrStdioDead
	}

	// INV-RPC-DEADLINE-01 (PLATFORM): Best-effort write deadline.
	// @inv-require: Best-effort write deadline
	// @inv-severity: critical
	// *os.File.SetWriteDeadline works on pollable descriptors (network
	// sockets, some pipes on Linux). On macOS, stdio pipes spawned by
	// Electron return "file type does not support deadline" — the Go
	// interface exists but the OS capability does not. That is NOT a write
	// failure, just lack of timeout protection; we proceed without a
	// deadline. Only real Write errors (frame/body) mark the writer dead.
	// Verified: macOS 15 (Electron stdio pipe, runtime-confirmed).
	if w, ok := s.writer.(deadlineWriter); ok {
		if derr := w.SetWriteDeadline(time.Now().Add(writeTimeout)); derr == nil {
			defer w.SetWriteDeadline(time.Time{}) //nolint:errcheck
		}
	}

	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, werr := io.WriteString(s.writer, frame); werr != nil {
		s.writerDead = true
		s.markDead()
		return fmt.Errorf("rpc: write frame: %w", werr)
	}
	if _, werr := s.writer.Write(data); werr != nil {
		s.writerDead = true
		s.markDead()
		return fmt.Errorf("rpc: write body: %w", werr)
	}
	return nil
}

func (s *Server) readFrame() ([]byte, error) {
	headers := make(map[string]string)
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		headers[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
	}

	val, ok := headers["content-length"]
	if !ok {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 || n > maxFrameSize {
		return nil, fmt.Errorf("invalid Content-Length %q (max %d)", val, maxFrameSize)
	}

	body := make([]byte, n)
	if _, err := io.ReadFull(s.reader, body); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(body), nil
}
