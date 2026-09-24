package constraints

import (
	"os"
	"path/filepath"
)

// SeedGoConstraints registers 5 executable Go constraints as a cold-start
// baseline when CKG is empty. Each seed has a Checker (INV-CSE-15).
// internal_boundary and test_no_main were dropped — no executable checker.
func SeedGoConstraints(reg *Registry, workDir string) int {
	if reg == nil {
		return 0
	}
	goMod := filepath.Join(workDir, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		return 0
	}
	root := absRoot(workDir)

	seeds := []Constraint{
		Constraint{
			ID:         "seed-go-err-check",
			Rule:       "error variables must be checked or explicitly ignored (`_ = err`). Never discard error returns silently.",
			Kind:       "quality",
			Priority:   PriorityQuality,
			Status:     StatusActive,
			Confidence: 0.9,
			Source:     SourceSeed,
			TTL:        0,
		}.AtTreeGo(root, CheckErrDiscard),
		Constraint{
			ID:         "seed-go-mutex-defer",
			Rule:       "sync.Mutex.Lock() must have a paired defer Unlock() in the same function to prevent deadlocks.",
			Kind:       "quality",
			Priority:   PriorityQuality,
			Status:     StatusActive,
			Confidence: 0.9,
			Source:     SourceSeed,
			TTL:        0,
		}.AtTreeGo(root, CheckMutexDefer),
		Constraint{
			ID:         "seed-go-exported-doc",
			Rule:       "Exported functions, types, and methods should have a documentation comment starting with the identifier name.",
			Kind:       "consistency",
			Priority:   PriorityConsistency,
			Status:     StatusCandidate,
			Confidence: 0.5,
			Source:     SourceSeed,
			TTL:        180,
		}.AtTreeGo(root, CheckExportedDoc),
		Constraint{
			ID:         "seed-go-context-first",
			Rule:       "context.Context should be the first parameter of a function, conventionally named `ctx`.",
			Kind:       "consistency",
			Priority:   PriorityConsistency,
			Status:     StatusCandidate,
			Confidence: 0.7,
			Source:     SourceSeed,
			TTL:        180,
		}.AtTreeGo(root, CheckContextFirst),
		Constraint{
			ID:         "seed-go-no-init-io",
			Rule:       "Do not perform I/O operations (network, file, database) in init() functions. Use explicit initialization in main() or constructors.",
			Kind:       "architecture",
			Priority:   PriorityArchitecture,
			Status:     StatusCandidate,
			Confidence: 0.6,
			Source:     SourceSeed,
			TTL:        120,
		}.AtTreeGo(root, CheckNoInitIO),
	}

	count := 0
	for _, s := range seeds {
		if existing := reg.Get(s.ID); existing != nil {
			continue
		}
		reg.Add(s)
		count++
	}
	return count
}
