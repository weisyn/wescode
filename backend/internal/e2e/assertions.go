//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	wesengine "github.com/weisyn/wesgine/engine"
)

// AssertToolTriggered verifies that the named tool was invoked.
func AssertToolTriggered(t *testing.T, result *ChatResult, toolName string) {
	t.Helper()
	if !result.HasTool(toolName) {
		t.Errorf("expected tool %q to be triggered, got tools: %v", toolName, result.ToolStarts)
	}
}

// AssertNoErrors verifies no error events occurred.
func AssertNoErrors(t *testing.T, result *ChatResult) {
	t.Helper()
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

// AssertTextContains checks the AI's text output contains a substring.
func AssertTextContains(t *testing.T, result *ChatResult, sub string) {
	t.Helper()
	if !strings.Contains(result.Text, sub) {
		t.Errorf("expected text to contain %q, got %d chars of text", sub, len(result.Text))
	}
}

// AssertToolResultContains checks tool result content for a substring.
func AssertToolResultContains(t *testing.T, result *ChatResult, sub string) {
	t.Helper()
	content := result.ToolResultContent()
	if !strings.Contains(content, sub) {
		t.Errorf("expected tool result to contain %q, got: %s", sub, truncate(content, 200))
	}
}

// AssertEventExists checks that at least one event of the given type exists.
func AssertEventExists(t *testing.T, result *ChatResult, eventType wesengine.EventType) {
	t.Helper()
	if !result.HasEvent(eventType) {
		types := make([]string, 0, len(result.AllEvents))
		for _, ev := range result.AllEvents {
			types = append(types, string(ev.Type))
		}
		t.Errorf("expected event %q, got types: %v", eventType, unique(types))
	}
}

// AssertPlanCreated verifies a plan was created during the chat.
func AssertPlanCreated(t *testing.T, result *ChatResult) {
	t.Helper()
	if !result.PlanCreated {
		t.Error("expected plan_created event but none received")
	}
}

// AssertFileExists checks that a file was created in the workspace.
func AssertFileExists(t *testing.T, workDir, relPath string) {
	t.Helper()
	path := relPath
	if !strings.HasPrefix(relPath, "/") {
		path = workDir + "/" + relPath
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %q to exist: %v", relPath, err)
	}
}

// AssertFileContains checks file content for a substring.
func AssertFileContains(t *testing.T, workDir, relPath, sub string) {
	t.Helper()
	path := relPath
	if !strings.HasPrefix(relPath, "/") {
		path = workDir + "/" + relPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("cannot read %q: %v", relPath, err)
		return
	}
	if !strings.Contains(string(data), sub) {
		t.Errorf("file %q does not contain %q", relPath, sub)
	}
}

// AssertCompiles builds the workspace and fails with the compiler's own output
// if it does not build.
//
// This replaces a scan of error-event text for the word "compilation". That
// scan could not work: no engine ErrorCode or message carries that word, so the
// branch was unreachable and the test passed unconditionally — while the
// assertion guarding it (`ev.Data.(string)`) would have panicked on the first
// EventError, because EventError carries ErrorData. A check that can only
// panic or no-op is worse than no check, since it reads like coverage.
//
// Compilation is a property of the files on disk, so the honest test compiles
// them rather than inferring it from what the engine said.
func AssertCompiles(t *testing.T, workDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("workspace does not compile: %v\n%s", err, out)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func unique(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
