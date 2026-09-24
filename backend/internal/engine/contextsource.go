package engine

import "context"

// ContextSourceInfo describes a context source for the frontend picker.
type ContextSourceInfo struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Icon       string `json:"icon"`
	Searchable bool   `json:"searchable"`
	Available  bool   `json:"available"`
}

// ContextSearchItem is a search result from a context source.
type ContextSearchItem struct {
	ID       string         `json:"id"`
	SourceID string         `json:"sourceId"`
	Label    string         `json:"label"`
	Detail   string         `json:"detail,omitempty"`
	Icon     string         `json:"icon,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// ContextResolved is the resolved content of a context item.
type ContextResolved struct {
	ID          string            `json:"id"`
	Content     string            `json:"content,omitempty"`
	FilePath    string            `json:"filePath,omitempty"`
	CodeSnippet *CodeSnippetRef   `json:"codeSnippet,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CodeSnippetRef describes a code region within a file.
type CodeSnippetRef struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Language  string `json:"language"`
	Content   string `json:"content"`
}

// ContextResolveReq is a request to resolve a single context item.
type ContextResolveReq struct {
	ID       string
	SourceID string
}

// ContextItemResolved carries resolved context for injection into a chat Run.
type ContextItemResolved struct {
	ID       string
	SourceID string
	// Label is the human chip label from the picker (INV-CTX-REF-01).
	Label string
	// Detail is the secondary human line (dir / line range / file count).
	Detail      string
	FilePath    string
	CodeSnippet *CodeSnippet
	Content     string
}

// ContextSource is the interface for context data providers.
type ContextSource interface {
	ID() string
	Info() ContextSourceInfo
	Search(ctx context.Context, query string, limit int) ([]ContextSearchItem, error)
	Resolve(ctx context.Context, itemID string) (*ContextResolved, error)
}
