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

// AttentionFile represents a file with attention metadata (from the engine layer).
type AttentionFile struct {
	Path       string
	OpenCount  int
	TotalDwell time.Duration
	LastActive time.Time
	WasEdited  bool
	CoVisited  []string
	Score      float64
	Category   string
}

// AttentionProvider is the interface for querying user attention data.
type AttentionProvider interface {
	TopAttentionFiles(n int) []AttentionFile
	CoVisitedFiles(path string) []string
}

func NewGetAttentionContextTool(provider AttentionProvider) tool.Tool {
	return &getAttentionContextTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionPerceive,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     true,
				Concurrent:   true,
				Timeout:      5 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		provider: provider,
	}
}

type getAttentionContextTool struct {
	tool.BaseTool
	provider AttentionProvider
}

func (t *getAttentionContextTool) IsBackendConfigured() bool {
	return t.provider != nil
}

func (t *getAttentionContextTool) Name() string { return "get_attention_context" }

func (t *getAttentionContextTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "get_attention_context",
		Description: "See what the user is currently focused on — which files they're editing, referencing, or switching between. This reveals the user's working context without them needing to explain it. Files marked 'editing' are active modification targets; 'reference' files are being read; co-visited files are frequently switched together (likely related).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"count": {
					"type": "integer",
					"description": "Number of top files to return. Default: 10."
				}
			}
		}`),
	}
}

func (t *getAttentionContextTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Count *int `json:"count"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	n := 10
	if params.Count != nil && *params.Count > 0 {
		n = *params.Count
	}

	files := t.provider.TopAttentionFiles(n)
	if len(files) == 0 {
		// 这是 not_ready 不是 empty：用户还没切过文件 ≠ 他没有关注点。
		// 报 empty 会让模型以为"这人没在看任何东西"，于是丢掉一个本该有的上下文。
		notReady = true
		return &tool.ToolResult{Content: "No file attention data available yet (user hasn't switched files recently)."}, nil
	}

	// 停留时长与打开次数进 detail：它们是"关注度"的度量，而这个工具的用途正是让
	// 模型知道用户在看哪里。category 进 kind 徽标（editing / reference / glanced
	// 是三种不同的关注）。文件粒度无行号。
	attnItems := make([]ListItem, 0, len(files))
	for _, f := range files {
		attnItems = append(attnItems, ListItem{
			Label: filepath.Base(f.Path),
			Detail: fmt.Sprintf("dwell %s · %d opens · %s ago",
				f.TotalDwell.Truncate(time.Second), f.OpenCount,
				time.Since(f.LastActive).Truncate(time.Second)),
			File: f.Path,
			Kind: f.Category,
		})
	}
	list = ListOf(attnItems, 0)

	var sb strings.Builder
	fmt.Fprintf(&sb, "User's current focus (%d file(s) in last 5 minutes):\n\n", len(files))

	for i, f := range files {
		displayPath := f.Path
		if tc != nil && projectRoot(tc) != "" {
			if rel, err := filepath.Rel(projectRoot(tc), f.Path); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = rel
			}
		}

		icon := "👁"
		switch f.Category {
		case "editing":
			icon = "✏️"
		case "reference":
			icon = "📖"
		case "glanced":
			icon = "👀"
		}

		ago := time.Since(f.LastActive).Truncate(time.Second)
		dwell := f.TotalDwell.Truncate(time.Second)
		fmt.Fprintf(&sb, "%d. %s %s [%s] (dwell: %s, opens: %d, %s ago)\n",
			i+1, icon, displayPath, f.Category, dwell, f.OpenCount, ago)

		if len(f.CoVisited) > 0 {
			coDisplayPaths := make([]string, len(f.CoVisited))
			for j, cp := range f.CoVisited {
				coDisplayPaths[j] = cp
				if tc != nil && projectRoot(tc) != "" {
					if rel, err := filepath.Rel(projectRoot(tc), cp); err == nil && !strings.HasPrefix(rel, "..") {
						coDisplayPaths[j] = rel
					}
				}
			}
			fmt.Fprintf(&sb, "   ↔ frequently switched with: %s\n", strings.Join(coDisplayPaths, ", "))
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
