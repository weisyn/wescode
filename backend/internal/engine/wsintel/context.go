package wsintel

import (
	"path/filepath"
	"time"
)

// WorkspaceContext captures the structural knowledge about the current
// workspace that gets injected into the system prompt.
type WorkspaceContext struct {
	Primary    ProjectInfo
	Siblings   []ProjectInfo
	Relations  []Relation
	BuildInfo  *BuildInfo
	CKG        *CKGStatus
	ActiveArea string   // directory with most recent user edits (from AttentionTracker)
	Boundary   Boundary // territory / read-only / generated classification
}

// ProjectInfo describes a single project root detected via marker files.
type ProjectInfo struct {
	Path    string
	Type    string // "go" / "node" / "rust" / "python" / "java" / "mixed" / "unknown" / ...
	Name    string
	Markers []string

	// Modules lists manifest-bearing subdirectories, populated only when the
	// root itself carries no manifest. A monorepo (backend/go.mod,
	// web/package.json) otherwise probes as "unknown", which loses the type,
	// the build commands and the project name in one stroke.
	Modules []Module

	// Description is the project's own one-line self-description, taken from
	// the ecosystem's manifest field or the README. Without it the model has
	// nothing but the directory name and will invent a purpose for the repo.
	Description string

	Frameworks  []string // detected frameworks: "cobra", "gin", "react", "vue", "spring-boot", ...
	TestPattern string   // "*_test.go" / "*.test.ts" / "test_*.py" / ...

	DeprecatedCount int           // estimated deprecated API usage count
	LastCommitAge   time.Duration // time since last commit
	TestFileCount   int           // number of test files
	FileCount       int           // total source files (non-vendor, non-node_modules)
	CommitterCount  int           // unique committers
}

// Module is a manifest-bearing directory inside a repository whose root holds
// no manifest of its own.
type Module struct {
	Dir  string // relative to ProjectInfo.Path
	Type string
}

// BuildRoot is the directory build commands should run in. A repository with
// exactly one module has an unambiguous answer; anything else resolves to the
// project root, because a monorepo has no single build command and guessing one
// is worse than leaving it to the model, which can see the Modules list.
func (p ProjectInfo) BuildRoot() string {
	if len(p.Modules) == 1 {
		return filepath.Join(p.Path, p.Modules[0].Dir)
	}
	return p.Path
}

// Relation describes a dependency edge between two projects in the workspace.
type Relation struct {
	From string
	To   string
	Kind string // "depends_on" / "consumed_by" / "shares_types"
}

// BuildInfo holds the detected build/test/lint commands for a project.
type BuildInfo struct {
	Build string
	Test  string
	Lint  string
}

// CKGStatus holds the code knowledge graph readiness to be formatted into prompt.
type CKGStatus struct {
	TotalFiles   int
	IndexedFiles int
	StaleFiles   int
	Completeness float64
	Indexing     bool

	// Focus file reachability — populated from attentionTracker + FileReachabilityProfile.
	// FocusFile == "" means no focus file available; remaining fields are ignored.
	FocusFile               string  // CKG-relative path of the most recently edited file
	FocusEdgeResolutionRate float64 // bound call edges / total call edges (0–1; -1 if no edges)
	FocusNameReachableCount int     // name_reachable symbols in the focus file
	FocusIsolatedCount      int     // isolated symbols in the focus file
}

// BoundaryZone classifies a directory path.
type BoundaryZone string

const (
	ZoneTerritory BoundaryZone = "territory" // read-write: project source, config, tests
	ZoneReadOnly  BoundaryZone = "readonly"  // read-only reference: vendor/, node_modules/
	ZoneGenerated BoundaryZone = "generated" // cautious write: *.pb.go, *.gen.ts
)

// Boundary holds classified directory zones for the primary project.
type Boundary struct {
	ReadOnlyDirs  []string // vendor/, node_modules/, .git/objects/
	GeneratedDirs []string // dirs containing generated files
}
