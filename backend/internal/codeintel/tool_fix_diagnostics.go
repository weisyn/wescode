package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewFixDiagnosticsTool creates a tool that lists diagnostics with their available
// LSP code actions, enabling a "diagnostics-first" repair strategy where AI
// applies LSP quick fixes before resorting to manual edits.
func NewFixDiagnosticsTool(cache *DiagnosticsCache, lsp LSPBridge, baseline *DiagnosticsBaseline) tool.Tool {
	return &fixDiagnosticsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionPerceive,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     true,
				Concurrent:   true,
				Timeout:      15 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		cache:    cache,
		lsp:      lsp,
		baseline: baseline,
	}
}

type fixDiagnosticsTool struct {
	tool.BaseTool
	cache    *DiagnosticsCache
	lsp      LSPBridge
	baseline *DiagnosticsBaseline
}

func (t *fixDiagnosticsTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	if !isNoop {
		return true
	}
	return !t.cache.UpdatedAt().IsZero()
}

func (t *fixDiagnosticsTool) Name() string { return "fix_diagnostics" }

func (t *fixDiagnosticsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "fix_diagnostics",
		Description: "Analyze project errors and find LSP quick fixes for them. Returns each error with available code actions (auto-import, remove unused, etc.). Use this BEFORE manually editing to fix errors — many errors have zero-cost LSP fixes that are more precise than AI edits. Errors marked [NEW] were introduced by your changes; pre-existing errors can be ignored.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Optional: limit to a specific file. Without this, analyzes all project errors."
				},
				"only_new": {
					"type": "boolean",
					"description": "Only show errors introduced after the baseline (i.e., your changes). Default: true."
				}
			}
		}`),
	}
}

type diagWithActions struct {
	Diag    IDEDiagnostic
	Actions []CodeActionResult
	IsNew   bool
}

func (t *fixDiagnosticsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这是 actionable 的典型形状：每条诊断带 0..N 个可用修复。
	var actions *ActionableData
	var notReady bool
	defer func() { res = PresentActionable(res, notReady, actions) }()

	var params struct {
		Path    string `json:"path"`
		OnlyNew *bool  `json:"only_new"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if params.Path != "" && tc != nil && projectRoot(tc) != "" && !filepath.IsAbs(params.Path) {
		params.Path = filepath.Join(projectRoot(tc), params.Path)
	}

	onlyNew := true
	if params.OnlyNew != nil {
		onlyNew = *params.OnlyNew
	}

	var diags []IDEDiagnostic
	if params.Path != "" {
		diags = t.cache.ForFile(params.Path)
	} else {
		diags = t.cache.All()
	}

	// Filter to errors only
	var errors []IDEDiagnostic
	for _, d := range diags {
		if d.Severity == 1 {
			errors = append(errors, d)
		}
	}

	if len(errors) == 0 {
		// 空列表而非 nil：落 empty（"没有错误"是这个工具最有价值的答案）。
		actions = ActionableOf(nil, 0)
		return &tool.ToolResult{Content: "No errors found. Project compiles cleanly."}, nil
	}

	// Determine which are new (if baseline available)
	var baseSet map[diagKey]IDEDiagnostic
	if t.baseline != nil && !t.baseline.TakenAt().IsZero() {
		t.baseline.mu.RLock()
		baseSet = buildDiagSet(t.baseline.snapshot)
		t.baseline.mu.RUnlock()
	}

	var results []diagWithActions
	_, isNoop := t.lsp.(NoopLSP)

	for _, d := range errors {
		isNew := baseSet != nil && !isDiagInSet(d, baseSet)
		if onlyNew && baseSet != nil && !isNew {
			continue
		}

		dwa := diagWithActions{Diag: d, IsNew: isNew}

		if !isNoop {
			actions, err := t.lsp.CodeActions(ctx, d.Path, d.Line, d.Column, d.Line, d.Column+1)
			if err == nil {
				dwa.Actions = actions
			}
		}

		results = append(results, dwa)
	}

	if len(results) == 0 {
		if onlyNew {
			// 空列表而非 nil：落 empty（"没有错误"是这个工具最有价值的答案）。
			actions = ActionableOf(nil, 0)
			return &tool.ToolResult{Content: "No new errors (all errors are pre-existing). Your changes compile cleanly."}, nil
		}
		// 空列表而非 nil：落 empty（"没有错误"是这个工具最有价值的答案）。
		actions = ActionableOf(nil, 0)
		return &tool.ToolResult{Content: "No errors found."}, nil
	}

	var sb strings.Builder
	fixableCount := 0
	for _, r := range results {
		for _, a := range r.Actions {
			if a.IsPreferred {
				fixableCount++
				break
			}
		}
	}

	fmt.Fprintf(&sb, "%d error(s) to fix", len(results))
	if fixableCount > 0 {
		fmt.Fprintf(&sb, " (%d have LSP quick fixes — use `apply_code_action` to fix without manual editing)", fixableCount)
	}
	sb.WriteString(":\n\n")

	byFile := make(map[string][]diagWithActions)
	var fileOrder []string
	for _, r := range results {
		if _, exists := byFile[r.Diag.Path]; !exists {
			fileOrder = append(fileOrder, r.Diag.Path)
		}
		byFile[r.Diag.Path] = append(byFile[r.Diag.Path], r)
	}

	for _, path := range fileOrder {
		displayPath := path
		if tc != nil && projectRoot(tc) != "" {
			if rel, err := filepath.Rel(projectRoot(tc), path); err == nil {
				displayPath = rel
			}
		}
		fmt.Fprintf(&sb, "── %s ──\n", displayPath)
		for _, r := range byFile[path] {
			newMarker := ""
			if r.IsNew {
				newMarker = "[NEW] "
			}
			fmt.Fprintf(&sb, "  L%d:%d %s%s\n", r.Diag.Line, r.Diag.Column, newMarker, r.Diag.Message)
			if len(r.Actions) > 0 {
				for _, a := range r.Actions {
					preferred := ""
					if a.IsPreferred {
						preferred = " ★"
					}
					fmt.Fprintf(&sb, "    → fix: \"%s\"%s\n", a.Title, preferred)
				}
			} else {
				sb.WriteString("    → no LSP fix available (manual edit needed)\n")
			}
		}
		sb.WriteByte('\n')
	}

	// 一项 = 一条诊断，动作 = 它的 LSP quick fix。prompt 必须具体到模型不用再猜——
	// 它会被原样提交回对话，然后由 agent 调 apply_code_action。
	//
	// 没有修复的诊断也要列出来（Actions 为空数组）：那句"需要手工改"是答案的一部分，
	// 漏掉它会让用户以为剩下的错误不存在。ActionableOf 会把 nil 归一成 [] ——
	// nil marshal 成 null 会让 wesui 拒收整个载荷，一条诊断带崩整张列表。
	items := make([]ActionableItem, 0, len(results))
	for _, r := range results {
		acts := make([]ActionRef, 0, len(r.Actions))
		for _, a := range r.Actions {
			style := ""
			if a.IsPreferred {
				style = "primary"
			}
			acts = append(acts, ActionRef{
				Label: a.Title,
				Prompt: fmt.Sprintf("Apply code action %q at %s:%d:%d",
					a.Title, r.Diag.Path, r.Diag.Line, r.Diag.Column),
				Style: style,
			})
		}
		kind := severityLabel(r.Diag.Severity)
		if r.IsNew {
			// [NEW] 是这个工具最重要的一个区分：新引入的错误必须先修，
			// 预存在的可以忽略。藏进 detail 会被 message 挤掉。
			kind = "new " + kind
		}
		items = append(items, ActionableItem{
			Label:   r.Diag.Message,
			Detail:  r.Diag.Source,
			File:    r.Diag.Path,
			Line:    r.Diag.Line, // IDEDiagnostic.Line 来自 VS Code marker，已是 1-based
			Kind:    kind,
			Actions: acts,
		})
	}
	actions = ActionableOf(items, 0)

	return &tool.ToolResult{Content: sb.String()}, nil
}
