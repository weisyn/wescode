package verification

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/message"
	"github.com/weisyn/wesgine/tool"
)

// ── L1: 真实 go build 测试 ──────────────────────────────────────────────────

func TestRunGoBuild_ValidCode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	result := RunGoBuild(context.Background(), dir, &tool.LocalShellProvider{})
	if !result.Success {
		t.Errorf("expected success, got errors: %s", result.RawOutput)
	}
}

func TestRunGoBuild_InvalidCode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tx := undefinedVar\n\t_ = x\n}\n"), 0644)

	result := RunGoBuild(context.Background(), dir, &tool.LocalShellProvider{})
	if result.Success {
		t.Fatal("expected build failure for undefined variable")
	}
	if len(result.Errors) == 0 {
		t.Error("expected parsed errors")
	}

	found := false
	for _, e := range result.Errors {
		if e.Line == 4 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on line 4, got: %+v", result.Errors)
	}
}

// ── L2: 真实 go test 测试 ───────────────────────────────────────────────────

func TestRunTests_Passing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package testmod\n\nfunc Add(a, b int) int { return a + b }\n"), 0644)
	os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(`package testmod

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("1+2 should be 3")
	}
}
`), 0644)

	result := RunTests(context.Background(), dir, []string{"./..."}, "", 30*time.Second, &tool.LocalShellProvider{})
	if !result.AllPass {
		t.Errorf("expected all tests to pass: %s", result.RawOutput)
	}
}

func TestRunTests_Failing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package testmod\n\nfunc Add(a, b int) int { return a - b }\n"), 0644)
	os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(`package testmod

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Error("1+2 should be 3")
	}
}
`), 0644)

	result := RunTests(context.Background(), dir, []string{"./..."}, "", 30*time.Second, &tool.LocalShellProvider{})
	if result.AllPass {
		t.Fatal("expected test failure (Add returns a-b)")
	}
}

// ── 编译错误解析 ────────────────────────────────────────────────────────────

func TestParseGoErrors_MultipleErrors(t *testing.T) {
	output := "./main.go:4:2: undefined: foo\n./handler.go:12:15: cannot use x (type int) as type string\n"
	errors := parseGoErrors(output, "/project")
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errors))
	}
	if errors[0].Line != 4 {
		t.Errorf("first error line: want 4, got %d", errors[0].Line)
	}
	if errors[1].Line != 12 {
		t.Errorf("second error line: want 12, got %d", errors[1].Line)
	}
}

// ── QualityGate 集成（真实 go build subprocess）────────────────────────────

func TestCodeQualityGate_PassesValidCode(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.go")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(mainFile, []byte("package main\n\nfunc main() {}\n"), 0644)

	cfg := DefaultConfig()
	cfg.L2Test = "never"
	gate := NewCodeQualityGate(cfg, dir, nil)
	gate.SetModifiedFilesFn(func() []string { return []string{mainFile} })

	turnResult := makeTurnResult(mainFile)
	verdict, err := gate.Evaluate(context.Background(), engine.SessionState{}, turnResult, wesgine.QualityEvaluateContext{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !verdict.Pass {
		t.Errorf("expected Pass for valid code, feedback: %s", textFromMessage(verdict.Feedback))
	}
}

func TestCodeQualityGate_FailsInvalidCode(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.go")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	os.WriteFile(mainFile, []byte("package main\n\nfunc main() {\n\tx := undefinedVar\n\t_ = x\n}\n"), 0644)

	cfg := DefaultConfig()
	cfg.L2Test = "never"
	gate := NewCodeQualityGate(cfg, dir, nil)
	gate.SetModifiedFilesFn(func() []string { return []string{mainFile} })

	turnResult := makeTurnResult(mainFile)
	verdict, err := gate.Evaluate(context.Background(), engine.SessionState{}, turnResult, wesgine.QualityEvaluateContext{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Pass {
		t.Fatal("expected Fail for code with undefined variable")
	}
	if verdict.Criteria != "compilation" {
		t.Errorf("criteria: want 'compilation', got %q", verdict.Criteria)
	}
	if verdict.MaxRevisions != 3 {
		t.Errorf("MaxRevisions: want 3, got %d", verdict.MaxRevisions)
	}
	text := textFromMessage(verdict.Feedback)
	if text == "" {
		t.Error("expected non-empty feedback message")
	}
}

func TestCodeQualityGate_NoModifiedFiles_Passes(t *testing.T) {
	gate := NewCodeQualityGate(DefaultConfig(), t.TempDir(), nil)

	turnResult := engine.TurnResult{
		Message: message.Message{Role: message.RoleAssistant,
			Content: []message.ContentBlock{{Type: "text", Text: "Done"}}},
	}
	verdict, err := gate.Evaluate(context.Background(), engine.SessionState{}, turnResult, wesgine.QualityEvaluateContext{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !verdict.Pass {
		t.Error("expected Pass when no files modified (pure text turn)")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func makeTurnResult(editedFile string) engine.TurnResult {
	input, _ := json.Marshal(map[string]string{"path": editedFile})
	return engine.TurnResult{
		Message: message.Message{
			Role: message.RoleAssistant,
			Content: []message.ContentBlock{
				{
					Type:  "tool_use",
					ID:    "call_1",
					Name:  "edit",
					Input: input,
				},
			},
		},
	}
}

func textFromMessage(m message.Message) string {
	for _, b := range m.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}
