// Model-facing constraint tools.
//
// There is deliberately no bulk lister here. A constraint reaches the model in
// exactly two shapes: the CSEOverlay pull (focus file ∩ Checker, ≤500 tokens)
// and a FAIL advisory carrying the checker's verdict. A tool that dumped the
// whole registry with its Rule prose would be rule injection through a side
// door — pull instead of push, but the same bytes in the same context window
// (INV-CSE-16). Humans browse and curate the full registry, Rule text included,
// through the IDE panel over the `constraint/list` RPC.
package codeintel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wesgine/tool"
)

// NewCheckConstraintsTool creates a "check_constraints" tool that runs every
// constraint matching a file against that file's current contents.
func NewCheckConstraintsTool(reg *constraints.Registry) tool.Tool {
	return &checkConstraintsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        5 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "constraint",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		reg: reg,
	}
}

type checkConstraintsTool struct {
	tool.BaseTool
	reg *constraints.Registry
}

func (t *checkConstraintsTool) Name() string { return "check_constraints" }

func (t *checkConstraintsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "check_constraints",
		Description: "Run the project constraints that apply to a file against its current contents. Reports only violations.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file_path": {"type": "string", "description": "Path of the file to check (absolute, or relative to the workspace root)"}
			},
			"required": ["file_path"]
		}`),
	}
}

// Call reads the file and evaluates each matching constraint's checker.
//
// There is no "description of the change" parameter any more. The pre-refactor
// version took prose and keyword-matched it against constraint text, which meant
// the answer depended on how the model phrased its intent rather than on what
// the code says — and every low-priority constraint was reported as a possible
// violation regardless of the description (INV-CSE-15). A checker reads the file.
func (t *checkConstraintsTool) Call(_ context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.FilePath == "" {
		return &tool.ToolResult{Content: "file_path is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.reg == nil {
		notReady = true
		return &tool.ToolResult{Content: "constraint registry not available"}, nil
	}

	absPath := resolvePath(params.FilePath, tc)
	matching := t.reg.MatchingFile(absPath)
	if len(matching) == 0 {
		return &tool.ToolResult{Content: "No constraints apply to this file."}, nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return &tool.ToolResult{
			Content:   fmt.Sprintf("cannot read %s: %v", absPath, err),
			IsError:   true,
			ErrorKind: tool.ErrorInvocation,
		}, nil
	}

	// The FAIL line carries the checker's mechanical verdict and nothing else —
	// same shape as the PostCall advisory. Appending c.Rule here would put rule
	// prose back into the model's context through a side door (INV-CSE-16).
	var violations []string
	var items []ListItem
	for _, c := range matching {
		failed, msg := constraints.CheckFile(c, absPath, string(content))
		if failed {
			violations = append(violations, fmt.Sprintf("[FAIL %s] %s", c.ID, msg))
			// detail 只放 checker 的机械判词，与 Content 里那一行同源——不放 c.Rule。
			// 上面那条 INV-CSE-16 说的侧门不是这里（已核实：进模型上下文的只有
			// exec.Result.Content，StructuredData 只进 EventStructuredOutput 给前端，
			// 见 wesgine internal/loop/loop_execute.go），但即便如此也不新增信息面：
			// 规则正文该由记忆中心/约束页展示，不该由一次检查结果顺带泄出来。
			items = append(items, ListItem{
				Label:  c.ID,
				Detail: msg,
				File:   absPath,
				Kind:   "violation",
			})
		}
	}

	if len(violations) == 0 {
		// 空列表而非 nil：全过是这个工具最有价值的答案（可以提交了）。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{
			Content: fmt.Sprintf("%d constraint(s) checked against %s — all pass.", len(matching), params.FilePath),
		}, nil
	}
	list = ListOf(items, 0)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d of %d constraint(s) fail for %s:\n\n", len(violations), len(matching), params.FilePath)
	for _, v := range violations {
		sb.WriteString(v + "\n")
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// NewSuggestConstraintsTool creates a "suggest_constraints" tool that runs the
// inference passes and registers what they find as candidates.
func NewSuggestConstraintsTool(index *CodeIndex, reg *constraints.Registry) tool.Tool {
	return &suggestConstraintsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        15 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "constraint",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index: index,
		reg:   reg,
	}
}

type suggestConstraintsTool struct {
	tool.BaseTool
	index *CodeIndex
	reg   *constraints.Registry
}

func (t *suggestConstraintsTool) Name() string { return "suggest_constraints" }

func (t *suggestConstraintsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "suggest_constraints",
		Description: "Suggest new project constraints by running the inference passes (dependency cycles, signature stability, concurrency, security, performance, state machines).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"category": {"type": "string", "enum": ["dependency", "compatibility", "concurrency", "security", "performance", "state", "all"], "description": "Category of constraints to suggest (default: all)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail"], "description": "Output detail level (default: summary)"}
			}
		}`),
	}
}

// inferencePass names an inference entry point so the tool can select one or
// run them all without a switch per category.
type inferencePass struct {
	category string
	label    string
	run      func(*sql.DB, *constraints.Registry, string) (int, error)
}

// inferencePasses is the full set of Checker-backed inference passes. Every
// entry must bind a checker; a pass that produced prose-only constraints would
// be rejected by Registry.Add anyway (INV-CSE-15).
var inferencePasses = []inferencePass{
	{"dependency", "Dependency cycles", constraints.InferFromCKG},
	{"compatibility", "Signature stability (high fan-in)", constraints.InferTypeConstraints},
	{"compatibility", "Signature stability (exported API)", constraints.InferCompatibilityConstraints},
	{"concurrency", "Goroutine captures without sync", constraints.InferConcurrencyConstraints},
	{"security", "SQL concat / hardcoded secrets", constraints.InferSecurityConstraints},
	{"performance", "N+1 query patterns", constraints.InferPerformanceConstraints},
	{"state", "Exhaustive enum switches", constraints.InferStateMachineConstraints},
}

func (t *suggestConstraintsTool) Call(_ context.Context, input json.RawMessage, _ *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Category  string `json:"category"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Category == "" {
		params.Category = "all"
	}

	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index not ready — cannot suggest constraints."}, nil
	}
	if t.reg == nil {
		notReady = true
		return &tool.ToolResult{Content: "constraint registry not available"}, nil
	}
	db := t.index.DB()
	if db == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index not available"}, nil
	}
	workDir := t.index.workDir

	var suggestions []string
	total := 0
	for _, pass := range inferencePasses {
		if params.Category != "all" && params.Category != pass.category {
			continue
		}
		n, err := pass.run(db, t.reg, workDir)
		if err != nil || n == 0 {
			continue
		}
		total += n
		suggestions = append(suggestions, fmt.Sprintf("• %s: %d", pass.label, n))
	}

	if total == 0 {
		return &tool.ToolResult{Content: "No new constraint suggestions found. The inference passes did not detect patterns beyond what is already registered."}, nil
	}

	var sb strings.Builder
	// Not "as candidates": AddInferred auto-promotes on evidence strength, so a
	// strong finding lands Active. The model has no say either way (INV-CSE-11).
	fmt.Fprintf(&sb, "Registered %d new constraint(s) for review:\n\n", total)
	for _, s := range suggestions {
		sb.WriteString(s + "\n")
	}
	if params.Verbosity == "detail" {
		sb.WriteString("\nCandidates are reviewed by the user in the IDE constraint panel.")
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
