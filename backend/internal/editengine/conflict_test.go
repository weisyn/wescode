package editengine

import "testing"

func TestConflictDetector_NoConflict(t *testing.T) {
	cd := NewConflictDetector()

	c := cd.RecordEdit("agent-a", "run-1", "config.go", 100, 200)
	if c != nil {
		t.Fatal("no conflict expected for first edit")
	}

	c = cd.RecordEdit("agent-b", "run-2", "config.go", 300, 400)
	if c != nil {
		t.Fatal("no conflict expected for non-overlapping ranges")
	}
}

func TestConflictDetector_OverlapDetected(t *testing.T) {
	cd := NewConflictDetector()

	cd.RecordEdit("agent-a", "run-1", "config.go", 100, 200)
	c := cd.RecordEdit("agent-b", "run-2", "config.go", 150, 250)
	if c == nil {
		t.Fatal("expected conflict for overlapping ranges")
	}
	if c.AgentA != "agent-a" || c.AgentB != "agent-b" {
		t.Errorf("agents wrong: A=%s B=%s", c.AgentA, c.AgentB)
	}
	if c.Path != "config.go" {
		t.Errorf("path wrong: %s", c.Path)
	}
}

func TestConflictDetector_SameAgentNoConflict(t *testing.T) {
	cd := NewConflictDetector()

	cd.RecordEdit("agent-a", "run-1", "config.go", 100, 200)
	c := cd.RecordEdit("agent-a", "run-1", "config.go", 150, 250)
	if c != nil {
		t.Fatal("same agent should not conflict with itself")
	}
}

func TestConflictDetector_DifferentFiles(t *testing.T) {
	cd := NewConflictDetector()

	cd.RecordEdit("agent-a", "run-1", "a.go", 100, 200)
	c := cd.RecordEdit("agent-b", "run-2", "b.go", 100, 200)
	if c != nil {
		t.Fatal("different files should not conflict")
	}
}

func TestConflictDetector_Reset(t *testing.T) {
	cd := NewConflictDetector()

	cd.RecordEdit("agent-a", "run-1", "config.go", 100, 200)
	cd.Reset()

	c := cd.RecordEdit("agent-b", "run-2", "config.go", 150, 250)
	if c != nil {
		t.Fatal("after reset, no conflict should be detected")
	}
}

func TestConflictDetector_AdjacentNoOverlap(t *testing.T) {
	cd := NewConflictDetector()

	cd.RecordEdit("agent-a", "run-1", "config.go", 100, 200)
	c := cd.RecordEdit("agent-b", "run-2", "config.go", 200, 300)
	if c != nil {
		t.Fatal("adjacent (non-overlapping) ranges should not conflict")
	}
}

func TestFormatConflict(t *testing.T) {
	c := &ConflictResult{
		Path:   "config.go",
		AgentA: "agent-a",
		AgentB: "agent-b",
		RangeA: EditRange{Start: 100, End: 200},
		RangeB: EditRange{Start: 150, End: 250},
	}
	msg := FormatConflict(c)
	if msg == "" {
		t.Fatal("formatted conflict should not be empty")
	}
}
