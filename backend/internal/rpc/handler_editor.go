package rpc

import (
	"context"
	"encoding/json"

	"github.com/weisyn/wescode/internal/codeintel"
	appengine "github.com/weisyn/wescode/internal/engine"
)

// indexPaths maps codeintel.IndexPath over a slice, preserving nil.
func indexPaths(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, p := range in {
		out[i] = codeintel.IndexPath(p)
	}
	return out
}

func (h *Handler) handleEditorState(ctx context.Context, req Request) (any, *RPCError) {
	var params EditorStateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	// The renderer sends VS Code fsPaths (backslashes on Windows) while
	// codeintel compares these against CKG file_path, which is slash-normalized.
	// Converting once here rather than at each comparison is what keeps the
	// downstream code honest: the consumers are `sym.FilePath == state.FocusFile`
	// (retrieval_enrichment), `fragments[i].Path == focusFile` plus
	// PathProximity's splitSlash (retrieval), and a `strings.Contains(focusFile,
	// "loop/engine.go")` test against user-typed paths — none of which announces
	// that it needs one representation, and all of which just stop matching.
	// The symptom is not an error: retrieval silently loses focus-file
	// proximity, related-symbol dedup and the "is the focus file what the user
	// asked about?" judgement, so the model gets a worse context and nothing
	// says so.
	//
	// Safe for the OS-facing uses too: readLines/readFile take these paths
	// straight to os.Open, which accepts forward slashes on Windows, and the
	// filepath.Dir-derived comparisons stay self-consistent because both sides
	// come from this same struct.
	state := codeintel.EditorState{
		FocusFile:        codeintel.IndexPath(params.FocusFile),
		CursorLine:       params.CursorLine,
		CursorCol:        params.CursorCol,
		OpenFiles:        indexPaths(params.OpenFiles),
		TerminalSnapshot: params.TerminalSnapshot,
		GitStagedFiles:   indexPaths(params.GitStagedFiles),
		VisibleEditors:   indexPaths(params.VisibleEditors),
	}
	if params.Selection != nil {
		state.Selection = &codeintel.Selection{
			StartLine: params.Selection.StartLine,
			StartCol:  params.Selection.StartCol,
			EndLine:   params.Selection.EndLine,
			EndCol:    params.Selection.EndCol,
			Text:      params.Selection.Text,
		}
	}
	if params.VisibleRange != nil {
		state.VisibleRange = &codeintel.LineRange{
			Start: params.VisibleRange.Start,
			End:   params.VisibleRange.End,
		}
	}
	for _, re := range params.RecentEdits {
		state.RecentEdits = append(state.RecentEdits, codeintel.RecentEdit{
			// Shares a dedup map with FocusFile in retrieval.go, so the two have
			// to agree on one spelling or the focus file is injected twice.
			Path:      codeintel.IndexPath(re.Path),
			Timestamp: re.Timestamp,
			StartLine: re.StartLine,
			EndLine:   re.EndLine,
		})
	}
	for _, d := range params.GlobalErrors {
		state.GlobalErrors = append(state.GlobalErrors, codeintel.DiagnosticEntry{
			File:    codeintel.IndexPath(d.Path),
			Line:    d.Line,
			Message: d.Message,
		})
	}
	h.engine.UpdateEditorState(state)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditorDidSave(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path != "" {
		h.engine.RemoveBuffer(params.Path)
		h.engine.NotifyFileChange(params.Path)
		h.engine.OnFileSaved(params.Path)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditorDidChange(ctx context.Context, req Request) (any, *RPCError) {
	var params EditorDidChangeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path != "" && len(params.Content) <= 5<<20 {
		h.engine.SetBuffer(params.Path, params.Content)
	}
	return map[string]bool{"ok": true}, nil
}

// handleEditorFileChanged handles filesystem-level file changes pushed from
// VSCode's FileSystemWatcher (create/modify/delete outside the editor).
func (h *Handler) handleEditorFileChanged(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	for _, p := range params.Paths {
		if p != "" {
			h.engine.NotifyFileChange(p)
		}
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditorDidClose(ctx context.Context, req Request) (any, *RPCError) {
	var params EditorDidCloseParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path != "" {
		h.engine.RemoveBuffer(params.Path)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleIndexFile(ctx context.Context, req Request) (any, *RPCError) {
	var params IndexFileParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path == "" {
		return nil, &RPCError{Code: -32602, Message: "path is required"}
	}
	if err := h.engine.IndexFile(ctx, params.Path); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleCompletionRequest(ctx context.Context, req Request) (any, *RPCError) {
	var params CompletionRequestParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}

	provider := h.engine.DefaultProvider()
	if provider == nil {
		return CompletionResult{}, nil
	}

	idx := h.engine.GetCodeIndex()
	fimCtxBuilder := codeintel.NewFIMContextBuilder(idx)
	fc := fimCtxBuilder.Build(ctx, params.Path, params.Prefix)
	fimPrompt := codeintel.FormatFIMPrompt(params.Prefix, params.Suffix, fc, params.Path)

	L(ctx).Debug("[completion] FIM request",
		"path", params.Path,
		"prefixLen", len(params.Prefix),
		"suffixLen", len(params.Suffix),
		"crossFileCtx", len(fc.CrossFileSignatures),
	)

	completion, err := h.engine.FIMComplete(ctx, provider, fimPrompt)
	if err != nil {
		L(ctx).Warn("[completion] FIM call failed", "error", err)
		return CompletionResult{}, nil
	}

	if completion == "" {
		return CompletionResult{}, nil
	}

	completion = codeintel.TrimToCompleteUnit(completion)

	return CompletionResult{
		Completions: []CompletionItem{{Text: completion}},
	}, nil
}

func (h *Handler) handleContextSources(_ context.Context, _ Request) (any, *RPCError) {
	sources := h.engine.ContextSources()
	result := ContextSourcesResult{Sources: make([]ContextSourceInfoRPC, 0, len(sources))}
	for _, src := range sources {
		info := src.Info()
		result.Sources = append(result.Sources, ContextSourceInfoRPC{
			ID:         info.ID,
			Label:      info.Label,
			Icon:       info.Icon,
			Searchable: info.Searchable,
			Available:  info.Available,
		})
	}
	return result, nil
}

func (h *Handler) handleContextSearch(ctx context.Context, req Request) (any, *RPCError) {
	var params ContextSearchParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	items, err := h.engine.SearchContext(ctx, params.SourceID, params.Query, params.Limit)
	if err != nil {
		return nil, internalError(err)
	}
	result := ContextSearchResult{Items: make([]ContextSearchItemRPC, 0, len(items))}
	for _, item := range items {
		result.Items = append(result.Items, ContextSearchItemRPC{
			ID:       item.ID,
			SourceID: item.SourceID,
			Label:    item.Label,
			Detail:   item.Detail,
			Icon:     item.Icon,
			Data:     item.Data,
		})
	}
	return result, nil
}

func (h *Handler) handleContextResolve(ctx context.Context, req Request) (any, *RPCError) {
	var params ContextResolveParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	reqItems := make([]appengine.ContextResolveReq, 0, len(params.Items))
	for _, item := range params.Items {
		reqItems = append(reqItems, appengine.ContextResolveReq{ID: item.ID, SourceID: item.SourceID})
	}
	resolved, err := h.engine.ResolveContextItems(ctx, reqItems)
	if err != nil {
		return nil, internalError(err)
	}
	result := ContextResolveResult{Resolved: make([]ContextResolvedRPC, 0, len(resolved))}
	for _, r := range resolved {
		rr := ContextResolvedRPC{
			ID:       r.ID,
			Content:  r.Content,
			FilePath: r.FilePath,
			Metadata: r.Metadata,
		}
		if r.CodeSnippet != nil {
			rr.CodeSnippet = &CodeSnippetRefRPC{
				Path:      r.CodeSnippet.Path,
				StartLine: r.CodeSnippet.StartLine,
				EndLine:   r.CodeSnippet.EndLine,
				Language:  r.CodeSnippet.Language,
				Content:   r.CodeSnippet.Content,
			}
		}
		result.Resolved = append(result.Resolved, rr)
	}
	return result, nil
}
