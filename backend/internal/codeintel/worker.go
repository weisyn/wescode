package codeintel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/weisyn/wescode/internal/treesitter"
)

const workerMaxRetries = 2

// --- Wire protocol types ---

type WorkerRequest struct {
	Files []WorkerFile `json:"files"`
}

type WorkerFile struct {
	Path     string `json:"path"`
	Language string `json:"language"`
}

type WorkerOutput struct {
	Type  string      `json:"type"`
	Node  *WorkerNode `json:"node,omitempty"`
	Edge  *WorkerEdge `json:"edge,omitempty"`
	Error string      `json:"error,omitempty"`
}

type WorkerNode struct {
	QualifiedName string `json:"qualified_name"`
	FilePath      string `json:"file_path"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Qualified     string `json:"qualified,omitempty"`
	Signature     string `json:"signature,omitempty"`
	Parent        string `json:"parent,omitempty"`
	LineStart     int    `json:"line_start"`
	LineEnd       int    `json:"line_end"`
	ByteStart     int    `json:"byte_start"`
	ByteEnd       int    `json:"byte_end"`
	ContentHash   string `json:"content_hash"`
	Exported      bool   `json:"exported"`
	Visibility    string `json:"visibility,omitempty"`
	PackagePath   string `json:"package_path,omitempty"`
}

// WorkerEdge is the wire shape for one extracted edge. There is deliberately no
// resolution field: tree-sitter reports that a reference exists at a source
// location, never which symbol it lands on. Every worker edge is therefore
// unresolved by construction and target binding is Pass 4's job. The field it
// used to carry was a float that every one of the eight emit sites set to the
// same literal 1.0 — a constant crossing a process boundary to say nothing.
type WorkerEdge struct {
	SourceQName string `json:"source_qname"`
	TargetQName string `json:"target_qname,omitempty"`
	TargetName  string `json:"target_name"`
	Kind        string `json:"kind"`
	Source      string `json:"source,omitempty"`
}

// --- Subprocess entry point ---

// RunWorker is the entry point for the --index-worker subprocess.
// It reads a WorkerRequest from stdin, parses all listed files using tree-sitter,
// and emits results as line-delimited JSON to stdout.
func RunWorker() error {
	runtime.GOMAXPROCS(4)

	var req WorkerRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	pool := treesitter.NewParserPoolN(4)
	defer pool.Close()

	enc := json.NewEncoder(os.Stdout)

	for _, f := range req.Files {
		if err := workerParseFile(pool, enc, f.Path, f.Language); err != nil {
			_ = enc.Encode(WorkerOutput{Type: "error", Error: fmt.Sprintf("%s: %v", f.Path, err)})
		}
	}

	return enc.Encode(WorkerOutput{Type: "done"})
}

func workerParseFile(pool *treesitter.ParserPool, enc *json.Encoder, path, language string) error {
	lang, ok := treesitter.DetectLang(path)
	if !ok {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	tree, err := pool.Parse(lang, content, nil)
	if err != nil {
		return err
	}
	defer tree.Close()

	symbols := treesitter.ExtractSymbols(lang, tree, content)
	pkgPath := filepath.Dir(path)
	hash := contentHashSHA(content)

	for _, sym := range symbols {
		qname := pkgPath + ":" + sym.Name + ":" + fmt.Sprintf("%d", sym.StartLine)
		qualified := pkgPath + "." + sym.Name
		if sym.Parent != "" {
			qualified = pkgPath + "." + sym.Parent + "." + sym.Name
		}
		_ = enc.Encode(WorkerOutput{
			Type: "node",
			Node: &WorkerNode{
				QualifiedName: qname,
				FilePath:      path,
				Kind:          string(sym.Kind),
				Name:          sym.Name,
				Qualified:     qualified,
				Signature:     sym.Signature,
				Parent:        sym.Parent,
				LineStart:     sym.StartLine,
				LineEnd:       sym.EndLine,
				ByteStart:     int(sym.StartByte),
				ByteEnd:       int(sym.EndByte),
				ContentHash:   hash,
				Exported:      sym.Exported,
				Visibility:    sym.Visibility,
				PackagePath:   pkgPath,
			},
		})
	}

	calls := treesitter.ExtractAllCallNames(lang, tree, content, symbols, nil)
	for _, sym := range symbols {
		if sym.Kind != treesitter.KindFunction && sym.Kind != treesitter.KindMethod {
			continue
		}
		key := sym.Name + "\x00" + fmt.Sprintf("%d", sym.StartLine)
		sourceQName := pkgPath + ":" + sym.Name + ":" + fmt.Sprintf("%d", sym.StartLine)
		for _, callee := range calls[key] {
			_ = enc.Encode(WorkerOutput{
				Type: "edge",
				Edge: &WorkerEdge{
					SourceQName: sourceQName,
					TargetName:  callee,
					Kind:        string(EdgeCall),
					Source:      "tree-sitter",
				},
			})
		}
	}

	imports := treesitter.ExtractImports(lang, tree, content)
	for _, imp := range imports {
		impQName := pkgPath + ":import:" + imp.Path
		_ = enc.Encode(WorkerOutput{
			Type: "node",
			Node: &WorkerNode{
				QualifiedName: impQName,
				FilePath:      path,
				Kind:          string(NodeImport),
				Name:          imp.Path,
				LineStart:     imp.StartLine,
				LineEnd:       imp.EndLine,
				PackagePath:   pkgPath,
			},
		})
		_ = enc.Encode(WorkerOutput{
			Type: "edge",
			Edge: &WorkerEdge{
				SourceQName: impQName,
				TargetName:  imp.Path,
				Kind:        string(EdgeImport),
				Source:      "tree-sitter",
			},
		})
	}

	// Extract type hierarchy edges (extends/implements declarations).
	hierEdges := treesitter.ExtractTypeHierarchy(lang, tree, content)
	for _, he := range hierEdges {
		sourceQName := pkgPath + ":" + he.SourceName + ":" + fmt.Sprintf("%d", he.SourceLine)
		_ = enc.Encode(WorkerOutput{
			Type: "edge",
			Edge: &WorkerEdge{
				SourceQName: sourceQName,
				TargetName:  he.TargetName,
				Kind:        string(EdgeExtends),
				Source:      "tree-sitter",
			},
		})
	}

	return nil
}

// --- Main process dispatcher ---

// isTestBinary reports whether the current process is a `go test` binary.
// go test names the executable <pkg>.test; spawning it with --index-worker
// would fail with "flag provided but not defined" because test binaries only
// register -test.* flags. Centralizing the detection lets every test exercise
// in-process parsing instead of requiring each caller to remember
// WorkerCount = -1 (only pass8_pass9_test.go did).
func isTestBinary() bool {
	// Windows names it <pkg>.test.exe, so the suffix check must run after the
	// platform extension is trimmed. Without this the predicate is always false
	// on Windows and every test reaching this path spawns the test binary as
	// --index-worker, which exits 2 on the unregistered flag — the package has
	// never been green there, on the platform whose spawn code differs most.
	base := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	return strings.HasSuffix(base, ".test")
}

// dispatchToWorkers splits files into batches and runs them in parallel subprocesses.
// Special case: workerCount == -1 triggers in-process parsing (for test environments
// where the test binary doesn't register --index-worker).
func dispatchToWorkers(ctx context.Context, files []FileToParse, buf *GraphBuffer, workerCount int) error {
	if workerCount == -1 || isTestBinary() {
		return parseInProcess(ctx, files, buf)
	}
	if workerCount <= 0 {
		workerCount = runtime.NumCPU() / 2
		if workerCount < 1 {
			workerCount = 1
		}
	}

	batches := splitBatches(files, workerCount)
	if len(batches) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(batches))

	for _, batch := range batches {
		wg.Add(1)
		go func(batch []FileToParse) {
			defer wg.Done()
			if err := runWorkerWithRetry(ctx, batch, buf); err != nil {
				errCh <- err
			}
		}(batch)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func runWorkerWithRetry(ctx context.Context, files []FileToParse, buf *GraphBuffer) error {
	for attempt := 0; attempt <= workerMaxRetries; attempt++ {
		err := runSingleWorker(ctx, files, buf)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("index worker crashed, retrying",
			"attempt", attempt+1,
			"max", workerMaxRetries+1,
			"files", len(files),
			"err", err,
		)
	}
	return fmt.Errorf("worker failed after %d attempts for %d files", workerMaxRetries+1, len(files))
}

func runSingleWorker(ctx context.Context, files []FileToParse, buf *GraphBuffer) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.CommandContext(ctx, executable, "--index-worker")
	setProcGroup(cmd)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=4")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}

	req := WorkerRequest{Files: toWorkerFiles(files)}
	if err := json.NewEncoder(stdin).Encode(&req); err != nil {
		_ = killGroup(cmd.Process.Pid)
		_ = cmd.Wait()
		return fmt.Errorf("send request: %w", err)
	}
	stdin.Close()

	if err := consumeWorkerOutput(stdout, buf); err != nil {
		_ = cmd.Wait()
		return err
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("worker exited: %w", err)
	}
	return nil
}

func consumeWorkerOutput(r io.Reader, buf *GraphBuffer) error {
	scanner := bufio.NewScanner(r)
	// tree-sitter extraction can produce long signature lines
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		var out WorkerOutput
		if err := json.Unmarshal(scanner.Bytes(), &out); err != nil {
			continue
		}
		switch out.Type {
		case "node":
			if out.Node != nil {
				n := workerNodeToNode(out.Node)
				buf.AddNode(&n)
			}
		case "edge":
			if out.Edge != nil {
				buf.AddEdge(workerEdgeToEdge(out.Edge))
			}
		case "done":
			return nil
		case "error":
			slog.Debug("worker file error", "err", out.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read worker output: %w", err)
	}
	return fmt.Errorf("worker output ended without done marker")
}

// --- Conversion helpers ---

func workerNodeToNode(wn *WorkerNode) Node {
	return Node{
		QualifiedName: wn.QualifiedName,
		FilePath:      wn.FilePath,
		Kind:          NodeKind(wn.Kind),
		Name:          wn.Name,
		Qualified:     wn.Qualified,
		Signature:     wn.Signature,
		Parent:        wn.Parent,
		LineStart:     wn.LineStart,
		LineEnd:       wn.LineEnd,
		ByteStart:     wn.ByteStart,
		ByteEnd:       wn.ByteEnd,
		ContentHash:   wn.ContentHash,
		Exported:      wn.Exported,
		Visibility:    wn.Visibility,
		PackagePath:   wn.PackagePath,
	}
}

func workerEdgeToEdge(we *WorkerEdge) Edge {
	return Edge{
		SourceQName: we.SourceQName,
		TargetQName: we.TargetQName,
		TargetName:  we.TargetName,
		Kind:        EdgeKind(we.Kind),
		Resolution:  ResolutionUnresolved,
		Source:      we.Source,
	}
}

func toWorkerFiles(files []FileToParse) []WorkerFile {
	wf := make([]WorkerFile, len(files))
	for i, f := range files {
		wf[i] = WorkerFile{Path: f.Path, Language: f.Language}
	}
	return wf
}

func splitBatches(files []FileToParse, n int) [][]FileToParse {
	if len(files) == 0 {
		return nil
	}
	if n > len(files) {
		n = len(files)
	}
	batchSize := (len(files) + n - 1) / n
	batches := make([][]FileToParse, 0, n)
	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		batches = append(batches, files[i:end])
	}
	return batches
}

// parseInProcess does in-process tree-sitter parsing without spawning subprocess.
// Used in test environments where the test binary doesn't handle --index-worker.
func parseInProcess(ctx context.Context, files []FileToParse, buf *GraphBuffer) error {
	pool := treesitter.NewParserPoolN(2)
	defer pool.Close()

	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		lang, ok := treesitter.DetectLang(f.Path)
		if !ok {
			continue
		}

		content, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}

		tree, err := pool.Parse(lang, content, nil)
		if err != nil {
			continue
		}

		symbols := treesitter.ExtractSymbols(lang, tree, content)
		pkgPath := filepath.Dir(f.Path)
		hash := contentHashSHA(content)

		for _, sym := range symbols {
			qname := pkgPath + ":" + sym.Name + ":" + fmt.Sprintf("%d", sym.StartLine)
			qualified := pkgPath + "." + sym.Name
			if sym.Parent != "" {
				qualified = pkgPath + "." + sym.Parent + "." + sym.Name
			}
			buf.AddNode(&Node{
				QualifiedName: qname,
				FilePath:      f.Path,
				Kind:          NodeKind(sym.Kind),
				Name:          sym.Name,
				Qualified:     qualified,
				Signature:     sym.Signature,
				Parent:        sym.Parent,
				LineStart:     sym.StartLine,
				LineEnd:       sym.EndLine,
				ByteStart:     int(sym.StartByte),
				ByteEnd:       int(sym.EndByte),
				ContentHash:   hash,
				Exported:      sym.Exported,
				Visibility:    sym.Visibility,
				PackagePath:   pkgPath,
			})
		}

		calls := treesitter.ExtractAllCallNames(lang, tree, content, symbols, nil)
		for _, sym := range symbols {
			if sym.Kind != treesitter.KindFunction && sym.Kind != treesitter.KindMethod {
				continue
			}
			key := sym.Name + "\x00" + fmt.Sprintf("%d", sym.StartLine)
			sourceQName := pkgPath + ":" + sym.Name + ":" + fmt.Sprintf("%d", sym.StartLine)
			for _, callee := range calls[key] {
				buf.AddEdge(Edge{
					SourceQName: sourceQName,
					TargetName:  callee,
					Kind:        EdgeCall,
					Resolution:  ResolutionUnresolved,
					Source:      "tree-sitter",
				})
			}
		}

		imports := treesitter.ExtractImports(lang, tree, content)
		for _, imp := range imports {
			impQName := pkgPath + ":import:" + imp.Path
			buf.AddNode(&Node{
				QualifiedName: impQName,
				FilePath:      f.Path,
				Kind:          NodeImport,
				Name:          imp.Path,
				LineStart:     imp.StartLine,
				LineEnd:       imp.EndLine,
				PackagePath:   pkgPath,
			})
			buf.AddEdge(Edge{
				SourceQName: impQName,
				TargetName:  imp.Path,
				Kind:        EdgeImport,
				Resolution:  ResolutionUnresolved,
				Source:      "tree-sitter",
			})
		}

		hierEdges := treesitter.ExtractTypeHierarchy(lang, tree, content)
		for _, he := range hierEdges {
			sourceQName := pkgPath + ":" + he.SourceName + ":" + fmt.Sprintf("%d", he.SourceLine)
			buf.AddEdge(Edge{
				SourceQName: sourceQName,
				TargetName:  he.TargetName,
				Kind:        EdgeExtends,
				Resolution:  ResolutionUnresolved,
				Source:      "tree-sitter",
			})
		}

		tree.Close()
	}
	return nil
}
