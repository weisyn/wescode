package engine

import wesgine "github.com/weisyn/wesgine"

// WorkerCyclePreset is the strict CycleDetect for delegated leaf Workers.
// Compared to the Orchestrator/Cell default:
//   - IdenticalCallTerm: 7 → 5 (faster cycle detection)
//   - ActionTerm: 50 → 40 (Worker scope is narrow)
//   - edit/write/apply_patch: [30,60] → [20,40]
var WorkerCyclePreset = wesgine.CycleDetectConfig{
	ExplorationWarnThreshold:   50,
	ExplorationTermThreshold:   100,
	ActionWarnThreshold:        20,
	ActionTermThreshold:        40,
	IdenticalCallWarnThreshold: 3,
	IdenticalCallTermThreshold: 5,
	ToolOverrides: map[string][2]int{
		"exec":          {20, 40},
		"exec:readonly": {40, 80},
		"edit":          {20, 40},
		"write":         {20, 40},
		"apply_patch":   {20, 40},
	},
}

// OrchestratorCyclePreset matches the Cell-level default — orchestrators
// need wide latitude because they coordinate multiple workers.
var OrchestratorCyclePreset = wesgine.CycleDetectConfig{
	ExplorationWarnThreshold:   50,
	ExplorationTermThreshold:   100,
	ActionWarnThreshold:        25,
	ActionTermThreshold:        50,
	IdenticalCallWarnThreshold: 4,
	IdenticalCallTermThreshold: 7,
	ToolOverrides: map[string][2]int{
		"exec":          {30, 60},
		"exec:readonly": {50, 100},
		"edit":          {30, 60},
		"write":         {30, 60},
		"apply_patch":   {30, 60},
	},
}

// CyclePresetForRole returns the CycleDetectConfig for a delegation role.
// Returns nil for unknown roles (use Cell default).
func CyclePresetForRole(role string) *wesgine.CycleDetectConfig {
	switch role {
	case "leaf", "worker":
		cp := WorkerCyclePreset
		return &cp
	case "orchestrator":
		cp := OrchestratorCyclePreset
		return &cp
	default:
		return nil
	}
}
