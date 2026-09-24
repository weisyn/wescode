package deviceagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weisyn/wescode/internal/notify"
	"github.com/weisyn/wesgine/tool"
)

// Exec routing timeouts.
const (
	TermCreationTimeout   = 30 * time.Second
	TermInactivityTimeout = 120 * time.Second
	TermAbsoluteTimeout   = 180 * time.Second
)

// VSCodeShellProvider routes exec commands to local process or VSCode terminal.
type VSCodeShellProvider struct {
	Notifier NotifyFn
	Local    tool.ShellProvider
	Mgr      *TerminalManager
}

func (p *VSCodeShellProvider) Start(ctx context.Context, req tool.ShellRequest) (tool.ShellSession, error) {
	userFacing := req.UserFacing || IsUserFacingCommand(req.Command)
	if !userFacing {
		if IsVerificationCommand(req.Command) {
			session, err := p.Local.Start(ctx, req)
			if err != nil {
				return nil, err
			}
			return p.WrapWithMirror(req, session), nil
		}
		return p.Local.Start(ctx, req)
	}
	if req.Background {
		session, err := p.Local.Start(ctx, req)
		if err != nil {
			return nil, err
		}
		return p.WrapWithMirror(req, session), nil
	}

	session := p.Mgr.Create(req, p.Notifier)
	if err := p.Notifier(notify.TerminalCreate, map[string]any{
		"sessionId": session.ID,
		"command":   req.Command,
		"workDir":   req.WorkDir,
		"env":       req.Env,
		"label":     req.Label,
	}); err != nil {
		session.Fail(err)
		session.Cleanup()
		return p.Local.Start(ctx, req)
	}
	return session, nil
}

// WrapWithMirror creates a mirror session that forwards output to a VSCode terminal tab.
func (p *VSCodeShellProvider) WrapWithMirror(req tool.ShellRequest, inner tool.ShellSession) tool.ShellSession {
	mirrorID := fmt.Sprintf("mirror-%d", p.Mgr.Counter.Add(1))
	label := req.Label
	if label == "" {
		cmd := req.Command
		if len(cmd) > 60 {
			cmd = cmd[:60]
		}
		label = cmd
	}
	if !req.Background && IsVerificationCommand(req.Command) {
		label = "[AI] " + label
	}
	if err := p.Notifier(notify.TerminalCreate, map[string]any{
		"sessionId": mirrorID,
		"command":   req.Command,
		"workDir":   req.WorkDir,
		"label":     label,
		"readOnly":  true,
	}); err != nil {
		return inner
	}

	pipeR, pipeW := io.Pipe()
	teeOut := io.TeeReader(inner.Output(), pipeW)

	notifier := p.Notifier
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pipeR.Read(buf)
			if n > 0 {
				if err := notifier(notify.TerminalOutput, map[string]any{
					"sessionId": mirrorID,
					"data":      string(buf[:n]),
				}); err != nil {
					slog.Warn("[terminal] notify mirror output failed", "mirror_id", mirrorID, "error", err)
				}
			}
			if err != nil {
				break
			}
		}
		if err := notifier(notify.TerminalExited, map[string]any{
			"sessionId": mirrorID,
			"exitCode":  0,
		}); err != nil {
			slog.Warn("[terminal] notify mirror exited failed", "mirror_id", mirrorID, "error", err)
		}
	}()

	return &MirrorSession{Inner: inner, TeeOut: teeOut, PipeW: pipeW}
}

// MirrorSession wraps a local ShellSession, teeing output to a VSCode terminal.
type MirrorSession struct {
	Inner  tool.ShellSession
	TeeOut io.Reader
	PipeW  *io.PipeWriter
}

func (m *MirrorSession) Write(p []byte) (int, error) { return m.Inner.Write(p) }
func (m *MirrorSession) Output() io.Reader           { return m.TeeOut }
func (m *MirrorSession) Wait() (int, error) {
	code, err := m.Inner.Wait()
	m.PipeW.Close()
	return code, err
}
func (m *MirrorSession) Signal(sig os.Signal) error { return m.Inner.Signal(sig) }
func (m *MirrorSession) Pid() int                   { return m.Inner.Pid() }

// TerminalManager manages active VSCode terminal sessions.
type TerminalManager struct {
	mu       sync.Mutex
	sessions map[string]*ShellSession
	Counter  atomic.Int64
}

func NewTerminalManager() *TerminalManager {
	return &TerminalManager{sessions: make(map[string]*ShellSession)}
}

func (m *TerminalManager) Create(_ tool.ShellRequest, notifyFn NotifyFn) *ShellSession {
	id := fmt.Sprintf("term-%d", m.Counter.Add(1))
	s := &ShellSession{
		ID:        id,
		OutputCh:  make(chan struct{}, 1),
		DoneCh:    make(chan struct{}),
		mgr:       m,
		notify:    notifyFn,
		createdAt: time.Now(),
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	s.startDeadlines()
	return s
}

func (m *TerminalManager) OnOutput(sessionID string, data []byte) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	m.mu.Unlock()
	if s != nil {
		s.AppendOutput(data)
	}
}

func (m *TerminalManager) OnExited(sessionID string, exitCode int) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if s != nil {
		s.Finish(exitCode, nil)
	}
}

// ShellSession represents a VSCode integrated terminal session.
type ShellSession struct {
	ID        string
	outputBuf bytes.Buffer
	outputMu  sync.Mutex
	OutputCh  chan struct{}
	exitCode  int
	exitErr   error
	DoneCh    chan struct{}
	done      atomic.Bool

	lastActivity atomic.Int64
	mgr          *TerminalManager
	notify       NotifyFn
	createdAt    time.Time
}

func (s *ShellSession) touchActivity() { s.lastActivity.Store(time.Now().UnixMilli()) }
func (s *ShellSession) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("stdin write to VSCode terminal not supported")
}
func (s *ShellSession) Pid() int { return 0 }

func (s *ShellSession) Output() io.Reader  { return &sessionReader{session: s} }
func (s *ShellSession) Wait() (int, error) { <-s.DoneCh; return s.exitCode, s.exitErr }

func (s *ShellSession) Signal(sig os.Signal) error {
	if sig == os.Kill || sig == os.Interrupt {
		s.Fail(fmt.Errorf("terminal session %s: killed by signal %v", s.ID, sig))
		s.Cleanup()
	}
	return nil
}

func (s *ShellSession) AppendOutput(data []byte) {
	s.touchActivity()
	s.outputMu.Lock()
	s.outputBuf.Write(data)
	s.outputMu.Unlock()
	select {
	case s.OutputCh <- struct{}{}:
	default:
	}
}

func (s *ShellSession) Finish(exitCode int, err error) {
	if s.done.CompareAndSwap(false, true) {
		s.touchActivity()
		s.exitCode = exitCode
		s.exitErr = err
		close(s.DoneCh)
		select {
		case s.OutputCh <- struct{}{}:
		default:
		}
	}
}

func (s *ShellSession) Fail(err error) { s.Finish(-1, err) }

func (s *ShellSession) Cleanup() {
	if s.mgr != nil {
		s.mgr.mu.Lock()
		delete(s.mgr.sessions, s.ID)
		s.mgr.mu.Unlock()
	}
}

// notifyExited informs the frontend that this terminal session timed out and
// the real terminal should be disposed. Without this, a hung command leaves
// its zsh process and terminal tab alive forever, exhausting kernel pty
// capacity (ptmx_max) and causing "terminal process failed to launch".
func (s *ShellSession) notifyExited() {
	if s.notify != nil {
		if err := s.notify(notify.TerminalExited, map[string]any{"sessionId": s.ID, "exitCode": -1}); err != nil {
			slog.Warn("[terminal] notify shell exited failed", "session_id", s.ID, "error", err)
		}
	}
}

func (s *ShellSession) startDeadlines() {
	s.touchActivity()
	go func() {
		timer := time.NewTimer(TermCreationTimeout)
		defer timer.Stop()
		select {
		case <-s.DoneCh:
			return
		case <-timer.C:
			elapsed := time.Since(time.UnixMilli(s.lastActivity.Load()))
			if elapsed > TermCreationTimeout-time.Second {
				s.Fail(fmt.Errorf("terminal session %s: pty host did not respond within %s", s.ID, TermCreationTimeout))
				s.Cleanup()
				s.notifyExited()
				return
			}
		}
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.DoneCh:
				return
			case <-ticker.C:
				sinceActivity := time.Since(time.UnixMilli(s.lastActivity.Load()))
				sinceCreation := time.Since(s.createdAt)
				if sinceActivity > TermInactivityTimeout {
					s.Fail(fmt.Errorf("terminal session %s: inactivity timeout %s", s.ID, sinceActivity.Truncate(time.Second)))
					s.Cleanup()
					s.notifyExited()
					return
				}
				if sinceCreation > TermAbsoluteTimeout {
					s.Fail(fmt.Errorf("terminal session %s: absolute timeout %s", s.ID, TermAbsoluteTimeout))
					s.Cleanup()
					s.notifyExited()
					return
				}
			}
		}
	}()
}

type sessionReader struct {
	session *ShellSession
	offset  int
}

func (r *sessionReader) Read(p []byte) (int, error) {
	for {
		r.session.outputMu.Lock()
		available := r.session.outputBuf.Len() - r.offset
		if available > 0 {
			data := r.session.outputBuf.Bytes()[r.offset:]
			n := copy(p, data)
			r.offset += n
			r.session.outputMu.Unlock()
			return n, nil
		}
		r.session.outputMu.Unlock()
		if r.session.done.Load() {
			return 0, io.EOF
		}
		<-r.session.OutputCh
	}
}

// Command classification patterns.
var UserFacingPatterns = []string{
	"npm install", "npm ci", "yarn install", "yarn add", "pnpm install", "pnpm add",
	"pip install", "pip3 install", "poetry install", "uv pip install",
	"npm start", "npm run dev", "npm run serve", "yarn start", "yarn dev",
	"pnpm start", "pnpm dev", "go run ",
	"uvicorn ", "gunicorn ", "flask run", "python manage.py runserver",
	"ng serve", "vite", "node server", "node app",
	"docker compose up", "docker-compose up",
	"make run", "make serve", "make dev",
}

var VerificationPatterns = []string{
	"go build", "go vet", "go test", "go generate", "golangci-lint ",
	"npm test", "npm run build", "npm run lint", "npm run check", "npm run test",
	"npm run typecheck", "npm run type-check", "npm run tsc",
	"npx tsc", "npx eslint", "npx jest", "npx vitest", "npx prettier --check",
	"npx mocha", "npx ava",
	"yarn test", "yarn build", "yarn lint", "yarn typecheck",
	"pnpm test", "pnpm build", "pnpm lint", "pnpm typecheck",
	"bun test", "bun build", "deno test", "deno check", "deno lint",
	"cargo build", "cargo test", "cargo clippy", "cargo check", "cargo fmt --check",
	"pytest", "python -m pytest", "python3 -m pytest",
	"python -m unittest", "python3 -m unittest",
	"mypy ", "pyright ", "ruff check", "ruff format --check",
	"flake8 ", "black --check", "isort --check",
	"poetry run pytest", "poetry run mypy",
	"mvn compile", "mvn test", "mvn verify", "mvn package", "mvn clean install",
	"mvnw ", "./mvnw ", "gradle build", "gradle test", "gradle check", "gradle assemble",
	"gradlew ", "./gradlew ",
	"cmake --build", "gcc ", "g++ ", "clang ", "clang++ ", "ctest ",
	"dotnet build", "dotnet test", "dotnet publish",
	"bundle exec rspec", "bundle exec rake test", "bundle exec rubocop",
	"rspec ", "rake test", "rubocop ",
	"composer test", "phpunit", "php artisan test", "phpstan ", "psalm ",
	"swift build", "swift test", "swiftc ", "xcodebuild ",
	"dart analyze", "dart test", "dart compile",
	"flutter test", "flutter build", "flutter analyze",
	"mix compile", "mix test", "mix credo",
	"sbt compile", "sbt test", "sbt assembly",
	"stack build", "stack test", "cabal build", "cabal test",
	"zig build", "zig test",
	"flyway validate", "flyway migrate", "liquibase validate",
	"prisma migrate", "prisma db push", "prisma generate", "knex migrate",
	"alembic ", "django-admin migrate", "python manage.py migrate", "sqlx migrate",
	"terraform plan", "terraform validate", "terraform apply",
	"pulumi preview", "ansible-lint", "ansible-playbook --check",
	"make test", "make build", "make check", "make lint", "make compile",
	"make verify", "make all", "bazel build", "bazel test", "ninja ", "cmake ",
	"eslint ", "tsc --noemit", "tsc -noemit", "biome check", "biome lint",
	"shellcheck ", "hadolint ", "yamllint ", "jsonlint ",
	"markdownlint ", "stylelint ", "swiftlint ", "ktlint ", "checkstyle ", "spotbugs ",
}

func IsUserFacingCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, pat := range UserFacingPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func IsVerificationCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, pat := range VerificationPatterns {
		if strings.HasPrefix(lower, pat) || strings.Contains(lower, " && "+pat) {
			return true
		}
	}
	return false
}
