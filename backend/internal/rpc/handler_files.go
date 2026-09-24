package rpc

import (
	"context"
	"encoding/json"
	"errors"

	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/failure"
)

func (h *Handler) handleListKBFiles(ctx context.Context, _ Request) (any, *RPCError) {
	files, err := h.engine.DocSyncListFiles(ctx)
	if err != nil {
		L(ctx).Warn("[rpc] listKBFiles error", "error", err)
		return nil, internalError(err)
	}
	L(ctx).Warn("[rpc] listKBFiles result", "count", len(files), "nil", files == nil)
	if files == nil {
		return []any{}, nil
	}
	type fileDTO struct {
		ID          string            `json:"id"`
		SourceID    string            `json:"source_id"`
		Path        string            `json:"path"`
		Name        string            `json:"name"`
		Ext         string            `json:"ext"`
		Size        int64             `json:"size"`
		ContentHash string            `json:"content_hash"`
		ModTime     string            `json:"mod_time"`
		Status      string            `json:"status"`
		Category    string            `json:"category"`
		MimeType    string            `json:"mime_type"`
		ChunkCount  int               `json:"chunk_count"`
		ParseMethod string            `json:"parse_method"`
		ErrorMsg    string            `json:"error_message"`
		CreatedAt   string            `json:"created_at"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	}
	out := make([]fileDTO, len(files))
	for i, f := range files {
		out[i] = fileDTO{
			ID:          f.ID,
			SourceID:    f.SourceID,
			Path:        f.Path,
			Name:        f.Name,
			Ext:         f.Ext,
			Size:        f.Size,
			ContentHash: f.ContentHash,
			ModTime:     f.ModTime.Format("2006-01-02T15:04:05Z07:00"),
			Status:      string(f.Status),
			Category:    string(f.Category),
			MimeType:    f.MimeType,
			ChunkCount:  f.ChunkCount,
			ParseMethod: f.ParseMethod,
			ErrorMsg:    f.ErrorMsg,
			CreatedAt:   f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Metadata:    f.Metadata,
		}
	}
	return out, nil
}

func (h *Handler) handleKBRescan(ctx context.Context, _ Request) (any, *RPCError) {
	ingested, orphaned, err := h.engine.DocSyncRescan(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"ingested": ingested, "orphaned": orphaned}, nil
}

func (h *Handler) handleKBSearch(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	results, err := h.engine.DocSyncSearch(ctx, params.Query)
	if err != nil {
		return nil, internalError(err)
	}
	if results == nil {
		return []any{}, nil
	}
	return results, nil
}

func (h *Handler) handleKBStats(ctx context.Context, _ Request) (any, *RPCError) {
	stats, err := h.engine.DocSyncStats(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if stats == nil {
		return map[string]any{}, nil
	}
	return map[string]any{
		"total_files":       stats.TotalFiles,
		"total_chunks":      stats.TotalChunks,
		"total_size":        0,
		"ready_files":       stats.ReadyFiles,
		"error_files":       stats.ErrorFiles,
		"indexing_files":    stats.StaleFiles,
		"quarantined_files": stats.QuarantinedFiles,
	}, nil
}

func (h *Handler) handleKBReindexQuarantined(ctx context.Context, _ Request) (any, *RPCError) {
	count, err := h.engine.DocSyncReindexQuarantined(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]int{"count": count}, nil
}

func (h *Handler) handleKBClear(ctx context.Context, _ Request) (any, *RPCError) {
	if err := h.engine.DocSyncClear(ctx); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleKBReindexFile(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		FileID string `json:"fileId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.FileID == "" {
		return nil, &RPCError{Code: -32602, Message: "fileId is required"}
	}
	if err := h.engine.DocSyncReindexFile(ctx, params.FileID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleKBDeleteFile(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		FileID string `json:"fileId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.FileID == "" {
		return nil, &RPCError{Code: -32602, Message: "fileId is required"}
	}
	if err := h.engine.DocSyncDeleteFile(ctx, params.FileID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleKBReindexErrors(ctx context.Context, _ Request) (any, *RPCError) {
	count, err := h.engine.DocSyncReindexErrors(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]int{"count": count}, nil
}

// ── Chat attachments (files/*) ───────────────────────────────────────────────
//
// Params and results come from protocol.go. Do not re-declare either shape
// here with an inline struct or a map literal: a second declaration of the
// same wire keys is how `fileId` became `id` on the response and `id` became
// `fileId` on the request, in opposite directions, in one commit.

// fileStoreError names the boot-window state so the renderer can branch on it
// and show its own sentence. Everything else stays an internal error.
func fileStoreError(err error) *RPCError {
	if errors.Is(err, appengine.ErrFileStoreNotReady) {
		return errFailure(failure.ReasonEngineNotReady)
	}
	return internalError(err)
}

func (h *Handler) handleFileUpload(_ context.Context, req Request) (any, *RPCError) {
	var params FileUploadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.FileName == "" || params.Data == "" {
		return nil, &RPCError{Code: -32602, Message: "fileName and data are required"}
	}
	m, err := h.engine.UploadFile(params.FileName, params.Data)
	if err != nil {
		return nil, fileStoreError(err)
	}
	return fileResultOf(m), nil
}

func (h *Handler) handleFileImportLocal(_ context.Context, req Request) (any, *RPCError) {
	var params FileImportLocalParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path == "" {
		return nil, &RPCError{Code: -32602, Message: "path is required"}
	}
	m, err := h.engine.ImportLocalFile(params.Path)
	if err != nil {
		return nil, fileStoreError(err)
	}
	return fileResultOf(m), nil
}

func (h *Handler) handleFileContent(_ context.Context, req Request) (any, *RPCError) {
	var params FileContentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.FileID == "" {
		return nil, &RPCError{Code: -32602, Message: "fileId is required"}
	}
	data, fileName, mimeType, err := h.engine.GetFileContent(params.FileID)
	if err != nil {
		return nil, fileStoreError(err)
	}
	return FileContentResult{Data: data, FileName: fileName, MIMEType: mimeType}, nil
}
