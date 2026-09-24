package codeintel

// ContextStrategy defines per-TaskType CKG tool weights and context budget allocation.
type ContextStrategy struct {
	CKGWeights    map[string]float64 // tool_name → priority weight (0.0-1.0)
	CommunityBias float64            // bonus weight for same-community content (1.0 = no bias)
	MaxDepth      int                // max CKG traversal depth
	Description   string
}

// DefaultStrategies maps each TaskType to its optimal context strategy.
var DefaultStrategies = map[TaskType]ContextStrategy{
	TaskFixBug: {
		CKGWeights: map[string]float64{
			"find_callers":          0.9,
			"impact_analysis":       0.8,
			"find_co_changed_files": 0.7,
			"check_constraints":     0.6,
		},
		CommunityBias: 1.5,
		MaxDepth:      3,
		Description:   "Bug fix: callers + impact + co-change",
	},
	TaskImplement: {
		CKGWeights: map[string]float64{
			"semantic_search":           0.9,
			"get_architecture_overview": 0.8,
			"find_implementations":      0.7,
			"search_symbols":            0.6,
		},
		CommunityBias: 1.2,
		MaxDepth:      2,
		Description:   "Implementation: semantic + architecture + similar patterns",
	},
	TaskRefactor: {
		CKGWeights: map[string]float64{
			"find_callers":         0.9,
			"find_implementations": 0.8,
			"impact_analysis":      0.8,
			"check_constraints":    0.7,
		},
		CommunityBias: 1.3,
		MaxDepth:      4,
		Description:   "Refactor: all dependents + constraints + deep impact",
	},
	TaskExplain: {
		CKGWeights: map[string]float64{
			"get_architecture_overview": 0.9,
			"find_callers":              0.5,
			"find_callees":              0.5,
		},
		CommunityBias: 1.0,
		MaxDepth:      2,
		Description:   "Explain: overview + local call context",
	},
	TaskReview: {
		CKGWeights: map[string]float64{
			"check_constraints":     0.9,
			"impact_analysis":       0.8,
			"find_callers":          0.7,
			"find_co_changed_files": 0.6,
		},
		CommunityBias: 1.4,
		MaxDepth:      3,
		Description:   "Review: constraints + impact + co-change patterns",
	},
	TaskCompletion: {
		CKGWeights: map[string]float64{
			"search_symbols":  0.8,
			"semantic_search": 0.6,
		},
		CommunityBias: 1.1,
		MaxDepth:      1,
		Description:   "Completion: symbols + semantic neighbors (minimal)",
	},
	TaskGeneral: {
		CKGWeights: map[string]float64{
			"search_symbols":            0.7,
			"find_callers":              0.6,
			"get_architecture_overview": 0.5,
		},
		CommunityBias: 1.0,
		MaxDepth:      2,
		Description:   "General: balanced default",
	},
}

// GetStrategy returns the context strategy for a task type.
// Falls back to TaskGeneral if not found.
func GetStrategy(task TaskType) ContextStrategy {
	if s, ok := DefaultStrategies[task]; ok {
		return s
	}
	return DefaultStrategies[TaskGeneral]
}

// ToolWeight returns the priority weight for a CKG tool in the given strategy.
// Returns 0 if the tool is not relevant for this task type.
func (s ContextStrategy) ToolWeight(toolName string) float64 {
	return s.CKGWeights[toolName]
}

// ApplyCommunityBias adjusts a fragment's value score based on whether
// it belongs to the same Leiden community as the focus file.
func (s ContextStrategy) ApplyCommunityBias(baseScore float64, sameCommunity bool) float64 {
	if sameCommunity {
		return baseScore * s.CommunityBias
	}
	return baseScore
}
