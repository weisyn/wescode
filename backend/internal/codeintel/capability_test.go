package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/message"
	"github.com/weisyn/wesgine/tool"

	"github.com/weisyn/wescode/internal/treesitter"
)

// ── CodeAssembler wrapper 模式 ──────────────────────────────────────────────

type mockAssembler struct {
	called bool
}

func (m *mockAssembler) Assemble(ctx context.Context, params *wesgine.AssembleParams) (*wesgine.AssembleResult, error) {
	m.called = true
	return &wesgine.AssembleResult{
		Messages:       params.Messages,
		TokenEstimate:  1000,
		RemainingInput: 80_000, // post-assemble remainder; CodeAssembler reads this
	}, nil
}

func TestCodeAssembler_DelegatesToInner(t *testing.T) {
	inner := &mockAssembler{}
	retriever := newTestRetriever(t)
	asm := NewCodeAssembler(retriever)

	// Simulate wrapper wiring
	wrapper := asm.WrapperFn()
	wrapped := wrapper(inner)

	msgs := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.NewTextBlock("hello")}},
	}

	result, err := wrapped.Assemble(context.Background(), &wesgine.AssembleParams{
		Messages: msgs,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !inner.called {
		t.Error("inner assembler should have been called")
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestCodeAssembler_InjectsCodeContext(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	os.WriteFile(goFile, []byte("package main\n\nfunc HelloWorld() {\n\tprintln(\"hello\")\n}\n"), 0644)

	ts := treesitter.NewParserPool()
	defer ts.Close()

	idx, err := NewCodeIndex(filepath.Join(dir, "test.db"), ts)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.IndexFile(context.Background(), goFile); err != nil {
		t.Fatal(err)
	}

	retriever := NewRetriever(idx, ts)
	asm := NewCodeAssembler(retriever)
	asm.UpdateEditorState(EditorState{
		FocusFile:  goFile,
		CursorLine: 2,
	})

	inner := &mockAssembler{}
	wrapper := asm.WrapperFn()
	wrapped := wrapper(inner)

	msgs := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.NewTextBlock("explain HelloWorld")}},
	}
	result, err := wrapped.Assemble(context.Background(), &wesgine.AssembleParams{
		Messages:         msgs,
		MaxTokenEstimate: 100000,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Should have more messages than input (code context injected)
	if len(result.Messages) <= len(msgs) {
		t.Error("expected code context to be injected as additional message")
	}

	// Check that injected message contains code
	found := false
	for _, m := range result.Messages {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "Code Context") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected [Code Context] message to be injected")
	}
}

// ── Retriever 真实 Go 项目检索 ──────────────────────────────────────────────

func TestRetriever_FocusFileAndCursorFunction(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "handler.go")
	content := `package main

import "fmt"

func HandleRequest() error {
	fmt.Println("handling")
	return nil
}

func HelperFunc() string {
	return "helper"
}
`
	os.WriteFile(goFile, []byte(content), 0644)

	ts := treesitter.NewParserPool()
	defer ts.Close()

	idx, err := NewCodeIndex(filepath.Join(dir, "test.db"), ts)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.IndexFile(context.Background(), goFile); err != nil {
		t.Fatal(err)
	}

	retriever := NewRetriever(idx, ts)

	state := EditorState{
		FocusFile:  goFile,
		CursorLine: 5, // inside HandleRequest
	}

	fragments := retriever.RetrieveV2(context.Background(), state, "fix the error handling", 50000, ContextNeed{TaskType: TaskFixBug})
	if len(fragments) == 0 {
		t.Fatal("expected at least one fragment")
	}

	hasFocusFile := false
	hasCursorFn := false
	for _, f := range fragments {
		if f.Kind == FragmentFocusFile {
			hasFocusFile = true
		}
		if f.Kind == FragmentFunction && f.Symbol == "HandleRequest" {
			hasCursorFn = true
		}
	}
	if !hasFocusFile {
		t.Error("expected focus file fragment")
	}
	if !hasCursorFn {
		t.Error("expected cursor function fragment for HandleRequest")
	}
}

func TestRetriever_ClassifiesTask(t *testing.T) {
	tests := []struct {
		msg  string
		want TaskType
	}{
		{"explain what this function does", TaskExplain},
		{"implement a new user service", TaskImplement},
		{"fix the nil pointer bug", TaskFixBug},
		{"refactor the auth module", TaskRefactor},
		{"make it better", TaskGeneral},
	}
	for _, tt := range tests {
		got := ClassifyTask(tt.msg)
		if got != tt.want {
			t.Errorf("ClassifyTask(%q) = %s, want %s", tt.msg, got, tt.want)
		}
	}
}

// ── 索引增量更新 ────────────────────────────────────────────────────────────

func TestCodeIndex_IncrementalUpdate(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "svc.go")
	os.WriteFile(goFile, []byte("package main\n\nfunc OldFunc() {}\n"), 0644)

	ts := treesitter.NewParserPool()
	defer ts.Close()

	idx, err := NewCodeIndex(filepath.Join(dir, "test.db"), ts)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	ctx := context.Background()

	idx.IndexFile(ctx, goFile)
	syms, _ := idx.FindSymbol(ctx, "OldFunc")
	if len(syms) == 0 {
		t.Fatal("OldFunc should be indexed")
	}

	// Update the file: rename the function
	os.WriteFile(goFile, []byte("package main\n\nfunc NewFunc() {}\n"), 0644)
	idx.IndexFile(ctx, goFile)

	symsOld, _ := idx.FindSymbol(ctx, "OldFunc")
	symsNew, _ := idx.FindSymbol(ctx, "NewFunc")
	if len(symsOld) > 0 {
		t.Error("OldFunc should be gone after re-index")
	}
	if len(symsNew) == 0 {
		t.Error("NewFunc should be found after re-index")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func newTestRetriever(t *testing.T) *Retriever {
	t.Helper()
	dir := t.TempDir()
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	idx, err := NewCodeIndex(filepath.Join(dir, "test.db"), ts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return NewRetriever(idx, ts)
}

// suppress unused import warning
var _ = tool.ToolSchema{}
