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

// NewGetDiagnosticsTool creates a "get_diagnostics" tool that reads IDE diagnostics
// (from tsserver, ESLint, gopls, etc.) via the cached push from the Extension.
// Falls back to LSP pull when cache is empty and LSPBridge is available.
// When baseline is non-nil, output includes `is_new` field distinguishing
// AI-introduced errors from pre-existing project debt.
func NewGetDiagnosticsTool(cache *DiagnosticsCache, lsp LSPBridge, baseline *DiagnosticsBaseline) tool.Tool {
	return &getDiagnosticsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionPerceive,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     true,
				Concurrent:   true,
				Timeout:      10 * time.Second,
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

type getDiagnosticsTool struct {
	tool.BaseTool
	cache    *DiagnosticsCache
	lsp      LSPBridge
	baseline *DiagnosticsBaseline
}

// IsBackendConfigured implements tool.BackendProbe.
// Hidden when both cache is empty (no IDE push) and LSP is NoopLSP (no pull).
// In IDE mode, cache gets populated within seconds of startup.
func (t *getDiagnosticsTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	if !isNoop {
		return true
	}
	return !t.cache.UpdatedAt().IsZero()
}

func (t *getDiagnosticsTool) Name() string { return "get_diagnostics" }

func (t *getDiagnosticsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "get_diagnostics",
		Description: "Get IDE diagnostics (errors, warnings) from language servers (TypeScript, ESLint, gopls, etc.). Shows problems the IDE has detected. Without arguments, returns all errors and warnings across the project. With a path, returns diagnostics for that specific file.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path to get diagnostics for. Omit to get all project diagnostics."
				},
				"severity": {
					"type": "string",
					"enum": ["error", "warning", "all"],
					"description": "Filter by severity. Default: error+warning."
				}
			}
		}`),
	}
}

func (t *getDiagnosticsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具的 not_ready 尤其承重：IDE 还没上报过 ≠ 项目没有错误，
	// 而用户会据此认为可以提交。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Path     string `json:"path"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if params.Path != "" && tc != nil && projectRoot(tc) != "" && !filepath.IsAbs(params.Path) {
		params.Path = filepath.Join(projectRoot(tc), params.Path)
	}

	maxSev := 2 // default: errors + warnings
	switch params.Severity {
	case "error":
		maxSev = 1
	case "all":
		maxSev = 8
	case "warning", "":
		maxSev = 2
	}

	var diags []IDEDiagnostic
	if params.Path != "" {
		diags = t.cache.ForFile(params.Path)
		if len(diags) == 0 {
			diags = t.pullFromLSP(ctx, params.Path)
		}
	} else {
		diags = t.cache.All()
	}

	var filtered []IDEDiagnostic
	for _, d := range diags {
		if d.Severity <= maxSev {
			filtered = append(filtered, d)
		}
	}

	if len(filtered) == 0 {
		if params.Path != "" {
			list = &ListData{Items: []ListItem{}}
			return &tool.ToolResult{Content: fmt.Sprintf("No diagnostics for %s", params.Path)}, nil
		}
		updatedAt := t.cache.UpdatedAt()
		if updatedAt.IsZero() {
			// 这里与下面那条的区别是整个 PC-02：IDE 还没上报过 ≠ 项目没有错误。
			// 两句话措辞早就不同，但都是 IsError=false 的纯文本，前端分不出来——
			// 于是"还不知道"被读成"干净"，而用户据此认为可以提交。
			notReady = true
			return &tool.ToolResult{Content: "No IDE diagnostics available (IDE may not have reported yet)."}, nil
		}
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No errors or warnings in the project (last updated %s ago).", time.Since(updatedAt).Truncate(time.Second))}, nil
	}

	list = ListOf(diagnosticListItems(filtered), 0)
	return &tool.ToolResult{Content: formatDiagnosticsWithBaseline(filtered, tc, t.baseline)}, nil
}

// diagnosticListItems 把诊断投影成列表项。
//
// label 用消息而不是文件名：诊断列表里用户扫的是"哪里错了"，而文件名会在位置列重复。
// 严重级进 kind 徽标——它决定用户先处理哪一条，藏在 detail 里会被 message 挤掉。
//
// Line 直接用：IDEDiagnostic.Line 来自 VS Code marker，已是 1-based（与 CKG 的
// LineStart 相反），所以这里**不能** +1。判据在 diagnostics_cache.go 的上游推送路径。
func diagnosticListItems(diags []IDEDiagnostic) []ListItem {
	items := make([]ListItem, 0, len(diags))
	for _, d := range diags {
		detail := d.Source
		if d.Code != "" {
			detail += " " + d.Code
		}
		items = append(items, ListItem{
			Label:  d.Message,
			Detail: detail,
			File:   d.Path,
			Line:   d.Line,
			Kind:   severityLabel(d.Severity),
		})
	}
	return items
}

// severityLabel 把 VS Code MarkerSeverity 映成徽标文字。
// 未知值落 "diagnostic" 而不是数字——数字对用户没有意义，而猜一个级别会让他
// 按错误的优先级处理。
func severityLabel(sev int) string {
	switch sev {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 4:
		return "info"
	case 8:
		return "hint"
	default:
		return "diagnostic"
	}
}

func (t *getDiagnosticsTool) pullFromLSP(ctx context.Context, path string) []IDEDiagnostic {
	if t.lsp == nil {
		return nil
	}
	if _, ok := t.lsp.(NoopLSP); ok {
		return nil
	}
	lspDiags, err := t.lsp.Diagnostics(ctx, path)
	if err != nil || len(lspDiags) == 0 {
		return nil
	}
	out := make([]IDEDiagnostic, len(lspDiags))
	for i, d := range lspDiags {
		out[i] = IDEDiagnostic{
			Path:     d.Path,
			Line:     d.Line,
			Column:   d.Column,
			Severity: int(d.Severity),
			Message:  d.Message,
			Source:   d.Source,
		}
	}
	return out
}

func formatDiagnosticsWithBaseline(diags []IDEDiagnostic, tc *tool.ToolContext, baseline *DiagnosticsBaseline) string {
	var sb strings.Builder

	// Build a set of baseline diagnostics for fast lookup
	var baseSet map[diagKey]IDEDiagnostic
	if baseline != nil && !baseline.TakenAt().IsZero() {
		baseline.mu.RLock()
		baseSet = buildDiagSet(baseline.snapshot)
		baseline.mu.RUnlock()
	}

	byFile := make(map[string][]IDEDiagnostic)
	var fileOrder []string
	for _, d := range diags {
		if _, exists := byFile[d.Path]; !exists {
			fileOrder = append(fileOrder, d.Path)
		}
		byFile[d.Path] = append(byFile[d.Path], d)
	}

	errorCount, warnCount, newErrorCount, newWarnCount := 0, 0, 0, 0
	for _, d := range diags {
		isNew := baseSet != nil && !isDiagInSet(d, baseSet)
		if d.Severity == 1 {
			errorCount++
			if isNew {
				newErrorCount++
			}
		} else {
			warnCount++
			if isNew {
				newWarnCount++
			}
		}
	}

	if baseSet != nil {
		fmt.Fprintf(&sb, "%d error(s), %d warning(s) in %d file(s) [NEW: %d error(s), %d warning(s)]:\n\n",
			errorCount, warnCount, len(fileOrder), newErrorCount, newWarnCount)
	} else {
		fmt.Fprintf(&sb, "%d error(s), %d warning(s) in %d file(s):\n\n", errorCount, warnCount, len(fileOrder))
	}

	for _, path := range fileOrder {
		displayPath := path
		if tc != nil && projectRoot(tc) != "" {
			if rel, err := filepath.Rel(projectRoot(tc), path); err == nil {
				displayPath = rel
			}
		}
		fmt.Fprintf(&sb, "── %s ──\n", displayPath)
		for _, d := range byFile[path] {
			sev := "error"
			if d.Severity == 2 {
				sev = "warning"
			} else if d.Severity > 2 {
				sev = "info"
			}
			src := ""
			if d.Source != "" {
				src = fmt.Sprintf("[%s] ", d.Source)
			}
			code := ""
			if d.Code != "" {
				code = fmt.Sprintf("(%s) ", d.Code)
			}
			newMarker := ""
			if baseSet != nil && !isDiagInSet(d, baseSet) {
				newMarker = "[NEW] "
			}
			fmt.Fprintf(&sb, "  L%d:%d %s: %s%s%s%s\n", d.Line, d.Column, sev, newMarker, src, code, d.Message)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func isDiagInSet(d IDEDiagnostic, set map[diagKey]IDEDiagnostic) bool {
	key := diagKey{
		Path:     d.Path,
		Line:     d.Line,
		Severity: d.Severity,
		Message:  d.Message,
	}
	_, exists := set[key]
	return exists
}
