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

// NewCoChangeTool creates a "find_co_changed_files" tool that finds files
// frequently modified together (based on co_changes_with edges from the
// CKG pipeline).
func NewCoChangeTool(index *CodeIndex) tool.Tool {
	return &coChangeTool{
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

type coChangeTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *coChangeTool) Name() string { return "find_co_changed_files" }

func (t *coChangeTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_co_changed_files",
		Description: "Find files that are frequently modified together with the given file, based on git co-change history. Coupling strength is the Jaccard coefficient over commits touching each file.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {"type": "string", "description": "File path to find co-changed partners for"},
				"limit": {"type": "integer", "description": "Max results (default 10)"}
			},
			"required": ["file"]
		}`),
	}
}

type coChangeResult struct {
	File     string
	Coupling float64 // Jaccard coefficient, read from edges.score
	Source   string
}

func (t *coChangeTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		File  string `json:"file"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.File == "" {
		return &tool.ToolResult{Content: "file path is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil || t.index.readerDB == nil {
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

	db := t.index.readerDB

	// co_changes_with edges link file-level symbols; query both directions.
	//
	// Both the projection and the ordering are MAX(e.score), not a bare column:
	// under GROUP BY, a bare `e.score` lets SQLite return an arbitrary row from
	// the group, so the reported coupling and the sort key could come from
	// different edges. The predecessor did exactly that (bare `e.certainty` in
	// both places), which ordered the list by an arbitrary member of each group
	// — invisible while every group had one row, wrong the moment a file pair
	// gains a second symbol-level edge.
	rows, err := db.QueryContext(ctx, `
		SELECT
			CASE WHEN s1.file_path = ? THEN s2.file_path ELSE s1.file_path END AS partner,
			MAX(e.score) AS coupling,
			e.source
		FROM edges e
		JOIN symbols s1 ON e.source_id = s1.id
		JOIN symbols s2 ON e.target_id = s2.id
		WHERE e.kind = 'co_changes_with'
		  AND (s1.file_path = ? OR s2.file_path = ?)
		GROUP BY partner
		ORDER BY coupling DESC
		LIMIT ?
	`, IndexPath(params.File), IndexPath(params.File), IndexPath(params.File), params.Limit)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer rows.Close()

	var results []coChangeResult
	for rows.Next() {
		var r coChangeResult
		if err := rows.Scan(&r.File, &r.Coupling, &r.Source); err != nil {
			continue
		}
		if r.File == params.File {
			continue
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(results) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No co-change partners found for %q", params.File)}, nil
	}

	// 耦合度进 detail：它是这张表唯一的排序与判断依据（80% 耦合意味着"改这个大概率
	// 也要改那个"，20% 只是噪声）。文件没有行号——共变是文件粒度的事实，编一个行号
	// 会让点击跳到文件头并看起来像"就是这一行耦合"。
	items := make([]ListItem, 0, len(results))
	for _, r := range results {
		items = append(items, ListItem{
			Label:  filepath.Base(r.File),
			Detail: fmt.Sprintf("%.0f%% coupling", r.Coupling*100),
			File:   r.File,
			Kind:   "co-change",
		})
	}
	list = ListOf(items, 0)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files frequently changed with %q (%d result(s)):\n\n", params.File, len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s  (coupling: %.0f%%)\n", i+1, r.File, r.Coupling*100))
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
