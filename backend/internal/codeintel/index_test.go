package codeintel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/weisyn/wescode/internal/codeintel/lspbridge"
	"github.com/weisyn/wescode/internal/treesitter"
)

func newTestIndex(t *testing.T) (*CodeIndex, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	pool := treesitter.NewParserPool()
	t.Cleanup(pool.Close)

	ci, err := NewCodeIndex(dbPath, pool)
	if err != nil {
		t.Fatalf("NewCodeIndex: %v", err)
	}
	t.Cleanup(func() { ci.Close() })
	return ci, dir
}

func TestCodeIndex_IndexAndSearch(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "main.go")
	os.WriteFile(goFile, []byte(`package main

func HelloWorld() {}

type UserService struct{}
`), 0644)

	if err := ci.IndexFile(context.Background(), goFile); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	syms, err := ci.SearchSymbols(context.Background(), "HelloWorld", 10)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected to find HelloWorld")
	}
	if syms[0].Name != "HelloWorld" {
		t.Errorf("expected HelloWorld, got %s", syms[0].Name)
	}
}

func TestCodeIndex_FindSymbol(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "svc.go")
	os.WriteFile(goFile, []byte(`package main

type MyService struct{}

func (s *MyService) DoWork() {}
`), 0644)

	ci.IndexFile(context.Background(), goFile)

	syms, err := ci.FindSymbol(context.Background(), "DoWork")
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected to find DoWork")
	}
}

func TestCodeIndex_SkipUnchanged(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "main.go")
	os.WriteFile(goFile, []byte("package main\n"), 0644)

	ci.IndexFile(context.Background(), goFile)
	if err := ci.IndexFile(context.Background(), goFile); err != nil {
		t.Fatalf("second IndexFile: %v", err)
	}
}

func TestCodeIndex_ListFileSymbols(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "lib.go")
	os.WriteFile(goFile, []byte(`package lib

func Alpha() {}
func Beta() {}
`), 0644)

	ci.IndexFile(context.Background(), goFile)

	syms, err := ci.ListFileSymbols(context.Background(), goFile)
	if err != nil {
		t.Fatalf("ListFileSymbols: %v", err)
	}
	if len(syms) < 2 {
		t.Fatalf("expected >= 2 symbols, got %d", len(syms))
	}
}

func TestCodeIndex_ListImports(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "main.go")
	os.WriteFile(goFile, []byte(`package main

import "fmt"
`), 0644)

	ci.IndexFile(context.Background(), goFile)

	imports, err := ci.ListImports(context.Background(), goFile)
	if err != nil {
		t.Fatalf("ListImports: %v", err)
	}
	if len(imports) == 0 {
		t.Fatal("expected imports")
	}
}

func TestClassifyTask(t *testing.T) {
	tests := []struct {
		input string
		want  TaskType
	}{
		{"explain how this works", TaskExplain},
		{"implement a new feature", TaskImplement},
		{"fix the null pointer bug", TaskFixBug},
		{"refactor the database layer", TaskRefactor},
		{"do something random", TaskGeneral},
	}
	for _, tt := range tests {
		got := ClassifyTask(tt.input)
		if got != tt.want {
			t.Errorf("ClassifyTask(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestBudgetAllocator(t *testing.T) {
	ba := NewBudgetAllocator(1000)
	if ba.Remaining() != 1000 {
		t.Errorf("initial remaining: %d", ba.Remaining())
	}
	ba.Consume(300)
	if ba.Remaining() != 700 {
		t.Errorf("after consume 300: %d", ba.Remaining())
	}
	if !ba.CanFit(700) {
		t.Error("should fit 700")
	}
	if ba.CanFit(701) {
		t.Error("should not fit 701")
	}
}

// ── CKG Phase 0 Tests ─────────────────────────────────────────────────────

func indexGoProject(t *testing.T, ci *CodeIndex, dir string) {
	t.Helper()
	ctx := context.Background()

	// File 1: service with exported and unexported functions
	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

import "fmt"

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }

func (s *UserService) CreateUser(name string) error {
	fmt.Println(name)
	return validate(name)
}

func (s *UserService) DeleteUser(id int) error {
	return nil
}

func validate(name string) error { return nil }
`), 0644)

	// File 2: handler that calls service
	os.WriteFile(filepath.Join(dir, "handler.go"), []byte(`package main

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) HandleCreate() {
	svc := NewUserService()
	svc.CreateUser("test")
}
`), 0644)

	// File 3: orphan — exported but never called
	os.WriteFile(filepath.Join(dir, "orphan.go"), []byte(`package main

func OrphanFunc() string { return "unused" }

type OrphanType struct{}

func helperPrivate() {}
`), 0644)

	for _, name := range []string{"service.go", "handler.go", "orphan.go"} {
		if err := ci.IndexFile(ctx, filepath.Join(dir, name)); err != nil {
			t.Fatalf("IndexFile %s: %v", name, err)
		}
	}
}

func TestFindOrphans(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	orphans, _, err := ci.FindOrphans(context.Background(), FindOrphanOpts{
		ExcludeMain:  true,
		ExcludeTests: true,
	})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}

	// OrphanFunc and OrphanType should be detected; DeleteUser may also appear.
	orphanNames := map[string]bool{}
	for _, o := range orphans {
		orphanNames[o.Symbol.Name] = true
	}

	if !orphanNames["OrphanFunc"] {
		t.Error("expected OrphanFunc to be detected as orphan")
	}
	if !orphanNames["OrphanType"] {
		t.Error("expected OrphanType to be detected as orphan")
	}
	// helperPrivate is not exported, should NOT appear.
	if orphanNames["helperPrivate"] {
		t.Error("unexported helperPrivate should not appear in orphan list")
	}
}

func TestFindOrphans_ExcludesCalledFunctions(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	orphans, _, err := ci.FindOrphans(context.Background(), FindOrphanOpts{
		ExcludeMain: true,
	})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}

	orphanNames := map[string]bool{}
	for _, o := range orphans {
		orphanNames[o.Symbol.Name] = true
	}

	// NewUserService and CreateUser are called by Handler, should NOT be orphans.
	if orphanNames["NewUserService"] {
		t.Error("NewUserService is called by HandleCreate, should not be orphan")
	}
	if orphanNames["CreateUser"] {
		t.Error("CreateUser is called by HandleCreate, should not be orphan")
	}
}

func TestImpactAnalysis(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	nodes, _, err := ci.ImpactAnalysis(context.Background(), "validate", 3)
	if err != nil {
		t.Fatalf("ImpactAnalysis: %v", err)
	}

	// validate is called by CreateUser, which is called by HandleCreate.
	if len(nodes) == 0 {
		t.Fatal("expected at least one impact node for validate")
	}

	hasCreateUser := false
	for _, n := range nodes {
		if n.Symbol.Name == "CreateUser" {
			hasCreateUser = true
			if n.Depth != 1 {
				t.Errorf("CreateUser should be depth 1, got %d", n.Depth)
			}
		}
	}
	if !hasCreateUser {
		t.Error("expected CreateUser as direct dependent of validate")
	}
}

func TestImpactAnalysis_MultiLevel(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	nodes, _, err := ci.ImpactAnalysis(context.Background(), "validate", 3)
	if err != nil {
		t.Fatalf("ImpactAnalysis: %v", err)
	}

	// validate → CreateUser → HandleCreate (depth 2)
	hasHandleCreate := false
	for _, n := range nodes {
		if n.Symbol.Name == "HandleCreate" {
			hasHandleCreate = true
			if n.Depth != 2 {
				t.Errorf("HandleCreate should be depth 2, got %d", n.Depth)
			}
		}
	}
	if !hasHandleCreate {
		t.Error("expected HandleCreate as depth-2 dependent of validate")
	}
}

func TestFindSimilar(t *testing.T) {
	ci, dir := newTestIndex(t)

	// Create functions with similar signatures for structural comparison.
	os.WriteFile(filepath.Join(dir, "auth.go"), []byte(`package main

func ValidateUser(name string) error { return nil }
func ValidateEmail(email string) error { return nil }
func ProcessPayment(amount int) error { return nil }
`), 0644)
	ci.IndexFile(context.Background(), filepath.Join(dir, "auth.go"))

	// Use signature-based similarity (FTS5 may not match partial word overlaps).
	results, err := ci.FindSimilar(context.Background(), "ValidateUser", FindSimilarOpts{
		Signature: "func ValidateUser(name string) error",
		Limit:     5,
		MinScore:  0.05,
	})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one similar result")
	}

	// ValidateEmail has a similar signature pattern (string param, error return).
	hasValidateEmail := false
	for _, r := range results {
		if r.Symbol.Name == "ValidateEmail" {
			hasValidateEmail = true
		}
	}
	if !hasValidateEmail {
		t.Error("expected ValidateEmail as similar to ValidateUser")
	}
}

// The version gate is the only migration mechanism (DEV-1): a stale
// user_version wipes and rebuilds, a current one keeps the data. Both halves
// have to be asserted — a gate that always wipes passes the first half alone
// and destroys the index on every startup.
func TestSchemaVersionGate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gate.db")
	pool := treesitter.NewParserPool()
	t.Cleanup(pool.Close)

	seed := func() {
		ci, err := NewCodeIndex(dbPath, pool)
		if err != nil {
			t.Fatalf("NewCodeIndex: %v", err)
		}
		if _, err := ci.DB().Exec(
			`INSERT INTO symbols (file_path, kind, name, line_start, line_end) VALUES ('a.go','function','Seeded',1,2)`,
		); err != nil {
			t.Fatalf("seed symbol: %v", err)
		}
		ci.Close()
	}
	countSeeded := func() int {
		ci, err := NewCodeIndex(dbPath, pool)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer ci.Close()
		var n int
		if err := ci.DB().QueryRow(`SELECT count(*) FROM symbols WHERE name='Seeded'`).Scan(&n); err != nil {
			t.Fatalf("count symbols: %v", err)
		}
		return n
	}

	seed()
	if n := countSeeded(); n != 1 {
		t.Fatalf("same-version reopen wiped the index: got %d seeded symbols, want 1", n)
	}

	// Forge a stale version stamp; the next open must rebuild from scratch.
	stale, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open for downgrade: %v", err)
	}
	if _, err := stale.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion-1)); err != nil {
		t.Fatalf("downgrade user_version: %v", err)
	}
	stale.Close()

	if n := countSeeded(); n != 0 {
		t.Fatalf("stale-version reopen kept old rows: got %d seeded symbols, want 0", n)
	}
}

func TestSymbolExportedFlag(t *testing.T) {
	ci, dir := newTestIndex(t)

	os.WriteFile(filepath.Join(dir, "vis.go"), []byte(`package main

func PublicFunc() {}
func privateFunc() {}
type PublicType struct{}
`), 0644)
	ci.IndexFile(context.Background(), filepath.Join(dir, "vis.go"))

	syms, err := ci.ListFileSymbols(context.Background(), filepath.Join(dir, "vis.go"))
	if err != nil {
		t.Fatalf("ListFileSymbols: %v", err)
	}

	for _, s := range syms {
		switch s.Name {
		case "PublicFunc":
			if !s.Exported {
				t.Error("PublicFunc should be exported")
			}
			if s.Visibility != "public" {
				t.Errorf("PublicFunc visibility = %q, want public", s.Visibility)
			}
		case "privateFunc":
			if s.Exported {
				t.Error("privateFunc should not be exported")
			}
			if s.Visibility != "private" {
				t.Errorf("privateFunc visibility = %q, want private", s.Visibility)
			}
		case "PublicType":
			if !s.Exported {
				t.Error("PublicType should be exported")
			}
		}
	}
}

func TestToolOrphans_EndToEnd(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	tool := NewOrphansTool(ci)
	result, err := tool.Call(context.Background(), []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestToolImpact_EndToEnd(t *testing.T) {
	ci, dir := newTestIndex(t)
	indexGoProject(t, ci, dir)

	tool := NewImpactTool(ci)
	result, err := tool.Call(context.Background(), []byte(`{"symbol":"validate"}`), nil)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestToolDuplicates_EndToEnd(t *testing.T) {
	ci, dir := newTestIndex(t)

	os.WriteFile(filepath.Join(dir, "funcs.go"), []byte(`package main

func CreateUser(name string) error { return nil }
func CreateAdmin(name string) error { return nil }
`), 0644)
	ci.IndexFile(context.Background(), filepath.Join(dir, "funcs.go"))

	tool := NewDuplicatesTool(ci)
	result, err := tool.Call(context.Background(), []byte(`{"name":"CreateUser"}`), nil)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content)
	}
}

// ── CKG Phase 0 Validation Tests ──────────────────────────────────────────
//
// These tests verify the CKG queries produce meaningful results on
// realistic multi-file codebases (ENG-1: validation criteria before code).

func TestCKG_FindOrphans_RealisticProject(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	// Simulate a realistic Go project with layered architecture.
	writeAndIndex(t, ci, dir, "handler.go", `package myapp

import "net/http"

func HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	svc := NewUserService()
	svc.Create(r.FormValue("name"))
}

func HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	svc := NewUserService()
	svc.Delete(r.FormValue("id"))
}
`)
	writeAndIndex(t, ci, dir, "service.go", `package myapp

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }

func (s *UserService) Create(name string) error {
	return validate(name)
}

func (s *UserService) Delete(id string) error {
	return nil
}

func validate(name string) error { return nil }
`)
	writeAndIndex(t, ci, dir, "orphan.go", `package myapp

func OrphanHelperOne() string { return "unused" }

func OrphanHelperTwo(x int) int { return x * 2 }

type OrphanConfig struct {
	Name string
}

func internalHelper() string { return "not exported, should not appear" }
`)
	writeAndIndex(t, ci, dir, "utils.go", `package myapp

func FormatName(first, last string) string { return first + " " + last }

func ParseID(raw string) (int, error) { return 0, nil }

func DeprecatedLegacyFunc() error { return nil }
`)

	orphans, _, err := ci.FindOrphans(ctx, FindOrphanOpts{ExcludeMain: true, ExcludeTests: true})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}

	orphanNames := map[string]bool{}
	for _, o := range orphans {
		orphanNames[o.Symbol.Name] = true
		t.Logf("orphan: %s (%s:%d) certainty=%.1f", o.Symbol.Name, o.Symbol.FilePath, o.Symbol.LineStart, o.Certainty)
	}

	// Validation: >= 3 true orphans expected.
	expectedOrphans := []string{"OrphanHelperOne", "OrphanHelperTwo", "OrphanConfig",
		"FormatName", "ParseID", "DeprecatedLegacyFunc"}
	found := 0
	for _, name := range expectedOrphans {
		if orphanNames[name] {
			found++
		}
	}
	if found < 3 {
		t.Errorf("expected >= 3 true orphans from %v, found %d", expectedOrphans, found)
	}

	// Validation: called functions should NOT be orphans.
	if orphanNames["validate"] {
		t.Error("validate() is called by Create — should not be orphan")
	}
	if orphanNames["NewUserService"] {
		t.Error("NewUserService() is called by handlers — should not be orphan")
	}
}

func TestCKG_ImpactAnalysis_MultiFileChain(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	writeAndIndex(t, ci, dir, "model.go", `package myapp

type User struct {
	Name  string
	Email string
}

func ValidateUser(u User) error { return nil }
`)
	writeAndIndex(t, ci, dir, "repo.go", `package myapp

func SaveUser(u User) error {
	if err := ValidateUser(u); err != nil {
		return err
	}
	return nil
}

func DeleteUser(id string) error { return nil }

func ListUsers() ([]User, error) { return nil, nil }
`)
	writeAndIndex(t, ci, dir, "service.go", `package myapp

func CreateUserFlow(name, email string) error {
	u := User{Name: name, Email: email}
	return SaveUser(u)
}

func UpdateUserFlow(id, name string) error {
	u := User{Name: name}
	return SaveUser(u)
}

func BulkImportUsers(names []string) error {
	for _, n := range names {
		CreateUserFlow(n, n+"@example.com")
	}
	return nil
}
`)
	writeAndIndex(t, ci, dir, "handler.go", `package myapp

func HandleCreate() {
	CreateUserFlow("test", "test@example.com")
}

func HandleBulkImport() {
	BulkImportUsers([]string{"a", "b"})
}

func HandleUpdate() {
	UpdateUserFlow("1", "new name")
}
`)

	// Validate: changing ValidateUser should show >= 3 downstream symbols.
	nodes, _, err := ci.ImpactAnalysis(ctx, "ValidateUser", 3)
	if err != nil {
		t.Fatalf("ImpactAnalysis(ValidateUser): %v", err)
	}
	for _, n := range nodes {
		t.Logf("impact[%d]: %s (%s:%d) via %s path=%v",
			n.Depth, n.Symbol.Name, n.Symbol.FilePath, n.Symbol.LineStart, n.EdgeKind, n.Path)
	}
	if len(nodes) < 3 {
		t.Errorf("ValidateUser impact: expected >= 3 downstream, got %d", len(nodes))
	}

	// Validate: path propagation for multi-level chains.
	for _, n := range nodes {
		if len(n.Path) < 2 {
			t.Errorf("node %s has incomplete path: %v", n.Symbol.Name, n.Path)
		}
		if n.Path[0] != "ValidateUser" {
			t.Errorf("node %s path should start with ValidateUser, got %v", n.Symbol.Name, n.Path)
		}
	}

	// Validate: SaveUser should show >= 4 downstream (CreateUserFlow,
	// UpdateUserFlow, BulkImportUsers, HandleCreate, HandleUpdate, HandleBulkImport).
	saveNodes, _, err := ci.ImpactAnalysis(ctx, "SaveUser", 3)
	if err != nil {
		t.Fatalf("ImpactAnalysis(SaveUser): %v", err)
	}
	if len(saveNodes) < 4 {
		t.Errorf("SaveUser impact: expected >= 4 downstream, got %d", len(saveNodes))
	}
}

// Type-only references (parameter types, struct field types) produce no edge:
// no extractor emits them, so a type used exclusively in signatures is
// indistinguishable from dead code. This test pins that blind spot so it stays
// visible — the previous version asserted the opposite via t.Logf and therefore
// passed either way. If type-reference extraction lands, this test must fail.
func TestCKG_TypeOnlyReferenceIsBlindSpot(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	writeAndIndex(t, ci, dir, "types.go", `package myapp

type Config struct {
	Name string
}
`)
	writeAndIndex(t, ci, dir, "funcs.go", `package myapp

func ProcessConfig(c Config) error {
	return nil
}
`)

	var edgesToConfig int
	if err := ci.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM edges WHERE target_name = 'Config' AND kind != 'import'`,
	).Scan(&edgesToConfig); err != nil {
		t.Fatalf("count edges to Config: %v", err)
	}
	if edgesToConfig != 0 {
		t.Fatalf("something now emits an edge for a type-only reference (%d edges to Config) — "+
			"type-reference extraction landed; update FindOrphans blind-spot docs and delete this test",
			edgesToConfig)
	}

	orphans, _, err := ci.FindOrphans(ctx, FindOrphanOpts{
		ExcludeMain:  true,
		ExcludeTests: true,
		KindFilter:   []string{"type"},
	})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	for _, o := range orphans {
		if o.Symbol.Name == "Config" {
			return // expected: the blind spot is real
		}
	}
	t.Fatal("Config was not reported as an orphan, yet no edge references it — " +
		"FindOrphans changed shape; re-derive what this test should pin")
}

func TestCKG_TargetIDResolution(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	writeAndIndex(t, ci, dir, "caller.go", `package myapp

func Caller() {
	Callee()
}
`)
	writeAndIndex(t, ci, dir, "callee.go", `package myapp

func Callee() string { return "hello" }
`)

	// After indexing both files, target_id should be resolved for same-name symbols.
	var resolvedCount int
	ci.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE target_id IS NOT NULL AND kind = 'call'`).Scan(&resolvedCount)

	t.Logf("resolved target_id edges: %d", resolvedCount)

	// Callee should not be an orphan since Caller calls it.
	orphans, _, err := ci.FindOrphans(ctx, FindOrphanOpts{ExcludeMain: true})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	for _, o := range orphans {
		if o.Symbol.Name == "Callee" {
			t.Error("Callee should not be orphan — Caller calls it")
		}
	}
}

func writeAndIndex(t *testing.T, ci *CodeIndex, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := ci.IndexFile(context.Background(), path); err != nil {
		t.Fatalf("index %s: %v", name, err)
	}
}

// Regression: Clear must drop ALL indexed data — the core CKG tables plus
// the auxiliary tables (mind_map, node_history, constraints,
// exploration_paths). Previously Clear only emptied edges/symbols/file_hashes,
// leaving stale auxiliary rows behind when switching workspaces.
func TestClearRemovesAllTables(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "a.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), goFile); err != nil {
		t.Fatalf("index file: %v", err)
	}

	w := ci.writerDB
	if _, err := w.Exec(`INSERT INTO mind_map (workspace_root, json_data, generated_at) VALUES ('/x', '{}', 'now')`); err != nil {
		t.Fatalf("seed mind_map: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO node_history (symbol_name, file_path, snapshot_at) VALUES ('Foo', ?, 't')`, goFile); err != nil {
		t.Fatalf("seed node_history: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO constraints (id, rule, kind, priority, source) VALUES ('c1', 'r', 'k', 1, 'test')`); err != nil {
		t.Fatalf("seed constraints: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO exploration_paths (run_id, session_id, paths_json) VALUES ('r1', 's1', '[]')`); err != nil {
		t.Fatalf("seed exploration_paths: %v", err)
	}

	ci.Clear()

	for _, tbl := range []string{"symbols", "edges", "file_hashes", "mind_map", "node_history", "constraints", "exploration_paths"} {
		var n int
		if err := w.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("%s count = %d after Clear, want 0", tbl, n)
		}
	}
}

// countingLSP wraps NoopLSP and counts References calls so tests can assert
// how many times the enrichment loop actually queried the language server.
type countingLSP struct {
	NoopLSP
	refs  []lspbridge.DefinitionLocation
	err   error
	calls int
}

func (m *countingLSP) References(_ context.Context, _ string, _, _ int) ([]lspbridge.DefinitionLocation, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.refs, nil
}

// seedEnrichCandidates inserts n `inferred` implements edges backed by .go
// symbols so EnrichWithLSP finds exactly n candidates for language "go".
//
// The edges must be bound: EnrichWithLSP only promotes edges that already have
// a target_id (References confirms a relation, it does not return a symbol id),
// and the schema refuses an `inferred` row with target_id NULL. So all n point
// at one shared interface symbol — n implementors of one interface.
func seedEnrichCandidates(t *testing.T, ci *CodeIndex, n int) {
	t.Helper()
	ifaceRes, err := ci.writerDB.Exec(
		`INSERT INTO symbols (file_path, kind, name, line_start, line_end)
		 VALUES ('/fake/root/iface.go', 'interface', 'Target', 1, 1)`)
	if err != nil {
		t.Fatalf("insert interface symbol: %v", err)
	}
	ifaceID, err := ifaceRes.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId iface: %v", err)
	}
	for i := 0; i < n; i++ {
		res, err := ci.writerDB.Exec(
			`INSERT INTO symbols (file_path, kind, name, line_start, line_end)
			 VALUES (?, 'function', ?, ?, ?)`,
			fmt.Sprintf("/fake/root/file%d.go", i), fmt.Sprintf("Fn%d", i), i+1, i+1)
		if err != nil {
			t.Fatalf("insert symbol %d: %v", i, err)
		}
		symID, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId %d: %v", i, err)
		}
		if _, err := ci.writerDB.Exec(
			`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
			 VALUES (?, ?, 'Target', 'implements', ?, 'tree-sitter')`,
			symID, ifaceID, ResolutionInferred); err != nil {
			t.Fatalf("insert edge %d: %v", i, err)
		}
	}
}

// TestEnrichWithLSP_LangCircuitBreaker verifies the lang-level circuit
// breaker: when LSP returns empty results for a language (no reference
// provider — e.g. Go extension not activated), only the first
// maxConsecutiveEmpty candidates are queried; the rest are skipped. This
// prevents the cold-boot pattern of 100+ identical fruitless references
// requests against a language with no LSP server.
func TestEnrichWithLSP_LangCircuitBreaker(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedEnrichCandidates(t, ci, 6)

	lsp := &countingLSP{refs: nil} // empty results — LSP unavailable for go
	enriched := ci.EnrichWithLSP(context.Background(), lsp)
	if enriched != 0 {
		t.Errorf("enriched = %d, want 0 (no references found)", enriched)
	}
	if lsp.calls != 2 {
		t.Errorf("References calls = %d, want 2 (circuit breaker trips after maxConsecutiveEmpty)", lsp.calls)
	}
}

// TestEnrichWithLSP_NoBreakerOnSuccess verifies successful references reset
// the circuit breaker: every candidate is queried and each confirmed edge is
// upgraded — the breaker must not suppress legitimate LSP enrichment.
func TestEnrichWithLSP_NoBreakerOnSuccess(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedEnrichCandidates(t, ci, 3)

	lsp := &countingLSP{refs: []lspbridge.DefinitionLocation{
		{Path: "/fake/root/other.go", Line: 1, Column: 1},
	}}
	enriched := ci.EnrichWithLSP(context.Background(), lsp)
	if enriched != 3 {
		t.Errorf("enriched = %d, want 3", enriched)
	}
	if lsp.calls != 3 {
		t.Errorf("References calls = %d, want 3 (successes reset the breaker)", lsp.calls)
	}
}

// ---------------------------------------------------------------------------
// FileReachabilityProfile tests (Phase 3 — INV-CKG-SINGLE-CLASS)
// ---------------------------------------------------------------------------

// insertSymbol inserts a symbol and returns its id.
func insertSymbol(t *testing.T, ci *CodeIndex, filePath, kind, name string, reachClass string) int64 {
	t.Helper()
	res, err := ci.writerDB.Exec(
		`INSERT INTO symbols (file_path, kind, name, line_start, line_end, reachability_class)
		 VALUES (?, ?, ?, 1, 1, ?)`, filePath, kind, name, reachClass)
	if err != nil {
		t.Fatalf("insertSymbol(%s, %s): %v", kind, name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// insertCallEdge inserts a call edge. For bound resolutions (exact/inferred),
// targetID must be non-nil; for unresolved/ambiguous it must be nil.
func insertCallEdge(t *testing.T, ci *CodeIndex, sourceID int64, targetID *int64, resolution Resolution) {
	t.Helper()
	if targetID != nil {
		_, err := ci.writerDB.Exec(
			`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
			 VALUES (?, ?, 'T', 'call', ?, 'tree-sitter')`, sourceID, *targetID, resolution)
		if err != nil {
			t.Fatalf("insertCallEdge(bound): %v", err)
		}
	} else {
		_, err := ci.writerDB.Exec(
			`INSERT INTO edges (source_id, target_name, kind, resolution, source)
			 VALUES (?, 'T', 'call', ?, 'tree-sitter')`, sourceID, resolution)
		if err != nil {
			t.Fatalf("insertCallEdge(unbound): %v", err)
		}
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestFileReachabilityProfile_EmptyFile(t *testing.T) {
	ci, _ := newTestIndex(t)
	fp, err := ci.FileReachabilityProfile("nonexistent.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.TotalFunctions != 0 {
		t.Errorf("TotalFunctions = %d, want 0", fp.TotalFunctions)
	}
	if fp.EdgeResolutionRate != -1 {
		t.Errorf("EdgeResolutionRate = %v, want -1", fp.EdgeResolutionRate)
	}
}

func TestFileReachabilityProfile_MixedClasses(t *testing.T) {
	ci, _ := newTestIndex(t)
	const file = "pkg/mixed.go"

	// Insert functions with various reachability classes.
	insertSymbol(t, ci, file, "function", "FnConnected", "connected")
	insertSymbol(t, ci, file, "function", "FnSourceOnly", "source_only")
	insertSymbol(t, ci, file, "method", "FnSinkOnly", "sink_only")
	insertSymbol(t, ci, file, "function", "FnNameReachable", "name_reachable")
	insertSymbol(t, ci, file, "function", "FnIsolated1", "isolated")
	insertSymbol(t, ci, file, "function", "FnIsolated2", "isolated")
	insertSymbol(t, ci, file, "method", "FnExempt", "structural_exempt")

	// type/interface/class are now classified; only variable is excluded.
	insertSymbol(t, ci, file, "type", "MyStruct", "connected")
	insertSymbol(t, ci, file, "variable", "globalVar", "isolated")

	fp, err := ci.FileReachabilityProfile(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.TotalFunctions != 8 {
		t.Errorf("TotalFunctions = %d, want 8", fp.TotalFunctions)
	}
	if fp.Connected != 2 {
		t.Errorf("Connected = %d, want 2", fp.Connected)
	}
	if fp.SourceOnly != 1 {
		t.Errorf("SourceOnly = %d, want 1", fp.SourceOnly)
	}
	if fp.SinkOnly != 1 {
		t.Errorf("SinkOnly = %d, want 1", fp.SinkOnly)
	}
	if fp.NameReachable != 1 {
		t.Errorf("NameReachable = %d, want 1", fp.NameReachable)
	}
	if fp.Isolated != 2 {
		t.Errorf("Isolated = %d, want 2", fp.Isolated)
	}
	if fp.StructuralExempt != 1 {
		t.Errorf("StructuralExempt = %d, want 1", fp.StructuralExempt)
	}
	// No edges → EdgeResolutionRate stays -1.
	if fp.EdgeResolutionRate != -1 {
		t.Errorf("EdgeResolutionRate = %v, want -1 (no edges)", fp.EdgeResolutionRate)
	}
}

func TestFileReachabilityProfile_EdgeResolution(t *testing.T) {
	ci, _ := newTestIndex(t)
	const file = "pkg/edges.go"

	// Create a target symbol for bound edges.
	targetID := insertSymbol(t, ci, file, "function", "Target", "connected")
	srcA := insertSymbol(t, ci, file, "function", "CallerA", "connected")
	srcB := insertSymbol(t, ci, file, "function", "CallerB", "connected")
	srcC := insertSymbol(t, ci, file, "function", "CallerC", "connected")
	srcD := insertSymbol(t, ci, file, "function", "CallerD", "connected")

	// 2 bound (exact + inferred), 2 unbound (ambiguous + unresolved).
	insertCallEdge(t, ci, srcA, int64Ptr(targetID), ResolutionExact)
	insertCallEdge(t, ci, srcB, int64Ptr(targetID), ResolutionInferred)
	insertCallEdge(t, ci, srcC, nil, ResolutionAmbiguous)
	insertCallEdge(t, ci, srcD, nil, ResolutionUnresolved)

	fp, err := ci.FileReachabilityProfile(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 bound / 4 total = 0.5
	want := 0.5
	if fp.EdgeResolutionRate != want {
		t.Errorf("EdgeResolutionRate = %v, want %v", fp.EdgeResolutionRate, want)
	}
}

func TestFileReachabilityProfile_AllBoundEdges(t *testing.T) {
	ci, _ := newTestIndex(t)
	const file = "pkg/allbound.go"

	target := insertSymbol(t, ci, file, "function", "Target", "connected")
	src := insertSymbol(t, ci, file, "function", "Caller", "connected")

	insertCallEdge(t, ci, src, int64Ptr(target), ResolutionExact)
	insertCallEdge(t, ci, src, int64Ptr(target), ResolutionInferred)

	fp, err := ci.FileReachabilityProfile(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.EdgeResolutionRate != 1.0 {
		t.Errorf("EdgeResolutionRate = %v, want 1.0", fp.EdgeResolutionRate)
	}
}

func TestFileReachabilityProfile_NoBoundEdges(t *testing.T) {
	ci, _ := newTestIndex(t)
	const file = "pkg/nobound.go"

	src := insertSymbol(t, ci, file, "function", "Caller", "connected")

	insertCallEdge(t, ci, src, nil, ResolutionUnresolved)
	insertCallEdge(t, ci, src, nil, ResolutionAmbiguous)

	fp, err := ci.FileReachabilityProfile(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.EdgeResolutionRate != 0.0 {
		t.Errorf("EdgeResolutionRate = %v, want 0.0", fp.EdgeResolutionRate)
	}
}

// ---------------------------------------------------------------------------
// Phase 4: Incremental classification tests
// ---------------------------------------------------------------------------

// insertSymbolEx inserts a symbol with parent and exported fields for RPC
// handler and interface declaration tests.
func insertSymbolEx(t *testing.T, ci *CodeIndex, filePath, kind, name, reachClass, parent string, exported bool) int64 {
	t.Helper()
	exp := 0
	if exported {
		exp = 1
	}
	res, err := ci.writerDB.Exec(
		`INSERT INTO symbols (file_path, kind, name, line_start, line_end, reachability_class, parent, exported)
		 VALUES (?, ?, ?, 1, 1, ?, ?, ?)`, filePath, kind, name, reachClass, parent, exp)
	if err != nil {
		t.Fatalf("insertSymbolEx(%s, %s): %v", kind, name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// insertNameEdge inserts a call edge with a specific target_name (for
// name_reachable tests). The edge has no target_id.
func insertNameEdge(t *testing.T, ci *CodeIndex, sourceID int64, targetName string) {
	t.Helper()
	_, err := ci.writerDB.Exec(
		`INSERT INTO edges (source_id, target_name, kind, resolution, source)
		 VALUES (?, ?, 'call', 'unresolved', 'tree-sitter')`, sourceID, targetName)
	if err != nil {
		t.Fatalf("insertNameEdge: %v", err)
	}
}

// queryClassAndTag returns (reachability_class, structural_tag) for a symbol.
func queryClassAndTag(t *testing.T, ci *CodeIndex, symbolID int64) (string, string) {
	t.Helper()
	var class string
	var tag sql.NullString
	err := ci.writerDB.QueryRow(
		`SELECT reachability_class, structural_tag FROM symbols WHERE id = ?`, symbolID).
		Scan(&class, &tag)
	if err != nil {
		t.Fatalf("queryClassAndTag(%d): %v", symbolID, err)
	}
	return class, tag.String
}

// --- collectReachabilityNeighbors ---

func TestCollectNeighbors_EmptySeeds(t *testing.T) {
	ci, _ := newTestIndex(t)
	result := ci.collectReachabilityNeighbors(context.Background(), ci.writerDB, nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestCollectNeighbors_OutboundEdge(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/a.go", "function", "Caller", "connected")
	tgtID := insertSymbol(t, ci, "pkg/b.go", "function", "Target", "connected")
	insertCallEdge(t, ci, srcID, int64Ptr(tgtID), ResolutionExact)

	result := ci.collectReachabilityNeighbors(context.Background(), ci.writerDB, []string{"pkg/a.go"})
	has := make(map[string]bool)
	for _, p := range result {
		has[p] = true
	}
	if !has["pkg/a.go"] {
		t.Error("seed path pkg/a.go missing")
	}
	if !has["pkg/b.go"] {
		t.Error("outbound neighbor pkg/b.go missing")
	}
}

func TestCollectNeighbors_InboundEdge(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/c.go", "function", "Caller", "connected")
	tgtID := insertSymbol(t, ci, "pkg/a.go", "function", "Target", "connected")
	insertCallEdge(t, ci, srcID, int64Ptr(tgtID), ResolutionExact)

	result := ci.collectReachabilityNeighbors(context.Background(), ci.writerDB, []string{"pkg/a.go"})
	has := make(map[string]bool)
	for _, p := range result {
		has[p] = true
	}
	if !has["pkg/a.go"] {
		t.Error("seed path pkg/a.go missing")
	}
	if !has["pkg/c.go"] {
		t.Error("inbound neighbor pkg/c.go missing")
	}
}

func TestCollectNeighbors_NameOnlyExcluded(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/a.go", "function", "Caller", "connected")
	_ = insertSymbol(t, ci, "pkg/d.go", "function", "SomeTarget", "connected")
	// Name-only edge (no target_id) should NOT expand neighbors.
	insertCallEdge(t, ci, srcID, nil, ResolutionUnresolved)

	result := ci.collectReachabilityNeighbors(context.Background(), ci.writerDB, []string{"pkg/a.go"})
	has := make(map[string]bool)
	for _, p := range result {
		has[p] = true
	}
	if has["pkg/d.go"] {
		t.Error("name-only neighbor pkg/d.go should NOT be in result")
	}
}

// --- ClassifyReachabilityForFiles ---

func TestIncrClassify_EmptyPaths(t *testing.T) {
	ci, _ := newTestIndex(t)
	if err := ci.ClassifyReachabilityForFiles(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIncrClassify_Isolated(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/lone.go", "function", "Lonely", "connected")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/lone.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, id)
	if cls != "isolated" {
		t.Errorf("class = %q, want isolated", cls)
	}
	if tag != "" {
		t.Errorf("tag = %q, want empty", tag)
	}
}

func TestIncrClassify_SourceOnly(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/src.go", "function", "Caller", "connected")
	tgtID := insertSymbol(t, ci, "pkg/tgt.go", "function", "Target", "connected")
	insertCallEdge(t, ci, srcID, int64Ptr(tgtID), ResolutionExact)

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/src.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, srcID)
	if cls != "source_only" {
		t.Errorf("class = %q, want source_only", cls)
	}
}

func TestIncrClassify_SinkOnly(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/src.go", "function", "Caller", "connected")
	tgtID := insertSymbol(t, ci, "pkg/sink.go", "function", "Sink", "connected")
	insertCallEdge(t, ci, srcID, int64Ptr(tgtID), ResolutionExact)

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/sink.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, tgtID)
	if cls != "sink_only" {
		t.Errorf("class = %q, want sink_only", cls)
	}
}

func TestIncrClassify_Connected(t *testing.T) {
	ci, _ := newTestIndex(t)
	aID := insertSymbol(t, ci, "pkg/mid.go", "function", "Middle", "connected")
	bID := insertSymbol(t, ci, "pkg/caller.go", "function", "Caller", "connected")
	cID := insertSymbol(t, ci, "pkg/target.go", "function", "Target", "connected")
	insertCallEdge(t, ci, bID, int64Ptr(aID), ResolutionExact)
	insertCallEdge(t, ci, aID, int64Ptr(cID), ResolutionExact)

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/mid.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, aID)
	if cls != "connected" {
		t.Errorf("class = %q, want connected", cls)
	}
}

func TestIncrClassify_NameReachable(t *testing.T) {
	ci, _ := newTestIndex(t)
	nrID := insertSymbol(t, ci, "pkg/nr.go", "function", "Handler", "connected")
	srcID := insertSymbol(t, ci, "pkg/ref.go", "function", "Referrer", "connected")
	// Name-only reference: edge target_name="Handler" with no target_id.
	insertNameEdge(t, ci, srcID, "Handler")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/nr.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, nrID)
	if cls != "name_reachable" {
		t.Errorf("class = %q, want name_reachable", cls)
	}
}

func TestIncrClassify_Testdata(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/testdata/fix.go", "function", "Fix", "connected")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/testdata/fix.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, id)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
	if tag != "testdata" {
		t.Errorf("tag = %q, want testdata", tag)
	}
}

func TestIncrClassify_BuildTag(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/impl_linux.go", "function", "LinuxOnly", "connected")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/impl_linux.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, id)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
	if tag != "build_tag" {
		t.Errorf("tag = %q, want build_tag", tag)
	}
}

func TestIncrClassify_TestFileExempt(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/foo_test.go", "function", "TestFoo", "connected")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/foo_test.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, id)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
}

func TestIncrClassify_ExternalEntry(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "cmd/main.go", "function", "main", "connected")
	ci.SetExternalEntryPoints([]string{"main"})

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"cmd/main.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, id)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
	if tag != "external_entry" {
		t.Errorf("tag = %q, want external_entry", tag)
	}
}

func TestIncrClassify_RPCHandler(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbolEx(t, ci, "svc/handler.go", "method", "CreateUser", "connected", "UserService", true)

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"svc/handler.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, id)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
	if tag != "rpc_handler" {
		t.Errorf("tag = %q, want rpc_handler", tag)
	}
}

func TestIncrClassify_InterfaceDecl(t *testing.T) {
	ci, _ := newTestIndex(t)
	_ = insertSymbolEx(t, ci, "pkg/iface.go", "interface", "Reader", "connected", "", false)
	methodID := insertSymbolEx(t, ci, "pkg/iface.go", "method", "Read", "connected", "Reader", false)

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/iface.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, methodID)
	if cls != "structural_exempt" {
		t.Errorf("class = %q, want structural_exempt", cls)
	}
	if tag != "interface_decl" {
		t.Errorf("tag = %q, want interface_decl", tag)
	}
}

func TestIncrClassify_NeighborExpansion(t *testing.T) {
	ci, _ := newTestIndex(t)
	srcID := insertSymbol(t, ci, "pkg/a.go", "function", "Caller", "connected")
	tgtID := insertSymbol(t, ci, "pkg/b.go", "function", "Target", "connected")
	insertCallEdge(t, ci, srcID, int64Ptr(tgtID), ResolutionExact)

	// Pre-seed Target with stale class to verify neighbor reclassification.
	if _, err := ci.writerDB.Exec(
		`UPDATE symbols SET reachability_class = 'isolated' WHERE id = ?`, tgtID); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	// Classify only pkg/a.go; pkg/b.go should be expanded as neighbor.
	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/a.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cls, _ := queryClassAndTag(t, ci, tgtID)
	if cls != "sink_only" {
		t.Errorf("neighbor Target class = %q, want sink_only", cls)
	}
	cls2, _ := queryClassAndTag(t, ci, srcID)
	if cls2 != "source_only" {
		t.Errorf("seed Caller class = %q, want source_only", cls2)
	}
}

func TestIncrClassify_NonFunctionUnchanged(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/types.go", "variable", "GlobalVar", "connected")

	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"pkg/types.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, id)
	if cls != "connected" {
		t.Errorf("class = %q, want connected (variable kind unchanged)", cls)
	}
}

func TestIncrClassify_PathNormalization(t *testing.T) {
	ci, _ := newTestIndex(t)
	id := insertSymbol(t, ci, "pkg/norm.go", "function", "Normalized", "connected")

	// Leading ./ should be cleaned by IndexPath → matches stored "pkg/norm.go".
	if err := ci.ClassifyReachabilityForFiles(context.Background(), []string{"./pkg/norm.go"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, id)
	if cls != "isolated" {
		t.Errorf("class = %q, want isolated (path normalization)", cls)
	}
}

// ---------------------------------------------------------------------------
// ClassifyReachability (full classification) direct tests
// ---------------------------------------------------------------------------

// TestFullClassify_AllClasses verifies that ClassifyReachability produces the
// correct reachability_class for all six classification outcomes.
func TestFullClassify_AllClasses(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// 1. connected: mutual ID-bound edges (A→B and B→A)
	cA := insertSymbol(t, ci, "pkg/a.go", "function", "ConnA", "connected")
	cB := insertSymbol(t, ci, "pkg/a.go", "function", "ConnB", "connected")
	insertCallEdge(t, ci, cA, int64Ptr(cB), ResolutionExact)
	insertCallEdge(t, ci, cB, int64Ptr(cA), ResolutionExact)

	// 2. isolated: no edges whatsoever
	iso := insertSymbol(t, ci, "pkg/b.go", "function", "Isolated", "connected")

	// 3. source_only: has outbound ID-bound edge, no inbound
	src := insertSymbol(t, ci, "pkg/c.go", "function", "SourceOnly", "connected")
	sink := insertSymbol(t, ci, "pkg/c.go", "function", "SinkOnly", "connected")
	insertCallEdge(t, ci, src, int64Ptr(sink), ResolutionExact)
	// sink: has inbound from src, no outbound → sink_only
	// src: has outbound to sink, no inbound → source_only

	// 5. name_reachable: no ID-bound edges, but a name-edge points to it
	nameR := insertSymbol(t, ci, "pkg/d.go", "function", "NameReachable", "connected")
	nameRSrc := insertSymbol(t, ci, "pkg/d.go", "function", "NameReachableCaller", "connected")
	insertNameEdge(t, ci, nameRSrc, "NameReachable")

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}

	tests := []struct {
		id   int64
		name string
		want string
	}{
		{cA, "ConnA", "connected"},
		{cB, "ConnB", "connected"},
		{iso, "Isolated", "isolated"},
		{src, "SourceOnly", "source_only"},
		{sink, "SinkOnly", "sink_only"},
		{nameR, "NameReachable", "name_reachable"},
	}
	for _, tt := range tests {
		cls, _ := queryClassAndTag(t, ci, tt.id)
		if cls != tt.want {
			t.Errorf("%s: class = %q, want %q", tt.name, cls, tt.want)
		}
	}
}

// TestFullClassify_StructuralTags verifies structural_tag assignment for
// testdata, build_tag, external_entry, rpc_handler, and interface_decl.
func TestFullClassify_StructuralTags(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// testdata: file under testdata/ directory
	tdID := insertSymbol(t, ci, "pkg/testdata/fixture.go", "function", "FixtureHelper", "connected")

	// build_tag: platform-specific file, isolated (no edges)
	btID := insertSymbol(t, ci, "pkg/util_darwin.go", "function", "DarwinOnly", "connected")

	// external_entry: registered as external entry point
	eeID := insertSymbol(t, ci, "pkg/tools.go", "function", "HandleReadFile", "connected")
	ci.SetExternalEntryPoints([]string{"HandleReadFile"})

	// rpc_handler: exported method on a *Service parent, isolated
	rpcID := insertSymbolEx(t, ci, "pkg/rpc.go", "method", "Execute", "connected", "ToolService", true)

	// interface_decl: method whose parent is an interface in the same file
	ifaceID := insertSymbol(t, ci, "pkg/iface.go", "interface", "Processor", "connected")
	implID := insertSymbolEx(t, ci, "pkg/iface.go", "method", "Process", "connected", "Processor", true)
	_ = ifaceID

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}

	tests := []struct {
		id      int64
		name    string
		wantCls string
		wantTag string
	}{
		{tdID, "testdata", "structural_exempt", "testdata"},
		{btID, "build_tag", "structural_exempt", "build_tag"},
		{eeID, "external_entry", "structural_exempt", "external_entry"},
		{rpcID, "rpc_handler", "structural_exempt", "rpc_handler"},
		{implID, "interface_decl", "structural_exempt", "interface_decl"},
	}
	for _, tt := range tests {
		cls, tag := queryClassAndTag(t, ci, tt.id)
		if cls != tt.wantCls {
			t.Errorf("%s: class = %q, want %q", tt.name, cls, tt.wantCls)
		}
		if tag != tt.wantTag {
			t.Errorf("%s: tag = %q, want %q", tt.name, tag, tt.wantTag)
		}
	}
}

// TestFullClassify_TestFileIsolated verifies that isolated _test.go functions
// become structural_exempt.
func TestFullClassify_TestFileIsolated(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// Isolated test function (no edges) → structural_exempt
	isoTest := insertSymbol(t, ci, "pkg/foo_test.go", "function", "TestFoo", "connected")

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, isoTest)
	if cls != "structural_exempt" {
		t.Errorf("isolated _test.go: class = %q, want structural_exempt", cls)
	}
}

// TestFullClassify_TestFileWithEdges verifies that _test.go functions WITH
// edges retain their functional classification (not forced to structural_exempt).
func TestFullClassify_TestFileWithEdges(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// Test function with outbound edge → source_only (not structural_exempt)
	testFn := insertSymbol(t, ci, "pkg/foo_test.go", "function", "TestFoo", "connected")
	target := insertSymbol(t, ci, "pkg/foo.go", "function", "Foo", "connected")
	insertCallEdge(t, ci, testFn, int64Ptr(target), ResolutionExact)

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, testFn)
	if cls == "structural_exempt" {
		t.Errorf("_test.go with edges should NOT be structural_exempt, got %q", cls)
	}
	if cls != "source_only" {
		t.Errorf("_test.go with outbound edge: class = %q, want source_only", cls)
	}
}

// TestFullClassify_RPCConnectedNotOverridden verifies that RPC handler methods
// that are already 'connected' or 'sink_only' are NOT overridden to structural_exempt.
func TestFullClassify_RPCConnectedNotOverridden(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// RPC handler method with inbound edge → already connected, should stay connected
	caller := insertSymbol(t, ci, "pkg/router.go", "function", "Route", "connected")
	rpc := insertSymbolEx(t, ci, "pkg/svc.go", "method", "Serve", "connected", "APIService", true)
	insertCallEdge(t, ci, caller, int64Ptr(rpc), ResolutionExact)

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}
	cls, tag := queryClassAndTag(t, ci, rpc)
	// rpc has inbound edge → sink_only or connected; RPC tagging skips connected/sink_only
	if cls == "structural_exempt" {
		t.Errorf("connected RPC handler should NOT be overridden; class = %q, tag = %q", cls, tag)
	}
}

// TestFullClassify_VariableKindUntouched verifies that non-function/method/type/
// interface/class kinds are not modified by ClassifyReachability.
func TestFullClassify_VariableKindUntouched(t *testing.T) {
	ci, _ := newTestIndex(t)
	ctx := context.Background()

	// Insert a variable — should not be touched by classification
	varID, err := ci.writerDB.Exec(
		`INSERT INTO symbols (file_path, kind, name, line_start, line_end, reachability_class)
		 VALUES ('pkg/vars.go', 'variable', 'GlobalVar', 1, 1, 'connected')`)
	if err != nil {
		t.Fatalf("insert variable: %v", err)
	}
	id, _ := varID.LastInsertId()

	if err := ci.ClassifyReachability(ctx); err != nil {
		t.Fatalf("ClassifyReachability: %v", err)
	}
	cls, _ := queryClassAndTag(t, ci, id)
	if cls != "connected" {
		t.Errorf("variable kind should be untouched; class = %q, want connected", cls)
	}
}
