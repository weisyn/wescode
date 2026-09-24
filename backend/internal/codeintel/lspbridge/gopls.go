package lspbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// NoopLSP is a fallback that returns empty results when no LSP is available.
type NoopLSP struct{}

func (NoopLSP) Diagnostics(_ context.Context, _ string) ([]Diagnostic, error) {
	return nil, nil
}
func (NoopLSP) Definition(_ context.Context, _ string, _, _ int) ([]DefinitionLocation, error) {
	return nil, nil
}
func (NoopLSP) References(_ context.Context, _ string, _, _ int) ([]DefinitionLocation, error) {
	return nil, nil
}
func (NoopLSP) Hover(_ context.Context, _ string, _, _ int) (string, error) { return "", nil }
func (NoopLSP) Implementation(_ context.Context, _ string, _, _ int) ([]DefinitionLocation, error) {
	return nil, nil
}
func (NoopLSP) DocumentSymbols(_ context.Context, _ string) ([]DocumentSymbol, error) {
	return nil, nil
}
func (NoopLSP) CodeActions(_ context.Context, _ string, _, _, _, _ int) ([]CodeActionResult, error) {
	return nil, nil
}
func (NoopLSP) ApplyCodeAction(_ context.Context, _ string, _, _, _, _ int, _ string) ([]FileEdit, error) {
	return nil, nil
}
func (NoopLSP) Rename(_ context.Context, _ string, _, _ int, _ string) ([]FileEdit, error) {
	return nil, nil
}
func (NoopLSP) OrganizeImports(_ context.Context, _ string) ([]FileEdit, error) {
	return nil, nil
}
func (NoopLSP) PrepareCallHierarchy(_ context.Context, _ string, _, _ int) ([]CallHierarchyItem, error) {
	return nil, nil
}
func (NoopLSP) IncomingCalls(_ context.Context, _ CallHierarchyItem) ([]IncomingCall, error) {
	return nil, nil
}
func (NoopLSP) OutgoingCalls(_ context.Context, _ CallHierarchyItem) ([]OutgoingCall, error) {
	return nil, nil
}

// IDELSPBridge implements LSPBridge by forwarding requests to the IDE's
// language servers via JSON-RPC notifications.
type IDELSPBridge struct {
	requestFn LSPRequestFn
}

// NewIDELSPBridge creates an LSP bridge that forwards requests through the IDE.
func NewIDELSPBridge(fn LSPRequestFn) *IDELSPBridge {
	return &IDELSPBridge{requestFn: fn}
}

func (b *IDELSPBridge) Diagnostics(ctx context.Context, path string) ([]Diagnostic, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
	}
	result, err := b.requestFn(ctx, "textDocument/diagnostic", params)
	if err != nil {
		return nil, fmt.Errorf("lsp diagnostics request: %w", err)
	}
	var resp struct {
		Items []struct {
			Range struct {
				Start struct{ Line, Character int }
			}
			Severity int
			Message  string
			Source   string
		}
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("lsp diagnostics unmarshal: %w", err)
	}
	var diags []Diagnostic
	for _, item := range resp.Items {
		diags = append(diags, Diagnostic{
			Path:     path,
			Line:     item.Range.Start.Line,
			Column:   item.Range.Start.Character,
			Severity: DiagSeverity(item.Severity),
			Message:  item.Message,
			Source:   item.Source,
		})
	}
	return diags, nil
}

func (b *IDELSPBridge) Definition(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error) {
	uri := pathToURI(path)
	slog.Debug("[lsp-bridge] definition request", "uri", uri, "line", line, "col", col)
	params := map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": line, "character": col},
	}
	result, err := b.requestFn(ctx, "textDocument/definition", params)
	if err != nil {
		slog.Warn("[lsp-bridge] definition error", "uri", uri, "line", line, "col", col, "err", err)
		return nil, err
	}
	locs := parseLSPLocations(result)
	slog.Debug("[lsp-bridge] definition result", "uri", uri, "line", line, "col", col, "count", len(locs), "raw_len", len(result))
	return locs, nil
}

func (b *IDELSPBridge) References(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error) {
	uri := pathToURI(path)
	slog.Debug("[lsp-bridge] references request", "uri", uri, "line", line, "col", col)
	params := map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": line, "character": col},
		"context":      map[string]bool{"includeDeclaration": true},
	}
	result, err := b.requestFn(ctx, "textDocument/references", params)
	if err != nil {
		slog.Warn("[lsp-bridge] references error", "uri", uri, "line", line, "col", col, "err", err)
		return nil, err
	}
	locs := parseLSPLocations(result)
	slog.Debug("[lsp-bridge] references result", "uri", uri, "line", line, "col", col, "count", len(locs), "raw_len", len(result))
	return locs, nil
}

func (b *IDELSPBridge) Implementation(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"position":     map[string]int{"line": line, "character": col},
	}
	result, err := b.requestFn(ctx, "textDocument/implementation", params)
	if err != nil {
		return nil, err
	}
	return parseLSPLocations(result), nil
}

func (b *IDELSPBridge) Hover(ctx context.Context, path string, line, col int) (string, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"position":     map[string]int{"line": line, "character": col},
	}
	result, err := b.requestFn(ctx, "textDocument/hover", params)
	if err != nil {
		return "", err
	}
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(result, &hover); err != nil {
		var plain string
		if json.Unmarshal(result, &plain) == nil {
			return plain, nil
		}
		return "", nil
	}
	return hover.Contents.Value, nil
}

func (b *IDELSPBridge) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
	}
	result, err := b.requestFn(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name  string `json:"name"`
		Kind  int    `json:"kind"`
		Range struct {
			Start struct{ Line, Character int }
			End   struct{ Line, Character int }
		} `json:"range"`
		Children []struct {
			Name  string `json:"name"`
			Kind  int    `json:"kind"`
			Range struct {
				Start struct{ Line, Character int }
				End   struct{ Line, Character int }
			} `json:"range"`
		} `json:"children"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, nil
	}
	syms := make([]DocumentSymbol, len(raw))
	for i, r := range raw {
		syms[i] = DocumentSymbol{
			Name: r.Name, Kind: r.Kind,
			StartLine: r.Range.Start.Line, StartColumn: r.Range.Start.Character,
			EndLine: r.Range.End.Line, EndColumn: r.Range.End.Character,
		}
		for _, c := range r.Children {
			syms[i].Children = append(syms[i].Children, DocumentSymbol{
				Name: c.Name, Kind: c.Kind,
				StartLine: c.Range.Start.Line, StartColumn: c.Range.Start.Character,
				EndLine: c.Range.End.Line, EndColumn: c.Range.End.Character,
			})
		}
	}
	return syms, nil
}

func (b *IDELSPBridge) CodeActions(ctx context.Context, path string, startLine, startCol, endLine, endCol int) ([]CodeActionResult, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"range": map[string]any{
			"start": map[string]int{"line": startLine, "character": startCol},
			"end":   map[string]int{"line": endLine, "character": endCol},
		},
		"context": map[string]any{
			"diagnostics": []any{},
		},
	}
	result, err := b.requestFn(ctx, "textDocument/codeAction", params)
	if err != nil {
		return nil, fmt.Errorf("lsp codeAction request: %w", err)
	}
	var raw []struct {
		Title       string `json:"title"`
		Kind        string `json:"kind"`
		IsPreferred bool   `json:"isPreferred"`
		Edit        *struct {
			Changes map[string]any `json:"changes"`
		} `json:"edit"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, nil
	}
	actions := make([]CodeActionResult, len(raw))
	for i, r := range raw {
		actions[i] = CodeActionResult{
			Title:       r.Title,
			Kind:        r.Kind,
			IsPreferred: r.IsPreferred,
			HasEdit:     r.Edit != nil,
		}
	}
	return actions, nil
}

func (b *IDELSPBridge) ApplyCodeAction(ctx context.Context, path string, startLine, startCol, endLine, endCol int, actionTitle string) ([]FileEdit, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"range": map[string]any{
			"start": map[string]int{"line": startLine, "character": startCol},
			"end":   map[string]int{"line": endLine, "character": endCol},
		},
		"context": map[string]any{
			"diagnostics": []any{},
		},
		"actionTitle": actionTitle,
	}
	result, err := b.requestFn(ctx, "textDocument/applyCodeAction", params)
	if err != nil {
		return nil, fmt.Errorf("lsp applyCodeAction request: %w", err)
	}
	return parseWorkspaceEdit(result), nil
}

func (b *IDELSPBridge) PrepareCallHierarchy(ctx context.Context, path string, line, col int) ([]CallHierarchyItem, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"position":     map[string]int{"line": line, "character": col},
	}
	result, err := b.requestFn(ctx, "textDocument/prepareCallHierarchy", params)
	if err != nil {
		return nil, fmt.Errorf("lsp prepareCallHierarchy: %w", err)
	}
	var items []struct {
		Name  string `json:"name"`
		Kind  int    `json:"kind"`
		URI   string `json:"uri"`
		Range struct {
			Start struct{ Line, Character int }
		} `json:"range"`
	}
	if err := json.Unmarshal(result, &items); err != nil {
		return nil, nil
	}
	out := make([]CallHierarchyItem, len(items))
	for i, it := range items {
		out[i] = CallHierarchyItem{
			Name:   it.Name,
			Kind:   it.Kind,
			Path:   uriToPath(it.URI),
			Line:   it.Range.Start.Line,
			Column: it.Range.Start.Character,
		}
	}
	return out, nil
}

func (b *IDELSPBridge) IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]IncomingCall, error) {
	params := map[string]any{
		"item": map[string]any{
			"name": item.Name,
			"kind": item.Kind,
			"uri":  pathToURI(item.Path),
			"range": map[string]any{
				"start": map[string]int{"line": item.Line, "character": item.Column},
				"end":   map[string]int{"line": item.Line, "character": item.Column + len(item.Name)},
			},
		},
	}
	result, err := b.requestFn(ctx, "callHierarchy/incomingCalls", params)
	if err != nil {
		return nil, fmt.Errorf("lsp incomingCalls: %w", err)
	}
	var raw []struct {
		From struct {
			Name  string `json:"name"`
			Kind  int    `json:"kind"`
			URI   string `json:"uri"`
			Range struct {
				Start struct{ Line, Character int }
			} `json:"range"`
		} `json:"from"`
		FromRanges []struct {
			Start struct{ Line, Character int }
			End   struct{ Line, Character int }
		} `json:"fromRanges"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, nil
	}
	out := make([]IncomingCall, len(raw))
	for i, r := range raw {
		out[i] = IncomingCall{
			From: CallHierarchyItem{
				Name:   r.From.Name,
				Kind:   r.From.Kind,
				Path:   uriToPath(r.From.URI),
				Line:   r.From.Range.Start.Line,
				Column: r.From.Range.Start.Character,
			},
		}
		for _, fr := range r.FromRanges {
			out[i].FromRanges = append(out[i].FromRanges, LSPRange{
				StartLine: fr.Start.Line, StartCol: fr.Start.Character,
				EndLine: fr.End.Line, EndCol: fr.End.Character,
			})
		}
	}
	return out, nil
}

func (b *IDELSPBridge) OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]OutgoingCall, error) {
	params := map[string]any{
		"item": map[string]any{
			"name": item.Name,
			"kind": item.Kind,
			"uri":  pathToURI(item.Path),
			"range": map[string]any{
				"start": map[string]int{"line": item.Line, "character": item.Column},
				"end":   map[string]int{"line": item.Line, "character": item.Column + len(item.Name)},
			},
		},
	}
	result, err := b.requestFn(ctx, "callHierarchy/outgoingCalls", params)
	if err != nil {
		return nil, fmt.Errorf("lsp outgoingCalls: %w", err)
	}
	var raw []struct {
		To struct {
			Name  string `json:"name"`
			Kind  int    `json:"kind"`
			URI   string `json:"uri"`
			Range struct {
				Start struct{ Line, Character int }
			} `json:"range"`
		} `json:"to"`
		FromRanges []struct {
			Start struct{ Line, Character int }
			End   struct{ Line, Character int }
		} `json:"fromRanges"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, nil
	}
	out := make([]OutgoingCall, len(raw))
	for i, r := range raw {
		out[i] = OutgoingCall{
			To: CallHierarchyItem{
				Name:   r.To.Name,
				Kind:   r.To.Kind,
				Path:   uriToPath(r.To.URI),
				Line:   r.To.Range.Start.Line,
				Column: r.To.Range.Start.Character,
			},
		}
		for _, fr := range r.FromRanges {
			out[i].Ranges = append(out[i].Ranges, LSPRange{
				StartLine: fr.Start.Line, StartCol: fr.Start.Character,
				EndLine: fr.End.Line, EndCol: fr.End.Character,
			})
		}
	}
	return out, nil
}

func (b *IDELSPBridge) Rename(ctx context.Context, path string, line, col int, newName string) ([]FileEdit, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
		"position":     map[string]int{"line": line, "character": col},
		"newName":      newName,
	}
	result, err := b.requestFn(ctx, "textDocument/rename", params)
	if err != nil {
		return nil, fmt.Errorf("lsp rename request: %w", err)
	}
	return parseWorkspaceEdit(result), nil
}

func (b *IDELSPBridge) OrganizeImports(ctx context.Context, path string) ([]FileEdit, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(path)},
	}
	result, err := b.requestFn(ctx, "textDocument/organizeImports", params)
	if err != nil {
		return nil, fmt.Errorf("lsp organizeImports request: %w", err)
	}
	return parseWorkspaceEdit(result), nil
}

// --- helpers ---

func parseWorkspaceEdit(data json.RawMessage) []FileEdit {
	if len(data) == 0 {
		return nil
	}
	var edit struct {
		Changes map[string][]struct {
			Range struct {
				Start struct{ Line, Character int }
				End   struct{ Line, Character int }
			} `json:"range"`
			NewText string `json:"newText"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(data, &edit); err != nil {
		return nil
	}
	var fileEdits []FileEdit
	for uri, edits := range edit.Changes {
		fe := FileEdit{Path: uriToPath(uri)}
		for _, e := range edits {
			fe.Edits = append(fe.Edits, TextEdit{
				StartLine: e.Range.Start.Line,
				StartCol:  e.Range.Start.Character,
				EndLine:   e.Range.End.Line,
				EndCol:    e.Range.End.Character,
				NewText:   e.NewText,
			})
		}
		fileEdits = append(fileEdits, fe)
	}
	return fileEdits
}

func pathToURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	return "file://" + path
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func parseLSPLocations(data json.RawMessage) []DefinitionLocation {
	if len(data) == 0 {
		return nil
	}
	type lspRange struct {
		Start struct{ Line, Character int }
		End   struct{ Line, Character int }
	}
	type lspLocation struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	}

	var locs []lspLocation
	if err := json.Unmarshal(data, &locs); err == nil && len(locs) > 0 {
		var result []DefinitionLocation
		for _, l := range locs {
			result = append(result, DefinitionLocation{
				Path:      uriToPath(l.URI),
				Line:      l.Range.Start.Line,
				Column:    l.Range.Start.Character,
				EndLine:   l.Range.End.Line,
				EndColumn: l.Range.End.Character,
			})
		}
		return result
	}

	var loc lspLocation
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []DefinitionLocation{{
			Path:      uriToPath(loc.URI),
			Line:      loc.Range.Start.Line,
			Column:    loc.Range.Start.Character,
			EndLine:   loc.Range.End.Line,
			EndColumn: loc.Range.End.Character,
		}}
	}

	return nil
}
