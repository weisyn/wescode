package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewSemanticSearchTool creates a "semantic_search" tool that finds
// functionally related code using the pre-computed SEMANTICALLY_RELATED
// edges in the CKG graph. Unlike text search (grep/FTS5), this finds
// code that does similar things even when naming conventions differ.
func NewSemanticSearchTool(index *CodeIndex) tool.Tool {
	return &semanticSearchTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        10 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "graph",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		index: index,
	}
}

type semanticSearchTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *semanticSearchTool) Name() string { return "semantic_search" }

func (t *semanticSearchTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "semantic_search",
		Description: "Find functionally related code using semantic similarity (Random Indexing vectors + API signature + type signature). Discovers code that does similar things even when names differ completely.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the function/method to find semantically related code for"},
				"limit": {"type": "integer", "description": "Max results (default 10, max 20)"},
				"min_similarity": {"type": "number", "description": "Minimum similarity threshold 0-1 (default 0.7)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *semanticSearchTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Symbol        string  `json:"symbol"`
		Limit         int     `json:"limit"`
		MinSimilarity float64 `json:"min_similarity"`
		Verbosity     string  `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: "symbol name is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	if params.Limit <= 0 {
		params.Limit = 10
	}
	if params.Limit > 20 {
		params.Limit = 20
	}
	if params.MinSimilarity <= 0 {
		params.MinSimilarity = 0.7
	}

	db := t.index.readerDB
	if db == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}

	// Find the source symbol's ID.
	// err 不用 := ：具名返回已占用这个名字（defer 需要它收敛声明）。
	var sourceID int64
	err = db.QueryRowContext(ctx,
		`SELECT id FROM symbols WHERE name = ? AND kind IN ('function','method') LIMIT 1`,
		params.Symbol).Scan(&sourceID)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol %q not found in index", params.Symbol)}, nil
	}

	// Query semantic_related edges outgoing from this symbol.
	rows, err := db.QueryContext(ctx,
		`SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, e.score
		 FROM edges e
		 JOIN symbols s ON (CASE WHEN e.source_id = ? THEN e.target_id ELSE e.source_id END) = s.id
		 WHERE e.kind = 'semantic_related'
		   AND (e.source_id = ? OR e.target_id = ?)
		   AND e.score >= ?
		 ORDER BY e.score DESC
		 LIMIT ?`,
		sourceID, sourceID, sourceID, params.MinSimilarity, params.Limit)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("semantic search failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer rows.Close()

	type result struct {
		entry      SymbolEntry
		similarity float64
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.entry.FilePath, &r.entry.Kind, &r.entry.Name, &r.entry.Signature, &r.entry.Parent, &r.entry.LineStart, &r.entry.LineEnd, &r.similarity); err != nil {
			continue
		}
		results = append(results, r)
	}

	if len(results) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No semantically related symbols found for %q (threshold: %.0f%%)", params.Symbol, params.MinSimilarity*100)}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	resultTotal := len(results)
	if verbosity == VerbositySummary && len(results) > 5 {
		results = results[:5]
	}

	// 相似度进 detail：它是这张表的排序依据，也是"这个结果值不值得看"的判据。
	// LineStart 要 +1（CKG 存 0-based）。
	items := make([]ListItem, 0, len(results))
	for _, r := range results {
		items = append(items, ListItem{
			Label:  r.entry.Name,
			Detail: fmt.Sprintf("%.0f%% related · %s", r.similarity*100, r.entry.Signature),
			File:   r.entry.FilePath,
			Line:   r.entry.LineStart + 1,
			Kind:   r.entry.Kind,
		})
	}
	list = ListOf(items, resultTotal)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %s symbol(s) semantically related to %q:\n\n", CountPhrase(len(results), resultTotal), params.Symbol))
	for i, r := range results {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s %s (%.0f%% similar)\n", i+1, r.entry.Kind, r.entry.Name, r.similarity*100))
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s (%.0f%% similar)\n", i+1, r.entry.Kind, r.entry.Name, r.similarity*100))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", r.entry.FilePath, r.entry.LineStart+1))
			if r.entry.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", r.entry.Signature))
			}
			sb.WriteByte('\n')
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
