package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/boundary"

	"github.com/weisyn/wesgine/tool"
)

// NewProjectMapTool creates a "project_map" tool that generates a structural
// overview of the project using the code index.
// Domain-scoped ("graph"): only injected into schema when domain is active,
// reducing baseline schema token cost (INV-DOMAIN-09).
func NewProjectMapTool(index *CodeIndex, workDir string) tool.Tool {
	return &projectMapTool{
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
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index:   index,
		workDir: workDir,
	}
}

type projectMapTool struct {
	tool.BaseTool
	index   *CodeIndex
	workDir string
}

func (t *projectMapTool) Name() string { return "project_map" }

var projectMapContract = tool.PromptContract{
	Summary: "Generate a structural overview of the project: directory tree with key symbols per file. Use to orient yourself in an unfamiliar codebase.",
	WhenToUse: []tool.PromptScenario{
		{Condition: "First encounter with a codebase or module — need to understand its structure"},
		{Condition: "Need a bird's-eye view of what's in a directory before diving deeper"},
		{Condition: "Planning a large change and need to see which files/packages are involved"},
	},
	WhenNotToUse: []tool.PromptRedirect{
		{Condition: "Already know the symbol name and need its location", AlternativeTool: "search_symbols", Reason: "search_symbols is faster for targeted lookup by name"},
		{Condition: "Need file listing without symbols (just directory contents)", AlternativeTool: "glob", Reason: "glob returns file paths without parsing symbols"},
		{Condition: "Need to read one file's full content", AlternativeTool: "read", Reason: "read gives complete file content; project_map gives structural overview only"},
	},
	SideEffects: []tool.SideEffectDecl{
		{Type: "none", Description: "Read-only structural analysis from code index", Reversible: true},
	},
	ParamNotes: []tool.ParamNote{
		{Param: "depth", Constraint: "Max directory depth to traverse", Default: "2"},
		{Param: "focus", Constraint: "Subdirectory relative path to scope the overview"},
		{Param: "verbosity", Constraint: "summary | detail | full", Default: "detail"},
	},
}

func (t *projectMapTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "project_map",
		Description: tool.ContractToDescription(&projectMapContract),
		Contract:    &projectMapContract,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"depth": {"type": "integer", "description": "Max depth (default 2, max 5)"},
				"focus": {"type": "string", "description": "Subdirectory to focus on (relative path)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			}
		}`),
	}
}

func (t *projectMapTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具**故意不产出列表**：输出是目录树，摊平成条目会丢掉
	// 层级，而层级正是"项目长什么样"这个问题的答案。传 nil 得到 category=text + 分级。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Depth     int    `json:"depth"`
		Focus     string `json:"focus"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Depth <= 0 {
		params.Depth = 2
	}
	if params.Depth > 5 {
		params.Depth = 5
	}

	slog.Debug("[project_map] request", "depth", params.Depth, "focus", params.Focus)
	rootDir := t.workDir
	if projectRoot(tc) != "" {
		rootDir = projectRoot(tc)
	}
	if params.Focus != "" {
		focused := filepath.Join(rootDir, filepath.Clean(params.Focus))
		if reason := tc.CheckPathAccess(focused, false); reason != "" {
			return &tool.ToolResult{Content: "project_map: " + reason, IsError: true, ErrorKind: tool.ErrorPermission}, nil
		}
		rootDir = focused
	}

	type dirInfo struct {
		path    string
		symbols []string
		files   int
	}

	fp := tc.Host.Files()
	dirs := map[string]*dirInfo{}
	var collectDir func(dir string, depth int)
	collectDir = func(dir string, depth int) {
		if depth > params.Depth {
			return
		}
		entries, err := fp.ReadDir(ctx, dir)
		if err != nil {
			return
		}

		relDir, _ := filepath.Rel(rootDir, dir)
		if relDir == "" || relDir == "." {
			relDir = "."
		}

		info := &dirInfo{path: relDir}
		dirs[relDir] = info

		for _, e := range entries {
			if e.IsDir() {
				name := e.Name()
				if skip, _ := boundary.NewClassifier(boundary.ModeFast, nil).ShouldSkipDir(name, name); skip {
					continue
				}
				collectDir(filepath.Join(dir, name), depth+1)
			} else {
				info.files++
			}
		}
	}
	collectDir(rootDir, 0)

	if t.index != nil {
		verbosity := ParseVerbosity(params.Verbosity)
		symbolsPerDir := 10
		switch verbosity {
		case VerbositySummary:
			symbolsPerDir = 3
		case VerbosityFull:
			symbolsPerDir = 20
		}

		for relDir, info := range dirs {
			absDir := filepath.Join(rootDir, relDir)
			entries, err := fp.ReadDir(ctx, absDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				filePath := filepath.Join(absDir, e.Name())
				symbols, err := t.index.ListFileSymbols(ctx, filePath)
				if err != nil || len(symbols) == 0 {
					continue
				}
				for _, sym := range symbols {
					switch sym.Kind {
					case "function", "type", "interface", "class", "method",
						"struct", "enum", "constant", "variable", "constructor":
						if sym.Parent == "" {
							info.symbols = append(info.symbols, fmt.Sprintf("%s %s", sym.Kind, sym.Name))
						}
					}
				}
			}
			if len(info.symbols) > symbolsPerDir {
				info.symbols = append(info.symbols[:symbolsPerDir], fmt.Sprintf("... and %d more", len(info.symbols)-symbolsPerDir))
			}
		}
	}

	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)

	var sb strings.Builder
	if params.Focus != "" {
		fmt.Fprintf(&sb, "Project map (focus: %s, depth: %d):\n\n", params.Focus, params.Depth)
	} else {
		fmt.Fprintf(&sb, "Project map (depth: %d):\n\n", params.Depth)
	}

	for _, d := range sortedDirs {
		info := dirs[d]
		indent := strings.Count(d, string(filepath.Separator))
		prefix := strings.Repeat("  ", indent)

		dirName := filepath.Base(d)
		if d == "." {
			dirName = filepath.Base(rootDir)
		}
		fmt.Fprintf(&sb, "%s%s/ (%d files)\n", prefix, dirName, info.files)

		if len(info.symbols) > 0 {
			for _, sym := range info.symbols {
				fmt.Fprintf(&sb, "%s  - %s\n", prefix, sym)
			}
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
