package editengine

import (
	"fmt"
	"sync"
)

// EditRange represents a byte range modified by an agent in a file.
type EditRange struct {
	AgentID string
	RunID   string
	Path    string
	Start   int
	End     int
}

// ConflictResult describes an overlap between two agents' edit ranges.
type ConflictResult struct {
	Path   string
	AgentA string
	AgentB string
	RangeA EditRange
	RangeB EditRange
}

// ConflictDetector tracks per-file edit ranges from multiple agents within a Cell
// and detects byte-range overlaps. When a conflict is detected, it is reported
// to the orchestrator (not resolved by individual agents).
type ConflictDetector struct {
	mu     sync.Mutex
	ranges map[string][]EditRange // path → edit ranges from all agents
}

func NewConflictDetector() *ConflictDetector {
	return &ConflictDetector{
		ranges: make(map[string][]EditRange),
	}
}

// RecordEdit records that an agent modified a byte range in a file.
// Returns a ConflictResult if this edit overlaps with a previous edit from a different agent.
// Returns nil if no conflict.
func (cd *ConflictDetector) RecordEdit(agentID, runID, path string, start, end int) *ConflictResult {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	newRange := EditRange{
		AgentID: agentID,
		RunID:   runID,
		Path:    path,
		Start:   start,
		End:     end,
	}

	existing := cd.ranges[path]
	for _, r := range existing {
		if r.AgentID == agentID {
			continue
		}
		if rangesOverlap(r.Start, r.End, start, end) {
			conflict := &ConflictResult{
				Path:   path,
				AgentA: r.AgentID,
				AgentB: agentID,
				RangeA: r,
				RangeB: newRange,
			}
			cd.ranges[path] = append(existing, newRange)
			return conflict
		}
	}

	cd.ranges[path] = append(existing, newRange)
	return nil
}

// Reset clears all tracked edit ranges. Called at session boundaries.
func (cd *ConflictDetector) Reset() {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.ranges = make(map[string][]EditRange)
}

// ResetFile clears tracked ranges for a specific file.
func (cd *ConflictDetector) ResetFile(path string) {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	delete(cd.ranges, path)
}

func rangesOverlap(s1, e1, s2, e2 int) bool {
	return s1 < e2 && s2 < e1
}

// FormatConflict formats a conflict for reporting to the orchestrator.
func FormatConflict(c *ConflictResult) string {
	return fmt.Sprintf(
		"⚠ Edit conflict in %s: agent %q modified bytes [%d,%d) which overlaps with agent %q's edit at [%d,%d). Orchestrator must resolve.",
		c.Path, c.AgentB, c.RangeB.Start, c.RangeB.End, c.AgentA, c.RangeA.Start, c.RangeA.End,
	)
}
