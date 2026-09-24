package codeintel

import "strings"

// ContextNeed captures both the task classification and the additional
// retrieval dimensions needed for optimal context assembly.
// CE-INV-08: when all EditorState enrichment fields are zero-value,
// output is equivalent to ClassifyTask(text).
type ContextNeed struct {
	TaskType      TaskType
	NeedsCallers  bool
	NeedsCallees  bool
	NeedsTests    bool
	NeedsSiblings bool
	NeedsHistory  bool
	Scope         string // "function" | "module" | "project" — used in logging/snapshot, not retrieval
}

// ClassifyTaskV2 performs multi-signal intent classification using both the
// user message text and the current editor state. All rules are deterministic
// (zero LLM calls). Falls back to ClassifyTask behavior when EditorState
// enrichment fields are empty.
func ClassifyTaskV2(text string, state EditorState) ContextNeed {
	need := ContextNeed{
		TaskType: ClassifyTask(text),
		Scope:    "function",
	}

	applyDiagnosticSignal(&need, state)
	applyTerminalSignal(&need, state)
	applyGitSignal(&need, state)
	applyMessageStructure(&need, text)
	applySelectionSemantics(&need, state)

	return need
}

func applyDiagnosticSignal(need *ContextNeed, state EditorState) {
	if len(state.GlobalErrors) == 0 {
		return
	}
	if need.TaskType == TaskGeneral {
		need.TaskType = TaskFixBug
	}
	need.NeedsCallers = true
}

func applyTerminalSignal(need *ContextNeed, state EditorState) {
	if state.TerminalSnapshot == "" {
		return
	}
	if hasBuildError(state.TerminalSnapshot) {
		if need.TaskType == TaskGeneral {
			need.TaskType = TaskFixBug
		}
	}
	if hasTestFailure(state.TerminalSnapshot) {
		need.NeedsTests = true
		if need.TaskType == TaskGeneral {
			need.TaskType = TaskFixBug
		}
	}
}

func applyGitSignal(need *ContextNeed, state EditorState) {
	if len(state.GitStagedFiles) > 3 && need.TaskType == TaskGeneral {
		need.TaskType = TaskReview
		need.Scope = "module"
	}
}

func applyMessageStructure(need *ContextNeed, text string) {
	if text == "" {
		return
	}
	if containsArchitectureWords(text) {
		need.Scope = "project"
		need.NeedsSiblings = true
	}
	if containsPerformanceWords(text) {
		need.NeedsHistory = true
	}
	if containsAsyncWords(text) {
		need.NeedsCallers = true
		need.NeedsCallees = true
		need.NeedsSiblings = true
		if need.Scope == "function" {
			need.Scope = "module"
		}
	}
}

func applySelectionSemantics(need *ContextNeed, state EditorState) {
	if state.Selection == nil || state.Selection.Text == "" {
		return
	}
	text := state.Selection.Text
	if looksLikeInterfaceDef(text) {
		need.NeedsCallers = true
		need.NeedsSiblings = true
	}
	if looksLikeStructDef(text) {
		need.NeedsCallees = true
	}
	if looksLikeTestFunc(text) {
		need.NeedsTests = false
	}
}

// ── Pattern matchers (deterministic, zero LLM) ──────────────────────────────

func hasBuildError(s string) bool {
	patterns := []string{
		"cannot ", "undefined:", "syntax error",
		"error TS", "error[E",
		"FAILED", "BUILD FAIL",
		"fatal error", "compilation failed",
		"not found", "does not exist",
	}
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func hasTestFailure(s string) bool {
	patterns := []string{
		"FAIL\t", "--- FAIL:", "FAILED",
		"AssertionError", "assert.",
		"✗", "✘", "FAILURES",
		"test failed", "tests failed",
	}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func containsArchitectureWords(s string) bool {
	words := []string{
		"架构", "设计", "扩展性", "模式", "分层", "解耦",
		"architecture", "design pattern", "scalab", "decouple", "layer",
		"微服务", "monolith", "microservice",
	}
	lower := strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func containsPerformanceWords(s string) bool {
	words := []string{
		"性能", "优化", "慢", "延迟", "瓶颈",
		"performance", "optimize", "slow", "latency", "bottleneck",
		"profil", "benchmark",
	}
	lower := strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func containsAsyncWords(s string) bool {
	words := []string{
		"异步", "并发", "goroutine", "channel", "async", "await",
		"concurrent", "parallel", "promise", "callback",
	}
	lower := strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func looksLikeInterfaceDef(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, "interface")
}

func looksLikeStructDef(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, "struct")
}

func looksLikeTestFunc(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "func Test") || strings.HasPrefix(trimmed, "func Benchmark")
}
