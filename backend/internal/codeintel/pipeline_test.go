package codeintel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/weisyn/wescode/internal/treesitter"
	_ "modernc.org/sqlite"
)

func TestPipelineEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()

	mainGo := `package main

func main() { helper() }

func helper() { util() }
`
	utilGo := `package main

func util() {}
`
	typesGo := `package main

type Config struct {
	Name string
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "util.go"), []byte(utilGo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(typesGo), 0644); err != nil {
		t.Fatal(err)
	}

	pool := treesitter.NewParserPoolN(2)
	defer pool.Close()

	dbPath := filepath.Join(t.TempDir(), "code.db")
	pipe := NewPipeline(tmpDir, dbPath, pool)

	result, err := pipe.Run(context.Background())
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if result.TotalFiles < 3 {
		t.Errorf("TotalFiles = %d, want >= 3", result.TotalFiles)
	}
	if result.NodesCreated <= 0 {
		t.Errorf("NodesCreated = %d, want > 0", result.NodesCreated)
	}
	if result.EdgesCreated <= 0 {
		t.Errorf("EdgesCreated = %d, want > 0", result.EdgesCreated)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var funcCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE kind='function'").Scan(&funcCount); err != nil {
		t.Fatalf("query functions: %v", err)
	}
	if funcCount < 3 {
		t.Errorf("function count = %d, want >= 3 (main, helper, util)", funcCount)
	}

	var callCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM edges WHERE kind='call'").Scan(&callCount); err != nil {
		t.Fatalf("query call edges: %v", err)
	}
	if callCount < 2 {
		t.Errorf("call edge count = %d, want >= 2 (main→helper, helper→util)", callCount)
	}

	var ftsCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbol_fts WHERE symbol_fts MATCH 'helper'").Scan(&ftsCount); err != nil {
		t.Fatalf("query FTS5: %v", err)
	}
	if ftsCount < 1 {
		t.Errorf("FTS5 match 'helper' = %d, want >= 1", ftsCount)
	}
}

func TestPipelineEmptyWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	pool := treesitter.NewParserPoolN(1)
	defer pool.Close()

	dbPath := filepath.Join(t.TempDir(), "empty.db")
	pipe := NewPipeline(tmpDir, dbPath, pool)

	result, err := pipe.Run(context.Background())
	if err != nil {
		t.Fatalf("Pipeline.Run on empty dir: %v", err)
	}
	if result.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0", result.TotalFiles)
	}
}

func TestPipelineIncrementalHash(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "inc.go")

	if err := os.WriteFile(srcFile, []byte("package main\n\nfunc Alpha() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pool := treesitter.NewParserPoolN(1)
	defer pool.Close()

	dbPath := filepath.Join(t.TempDir(), "inc.db")

	pipe := NewPipeline(tmpDir, dbPath, pool)
	r1, err := pipe.Run(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.TotalFiles < 1 {
		t.Fatalf("first run TotalFiles = %d, want >= 1", r1.TotalFiles)
	}

	pipe2 := NewPipeline(tmpDir, dbPath, pool)
	r2, err := pipe2.Run(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if r2.TotalFiles != 0 {
		t.Errorf("second run (no changes) TotalFiles = %d, want 0", r2.TotalFiles)
	}

	if err := os.WriteFile(srcFile, []byte("package main\n\nfunc Alpha() {}\nfunc Beta() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pipe3 := NewPipeline(tmpDir, dbPath, pool)
	r3, err := pipe3.Run(context.Background())
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if r3.TotalFiles != 1 {
		t.Errorf("third run (modified) TotalFiles = %d, want 1", r3.TotalFiles)
	}
}

func TestPass4_ImportEdgesResolved(t *testing.T) {
	tmpDir := t.TempDir()

	// go.mod module prefix — required to map module paths back to filesystem paths.
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/proj\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Internal package the main file imports (intra-workspace, resolvable).
	pkgDir := filepath.Join(tmpDir, "internal", "greet")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "greet.go"), []byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// main imports both the standard library (unresolvable) and the module's own package (resolvable).
	src := `package main

import (
	"fmt"
	"example.com/proj/internal/greet"
)

func hello() { fmt.Println(greet.Hello()) }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "code.db")
	pool := treesitter.NewParserPool()
	defer pool.Close()

	pipe := NewPipeline(tmpDir, dbPath, pool)
	result, err := pipe.Run(context.Background())
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if result.TotalFiles < 1 {
		t.Fatalf("TotalFiles = %d, want >= 1", result.TotalFiles)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var importResolved int
	err = db.QueryRow(`SELECT COUNT(*) FROM edges WHERE kind = 'import' AND target_id IS NOT NULL`).Scan(&importResolved)
	if err != nil {
		t.Fatalf("query import edges: %v", err)
	}
	if importResolved < 1 {
		t.Errorf("intra-workspace import edges with target_id = %d, want >= 1 (pass4 should resolve module imports)", importResolved)
	}

	var importUnresolved int
	err = db.QueryRow(`SELECT COUNT(*) FROM edges WHERE kind = 'import' AND target_id IS NULL`).Scan(&importUnresolved)
	if err != nil {
		t.Fatalf("query unresolved import edges: %v", err)
	}
	if importUnresolved < 1 {
		t.Error("no unresolved import edges — expected stdlib import (fmt) to stay NULL")
	}

	var importTotal int
	err = db.QueryRow(`SELECT COUNT(*) FROM edges WHERE kind = 'import'`).Scan(&importTotal)
	if err != nil {
		t.Fatalf("query import total: %v", err)
	}
	if importTotal == 0 {
		t.Error("no import edges found at all — extraction may be broken")
	}
}
