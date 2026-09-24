package lspbridge

import (
	"context"
	"encoding/json"
)

// DiagSeverity indicates the severity level of a diagnostic.
type DiagSeverity int

const (
	DiagError   DiagSeverity = 1
	DiagWarning DiagSeverity = 2
	DiagInfo    DiagSeverity = 3
	DiagHint    DiagSeverity = 4
)

// Diagnostic represents a language server diagnostic (error/warning).
type Diagnostic struct {
	Path     string
	Line     int
	Column   int
	Severity DiagSeverity
	Message  string
	Source   string
}

// DefinitionLocation is a goto-definition result.
type DefinitionLocation struct {
	Path      string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// CodeActionResult represents a single code action offered by the language server.
type CodeActionResult struct {
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	IsPreferred bool   `json:"isPreferred"`
	HasEdit     bool   `json:"hasEdit"`
}

// TextEdit represents a single text replacement within a file.
type TextEdit struct {
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	NewText   string `json:"newText"`
}

// FileEdit represents edits to a single file as part of a workspace edit.
type FileEdit struct {
	Path  string     `json:"path"`
	Edits []TextEdit `json:"edits"`
}

// DocumentSymbol represents a symbol in a document (LSP DocumentSymbol).
type DocumentSymbol struct {
	Name        string
	Kind        int
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Children    []DocumentSymbol
}

// CallHierarchyItem represents a callable symbol in the call hierarchy.
type CallHierarchyItem struct {
	Name   string `json:"name"`
	Kind   int    `json:"kind"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// IncomingCall represents a caller of a symbol.
type IncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []LSPRange        `json:"fromRanges"`
}

// OutgoingCall represents a callee from a symbol.
type OutgoingCall struct {
	To     CallHierarchyItem `json:"to"`
	Ranges []LSPRange        `json:"fromRanges"`
}

// LSPRange is a generic line/col range.
type LSPRange struct {
	StartLine int `json:"startLine"`
	StartCol  int `json:"startCol"`
	EndLine   int `json:"endLine"`
	EndCol    int `json:"endCol"`
}

// LSPBridge provides access to language server capabilities.
// Implementations connect to running language servers via their protocol.
type LSPBridge interface {
	Diagnostics(ctx context.Context, path string) ([]Diagnostic, error)
	Definition(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error)
	References(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error)
	Hover(ctx context.Context, path string, line, col int) (string, error)
	Implementation(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error)
	DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error)
	CodeActions(ctx context.Context, path string, startLine, startCol, endLine, endCol int) ([]CodeActionResult, error)
	ApplyCodeAction(ctx context.Context, path string, startLine, startCol, endLine, endCol int, actionTitle string) ([]FileEdit, error)
	Rename(ctx context.Context, path string, line, col int, newName string) ([]FileEdit, error)
	OrganizeImports(ctx context.Context, path string) ([]FileEdit, error)
	PrepareCallHierarchy(ctx context.Context, path string, line, col int) ([]CallHierarchyItem, error)
	IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]IncomingCall, error)
	OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]OutgoingCall, error)
}

// LSPRequestFn is the function signature for sending LSP requests to the IDE.
type LSPRequestFn func(ctx context.Context, method string, params any) (json.RawMessage, error)
