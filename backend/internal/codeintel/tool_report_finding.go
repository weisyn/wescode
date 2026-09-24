package codeintel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewReportFindingTool creates a "report_finding" tool for structured,
// evidence-backed code analysis findings. Part of the Grounded Claims
// Architecture (design/grounded-claims.md): findings must reference
// actual tool calls via evidence_refs, which are mechanically verified
// by the engine.
//
// This is a Tier C tool — wescode-specific, registered to the Cell.
func NewReportFindingTool() tool.Tool {
	return &reportFindingTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionCognize,
				Availability: tool.AvailabilityAlwaysOn,
				ReadOnly:     false,
				Concurrent:   false,
				Timeout:      5 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyCognize,
				Enabled:      true,
			},
		},
	}
}

type reportFindingTool struct {
	tool.BaseTool
}

func (t *reportFindingTool) Name() string { return "report_finding" }

func (t *reportFindingTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "report_finding",
		Description: "Record a structured, evidence-backed finding from code analysis. Each finding must reference at least one tool call (grep/read/glob/exec) from this Run as evidence. The engine mechanically verifies that all referenced tool_call_ids actually exist. Use this instead of free-text conclusions to produce verifiable analysis reports.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"severity": {
					"type": "string",
					"enum": ["P0", "P1", "P2", "info"],
					"description": "Severity: P0=critical, P1=important, P2=minor, info=observation"
				},
				"category": {
					"type": "string",
					"description": "Finding category (e.g. missing-error-handling, security, performance, style)"
				},
				"claim": {
					"type": "string",
					"description": "One-sentence description of the finding"
				},
				"location": {
					"type": "string",
					"description": "File path and optional line range (e.g. cmd/main.go:42-58)"
				},
				"evidence_refs": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"tool_call_id": {"type": "string"},
							"observation": {"type": "string", "description": "What was observed from this tool call result"}
						},
						"required": ["tool_call_id"]
					},
					"minItems": 1,
					"description": "References to tool calls in this Run that support this finding. Each tool_call_id is mechanically verified."
				}
			},
			"required": ["severity", "claim", "evidence_refs"]
		}`),
	}
}

func (t *reportFindingTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Severity     string             `json:"severity"`
		Category     string             `json:"category"`
		Claim        string             `json:"claim"`
		Location     string             `json:"location"`
		EvidenceRefs []tool.EvidenceRef `json:"evidence_refs"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if params.Claim == "" {
		return &tool.ToolResult{Content: "claim is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if len(params.EvidenceRefs) == 0 {
		return &tool.ToolResult{Content: "at least one evidence_ref is required — findings must reference actual tool calls", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if err := tool.VerifyEvidenceRefs(ctx, tc, params.EvidenceRefs); err != nil {
		return &tool.ToolResult{Content: "evidence verification failed: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	source := params.Location
	if source == "" {
		source = params.Category
	}
	if source == "" {
		source = "finding"
	}

	insight := fmt.Sprintf("[%s] %s", params.Severity, params.Claim)
	if len(insight) > 240 {
		insight = insight[:240]
	}

	findingID := fmt.Sprintf("rf-%x", sha256.Sum256([]byte(params.Claim+tc.SessionID)))[:19]

	if tc.TaskAddFinding != nil && tc.SessionID != "" {
		if err := tc.TaskAddFinding(ctx, tc.SessionID, findingID, source, insight, params.EvidenceRefs, time.Now()); err != nil {
			if tc.Logger != nil {
				tc.Logger.Warn("report_finding: TaskMemory write failed", "err", err)
			}
		}
	}

	return &tool.ToolResult{
		Content: fmt.Sprintf("Finding recorded (id: %s, severity: %s, %d evidence ref(s) verified). Claim: %s",
			findingID, params.Severity, len(params.EvidenceRefs), params.Claim),
	}, nil
}
