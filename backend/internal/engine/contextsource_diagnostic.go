package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel"
)

// diagnosticContextSource provides LSP diagnostics from the DiagnosticsCache.
type diagnosticContextSource struct {
	getDiagCache func() *codeintel.DiagnosticsCache
}

func (s *diagnosticContextSource) ID() string { return "diagnostic" }

func (s *diagnosticContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "diagnostic", Label: "诊断问题", Icon: "warning",
		Searchable: true, Available: s.getDiagCache() != nil,
	}
}

func (s *diagnosticContextSource) Search(_ context.Context, query string, limit int) ([]ContextSearchItem, error) {
	dc := s.getDiagCache()
	if dc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))

	diags := dc.ErrorsAndWarnings()
	if len(diags) == 0 {
		return nil, nil
	}

	var results []ContextSearchItem
	for _, d := range diags {
		if len(results) >= limit {
			break
		}
		label := fmt.Sprintf("%s:%d", d.Path, d.Line)
		detail := d.Message
		if query != "" && query != "*" &&
			!strings.Contains(strings.ToLower(label), query) &&
			!strings.Contains(strings.ToLower(detail), query) {
			continue
		}
		icon := "warning"
		if d.Severity == 1 {
			icon = "error"
		}
		results = append(results, ContextSearchItem{
			ID:       fmt.Sprintf("diagnostic:%s:%d", d.Path, d.Line),
			SourceID: "diagnostic",
			Label:    label,
			Detail:   detail,
			Icon:     icon,
			Data: map[string]any{
				"severity": d.Severity,
				"source":   d.Source,
				"code":     d.Code,
				"path":     d.Path,
				"line":     d.Line,
			},
		})
	}
	return results, nil
}

func (s *diagnosticContextSource) Resolve(_ context.Context, itemID string) (*ContextResolved, error) {
	dc := s.getDiagCache()
	if dc == nil {
		return &ContextResolved{ID: itemID}, nil
	}

	parts := strings.SplitN(strings.TrimPrefix(itemID, "diagnostic:"), ":", 2)
	if len(parts) < 2 {
		return &ContextResolved{ID: itemID}, nil
	}
	filePath := parts[0]
	var targetLine int
	fmt.Sscanf(parts[1], "%d", &targetLine)

	fileDiags := dc.ForFile(filePath)
	var sb strings.Builder
	for _, d := range fileDiags {
		sev := "warning"
		if d.Severity == 1 {
			sev = "error"
		}
		fmt.Fprintf(&sb, "[%s] %s:%d:%d: %s", sev, d.Path, d.Line, d.Column, d.Message)
		if d.Source != "" {
			fmt.Fprintf(&sb, " (%s)", d.Source)
		}
		sb.WriteString("\n")
	}

	contextCode, lang := readSourceLines(filePath, targetLine-5, targetLine+5)

	content := sb.String()
	if contextCode != "" {
		startLine := targetLine - 5
		if startLine < 1 {
			startLine = 1
		}
		content += fmt.Sprintf("\nContext (%s lines %d-%d):\n```%s\n%s\n```\n",
			filePath, startLine, targetLine+5, lang, contextCode)
	}
	return &ContextResolved{
		ID:      itemID,
		Content: content,
	}, nil
}
