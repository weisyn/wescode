package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewReadSymbolsTool creates a "read_symbols" tool that batch-reads specific
// symbols from multiple files using CKG positional data. This reduces tool
// call round-trips when the LLM needs to cross-reference code across files
// (common after file_card degradation triggers re-reads).
func NewReadSymbolsTool(index *CodeIndex, fp tool.FileProvider) tool.Tool {
	return &readSymbolsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionPerceive,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     true,
				Concurrent:   true,
				Timeout:      15 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyOther,
			},
		},
		index: index,
		fp:    fp,
	}
}

type readSymbolsTool struct {
	tool.BaseTool
	index *CodeIndex
	fp    tool.FileProvider
}

func (t *readSymbolsTool) Name() string { return "read_symbols" }

func (t *readSymbolsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "read_symbols",
		Description: "Batch-read specific symbols (functions, types, variables) from multiple files. More efficient than multiple read() calls when you need to cross-reference code across files. Uses the code knowledge graph to locate symbol positions precisely.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"queries": {
					"type": "array",
					"description": "List of symbol queries. Each query specifies a file path, symbol name, and desired detail level.",
					"items": {
						"type": "object",
						"properties": {
							"path": {
								"type": "string",
								"description": "Absolute file path"
							},
							"symbol": {
								"type": "string",
								"description": "Symbol name (function, type, variable, method)"
							},
							"depth": {
								"type": "string",
								"enum": ["signature", "body", "full"],
								"description": "Detail level: 'signature' = declaration only, 'body' = declaration + implementation, 'full' = includes doc comments"
							}
						},
						"required": ["path", "symbol"]
					}
				},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail). Controls max queries processed: summary=5, detail=15, full=20."}
			},
			"required": ["queries"]
		}`),
	}
}

type readSymbolsArgs struct {
	Queries   []symbolQuery `json:"queries"`
	Verbosity string        `json:"verbosity"`
}

type symbolQuery struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Depth  string `json:"depth"`
}

func (t *readSymbolsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具**故意不产出列表**：它的输出是逐符号的源码块拼接，
	// 摊平成条目会丢掉代码本身，而代码正是用户要读的东西。传 nil 得到 category=text
	// + 分级——"一个符号都没解析出来"与"解析到了"仍然是两件事。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args readSymbolsArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if len(args.Queries) == 0 {
		return &tool.ToolResult{Content: "queries array is empty", IsError: true}, nil
	}
	if len(args.Queries) > 20 {
		return &tool.ToolResult{Content: "too many queries (max 20)", IsError: true}, nil
	}

	verbosity := ParseVerbosity(args.Verbosity)
	// 这里截断的不是查询结果，是**调用方点名要的查询列表**。summary 档上限 5，
	// 于是问 8 个符号只答 5 个——而此前没有任何一句话说剩下 3 个没答。
	// 数字错至少还答了问题；这个是答了别的问题然后装作答完了。
	queries, askedFor := TruncateWithTotal(args.Queries, verbosity)

	var sb strings.Builder
	if askedFor > len(queries) {
		sb.WriteString(fmt.Sprintf("Answering %s requested symbol(s) (verbosity=%s limits the batch; raise it or split the call):\n\n",
			CountPhrase(len(queries), askedFor), verbosity))
	}
	for i, q := range queries {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		result := t.resolveSymbol(ctx, q)
		sb.WriteString(result)
	}

	return &tool.ToolResult{
		Content:   sb.String(),
		PinResult: true,
	}, nil
}

func (t *readSymbolsTool) resolveSymbol(ctx context.Context, q symbolQuery) string {
	depth := q.Depth
	if depth == "" {
		depth = "body"
	}

	if t.index == nil {
		return t.fallbackReadSymbol(ctx, q, depth)
	}

	symbols, err := t.index.SearchSymbols(ctx, q.Symbol, 5)
	if err != nil {
		return t.fallbackReadSymbol(ctx, q, depth)
	}
	var match *SymbolEntry
	for i := range symbols {
		if symbols[i].FilePath == q.Path && strings.EqualFold(symbols[i].Name, q.Symbol) {
			match = &symbols[i]
			break
		}
	}
	if match == nil {
		for i := range symbols {
			if strings.HasSuffix(symbols[i].FilePath, q.Path) && strings.EqualFold(symbols[i].Name, q.Symbol) {
				match = &symbols[i]
				break
			}
		}
	}

	if match == nil {
		return t.fallbackReadSymbol(ctx, q, depth)
	}

	content, err := t.readLines(ctx, match.FilePath, match.LineStart, match.LineEnd, depth)
	if err != nil {
		return fmt.Sprintf("[read_symbols] %s:%s — error: %s", q.Path, q.Symbol, err)
	}

	header := fmt.Sprintf("[read_symbols] %s :: %s (%s) L%d-L%d\n",
		match.FilePath, match.Name, match.Kind, match.LineStart, match.LineEnd)
	return header + content
}

func (t *readSymbolsTool) fallbackReadSymbol(ctx context.Context, q symbolQuery, depth string) string {
	data, err := t.readFileContent(ctx, q.Path)
	if err != nil {
		return fmt.Sprintf("[read_symbols] %s:%s — file not found: %s", q.Path, q.Symbol, err)
	}

	lines := strings.Split(string(data), "\n")
	matchStart := -1
	for i, line := range lines {
		if strings.Contains(line, q.Symbol) {
			matchStart = i
			break
		}
	}

	if matchStart < 0 {
		return fmt.Sprintf("[read_symbols] %s:%s — symbol not found in file", q.Path, q.Symbol)
	}

	start := matchStart
	end := matchStart + 1

	if depth == "signature" {
		end = matchStart + 1
	} else {
		braceCount := 0
		foundOpen := false
		for i := matchStart; i < len(lines) && i < matchStart+500; i++ {
			for _, ch := range lines[i] {
				if ch == '{' {
					braceCount++
					foundOpen = true
				} else if ch == '}' {
					braceCount--
				}
			}
			end = i + 1
			if foundOpen && braceCount <= 0 {
				break
			}
		}
	}

	if depth == "full" && start > 0 {
		for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
			start--
		}
	}

	result := strings.Join(lines[start:end], "\n")
	header := fmt.Sprintf("[read_symbols] %s :: %s (fallback) L%d-L%d\n",
		q.Path, q.Symbol, start+1, end)
	return header + result
}

func (t *readSymbolsTool) readFileContent(ctx context.Context, path string) ([]byte, error) {
	if t.fp != nil {
		return t.fp.ReadFile(ctx, path)
	}
	return os.ReadFile(path)
}

func (t *readSymbolsTool) readLines(ctx context.Context, path string, startLine, endLine int, depth string) (string, error) {
	data, err := t.readFileContent(ctx, path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	start := startLine - 1
	end := endLine
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}

	if depth == "signature" && end > start+3 {
		end = start + 3
	}

	if depth == "full" && start > 0 {
		for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
			start--
		}
	}

	return strings.Join(lines[start:end], "\n"), nil
}
