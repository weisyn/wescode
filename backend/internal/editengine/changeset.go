package editengine

import (
	"sync"
	"time"
)

type changesetStatus string

const (
	changesetPending  changesetStatus = "pending"
	changesetAccepted changesetStatus = "accepted"
	changesetRejected changesetStatus = "rejected"
)

// ChangesetFile describes a single file within a changeset.
type ChangesetFile struct {
	Path   string   `json:"path"`
	Action string   `json:"action"` // "modified" | "created" | "deleted"
	TxIDs  []string `json:"txIds,omitempty"`
}

// Changeset groups all file modifications from a single Run into a cohesive
// unit that can be accepted or rejected as a whole (INV-EDIT-23).
type Changeset struct {
	mu           sync.Mutex
	ID           string          `json:"id"`
	CheckpointID string          `json:"checkpointId"`
	Files        []ChangesetFile `json:"files"`
	Status       changesetStatus `json:"status"`
	CreatedAt    time.Time       `json:"createdAt"`

	fileIndex map[string]int // path → index in Files
}

// NewChangeset creates a changeset bound to a RunID and Checkpoint.
func NewChangeset(runID, checkpointID string) *Changeset {
	return &Changeset{
		ID:           runID,
		CheckpointID: checkpointID,
		Status:       changesetPending,
		CreatedAt:    time.Now(),
		fileIndex:    make(map[string]int),
	}
}

// AddFile records a file modification. Deduplicates by path; subsequent
// calls for the same path append txIDs but don't create duplicate entries.
func (cs *Changeset) AddFile(path, action, txID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if idx, ok := cs.fileIndex[path]; ok {
		if txID != "" {
			cs.Files[idx].TxIDs = append(cs.Files[idx].TxIDs, txID)
		}
		if action == "created" && cs.Files[idx].Action == "" {
			cs.Files[idx].Action = action
		}
		return
	}

	f := ChangesetFile{
		Path:   path,
		Action: action,
	}
	if txID != "" {
		f.TxIDs = []string{txID}
	}
	cs.fileIndex[path] = len(cs.Files)
	cs.Files = append(cs.Files, f)
}

// Accept marks the changeset as accepted (checkpoint can be dropped).
func (cs *Changeset) Accept() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Status = changesetAccepted
}

// Reject marks the changeset as rejected.
func (cs *Changeset) Reject() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Status = changesetRejected
}

// FilePaths returns all file paths in the changeset.
func (cs *Changeset) FilePaths() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	paths := make([]string, len(cs.Files))
	for i, f := range cs.Files {
		paths[i] = f.Path
	}
	return paths
}

// Snapshot returns a copy of the changeset state for serialization.
func (cs *Changeset) Snapshot() ChangesetSnapshot {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	files := make([]ChangesetFile, len(cs.Files))
	copy(files, cs.Files)
	return ChangesetSnapshot{
		ID:           cs.ID,
		CheckpointID: cs.CheckpointID,
		Files:        files,
		Status:       cs.Status,
		CreatedAt:    cs.CreatedAt,
	}
}

// ChangesetSnapshot is an immutable copy of a Changeset for serialization.
type ChangesetSnapshot struct {
	ID           string          `json:"id"`
	CheckpointID string          `json:"checkpointId"`
	Files        []ChangesetFile `json:"files"`
	Status       changesetStatus `json:"status"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// FileCount returns the number of files in the changeset.
func (cs *Changeset) FileCount() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.Files)
}
