package codeintel

import "slices"

// CodeFragment represents a retrieved code snippet with metadata.
type CodeFragment struct {
	Path       string
	StartLine  int
	EndLine    int
	Content    string
	Kind       FragmentKind
	Symbol     string  // symbol name if applicable
	Reason     string  // why this fragment was retrieved (CI-07 compliance)
	TokenCost  int     // estimated token count
	ValueScore float64 // relevance score (higher = more valuable)
	Language   string  // programming language (e.g. "go", "typescript", "python")
	SourceFile string  // canonical source file path (same as Path, used for cross-ref dedup)
}

// FragmentKind classifies the type of a retrieved code fragment.
type FragmentKind string

const (
	FragmentFocusFile    FragmentKind = "focus_file"
	FragmentSelection    FragmentKind = "selection"
	FragmentFunction     FragmentKind = "function"
	FragmentSignature    FragmentKind = "signature"
	FragmentImport       FragmentKind = "import"
	FragmentDiagnostic   FragmentKind = "diagnostic"
	FragmentRelevantCode FragmentKind = "relevant_code"
	FragmentProjectTree  FragmentKind = "project_tree"
	FragmentUserRef      FragmentKind = "user_ref"
	FragmentVisibleRange FragmentKind = "visible_range"
	FragmentOpenTab      FragmentKind = "open_tab"
	FragmentRecentEdit   FragmentKind = "recent_edit"
	FragmentGitDiff      FragmentKind = "git_diff"
	FragmentLSPDef       FragmentKind = "lsp_definition"
	FragmentLSPRef       FragmentKind = "lsp_reference"
	FragmentDataFlow     FragmentKind = "data_flow"
	FragmentControlFlow  FragmentKind = "control_flow"
	FragmentCrossLang    FragmentKind = "cross_lang_route"
	FragmentProjectMap   FragmentKind = "project_map"
	FragmentTerminal     FragmentKind = "terminal_output"
	FragmentGlobalDiag   FragmentKind = "global_diagnostic"
	FragmentRelatedTest  FragmentKind = "related_test"
	FragmentSiblingImpl  FragmentKind = "sibling_impl"
	FragmentGitBlame     FragmentKind = "git_blame"
	FragmentTypeContext  FragmentKind = "type_context"
	FragmentConstraint   FragmentKind = "constraint"
	FragmentDocContext   FragmentKind = "doc_context"
)

// Priority levels for token budget allocation.
const (
	PriorityP0 = 0 // must include (system prompt, focus file, selection)
	PriorityP1 = 1 // high (direct dependencies, diagnostics)
	PriorityP2 = 2 // medium (relevant code, agentic context)
	PriorityP3 = 3 // low (project structure, git context, memory)
)

// EditorState captures the current state of the user's editor.
type EditorState struct {
	FocusFile      string // currently active file path
	CursorLine     int    // 0-based line number
	CursorCol      int    // 0-based column
	Selection      *Selection
	OpenFiles      []string     // all open editor tabs (not just visible)
	VisibleRange   *LineRange   // visible line range in the active editor
	RecentEdits    []RecentEdit // most recent edit locations across files (LRU, max 20)
	WorkspaceRoots []string     // all open workspace root paths (primary + AllowPaths)

	// Context enrichment signals (CE-01: zero-value safe, no regression when empty).
	GlobalErrors     []DiagnosticEntry `json:"globalErrors,omitempty"`     // project-wide error-level diagnostics (max 20)
	TerminalSnapshot string            `json:"terminalSnapshot,omitempty"` // last 2000 chars of most recent active terminal
	GitStagedFiles   []string          `json:"gitStagedFiles,omitempty"`   // staged file paths (max 30)
	VisibleEditors   []string          `json:"visibleEditors,omitempty"`   // all visible editor file paths (split panes)
}

// DiagnosticEntry represents a single project-level diagnostic (error severity).
type DiagnosticEntry struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Source  string `json:"source"` // "gopls" / "typescript" / "pyright"
}

// RecentEdit records a recent code edit location for context-aware retrieval.
type RecentEdit struct {
	Path      string `json:"path"`
	Timestamp int64  `json:"timestamp"` // Unix milliseconds
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// Selection represents a text selection range in the editor.
type Selection struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Text      string
}

// LineRange represents a contiguous range of lines.
type LineRange struct {
	Start int
	End   int
}

// TaskType classifies the user's intent to drive retrieval strategy.
type TaskType string

const (
	TaskExplain    TaskType = "explain"
	TaskImplement  TaskType = "implement"
	TaskFixBug     TaskType = "fix_bug"
	TaskRefactor   TaskType = "refactor"
	TaskReview     TaskType = "review"
	TaskCompletion TaskType = "completion"
	TaskGeneral    TaskType = "general"
)

// LanguageContext captures the programming language of the focus file,
// used to isolate symbol lookups to the correct language and prevent
// cross-language pollution (e.g. Go's Lock() matching TypeScript's Lock).
type LanguageContext struct {
	Language string   // canonical language name, e.g. "go", "typescript"
	Exts     []string // file extensions for the language, e.g. [".go"] or [".ts", ".tsx"]
}

// DetectLanguage extracts the language context from a file path.
func DetectLanguage(filePath string) LanguageContext {
	ext := detectExt(filePath)
	switch ext {
	case ".go":
		return LanguageContext{Language: "go", Exts: []string{".go"}}
	case ".ts", ".tsx":
		return LanguageContext{Language: "typescript", Exts: []string{".ts", ".tsx"}}
	case ".js", ".jsx":
		return LanguageContext{Language: "javascript", Exts: []string{".js", ".jsx"}}
	case ".py":
		return LanguageContext{Language: "python", Exts: []string{".py"}}
	case ".java":
		return LanguageContext{Language: "java", Exts: []string{".java"}}
	case ".rs":
		return LanguageContext{Language: "rust", Exts: []string{".rs"}}
	case ".cpp", ".cc", ".cxx":
		return LanguageContext{Language: "cpp", Exts: []string{".cpp", ".cc", ".cxx", ".hpp"}}
	case ".c", ".h":
		return LanguageContext{Language: "c", Exts: []string{".c", ".h"}}
	case ".rb":
		return LanguageContext{Language: "ruby", Exts: []string{".rb"}}
	case ".swift":
		return LanguageContext{Language: "swift", Exts: []string{".swift"}}
	case ".kt", ".kts":
		return LanguageContext{Language: "kotlin", Exts: []string{".kt", ".kts"}}
	default:
		return LanguageContext{Language: "", Exts: nil}
	}
}

func detectExt(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '.' {
			return p[i:]
		}
		if p[i] == '/' || p[i] == '\\' {
			break
		}
	}
	return ""
}

func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// ContextSnapshot captures what code context was injected into the LLM
// for a single Assemble call. Pushed to Extension via context/snapshot notification.
type ContextSnapshot struct {
	Fragments        []FragmentSummary `json:"fragments"`
	TokenBudget      int               `json:"token_budget"`
	TokenUsed        int               `json:"token_used"`
	TaskType         string            `json:"task_type"`
	HasMindMap       bool              `json:"has_mind_map"`
	TruncatedCount   int               `json:"truncated_count"`
	ContextNeedScope string            `json:"context_need_scope,omitempty"`
}

// FragmentSummary is a lightweight description of a retrieved code fragment.
type FragmentSummary struct {
	Path       string  `json:"path"`
	Symbol     string  `json:"symbol,omitempty"`
	Kind       string  `json:"kind"`
	Reason     string  `json:"reason"`
	TokenCost  int     `json:"token_cost"`
	ValueScore float64 `json:"value_score"`
}

// SnapshotCallback is called after Assemble with a summary of injected context.
type SnapshotCallback func(snapshot ContextSnapshot)

// FileEntry represents a file-level search result from the L1 full-text index.
type FileEntry struct {
	FilePath  string
	Language  string
	LineCount int
	ByteSize  int
	Snippet   string // FTS5 snippet() highlighted fragment
}

func splitPathSegments(p string) []string {
	var segs []string
	for p != "" {
		dir, file := pathSplit(p)
		if file != "" {
			segs = append(segs, file)
		}
		if dir == p {
			break
		}
		p = dir
	}
	return segs
}

// ── CKG Graph Query Types ──────────────────────────────────────────────────

// EdgeKind classifies a CKG graph edge.
//
// This block is the whole closed set: every kind listed here has a producer,
// and every kind a reader may name is listed here. A constant with no producer
// is worse than a missing one — readers filter on it, tool descriptions promise
// it to the model, and the resulting empty result set is indistinguishable from
// "this code really has no such relationship". Deleted for exactly that reason:
// `route` (routes are a NodeKind, never an edge), `type_ref` /
// `struct_field_type` (declared, filtered on by three readers, never emitted by
// any extractor), `similar_to`, `config_references`, `cross_grpc_calls`,
// `cross_async` (no producer, no consumer), and `tool_wrap` — that last one was
// worse than dead: it was produced, with `source_id = 0`, which no row in
// `symbols` ever has, so every insert lost the foreign key check silently and
// the false-orphan suppression it existed for never once ran. External entry
// points are now an explicit set on CodeIndex (SetExternalEntryPoints), where
// "this symbol is reached from outside the graph" is a property of the symbol
// rather than an edge from a source that does not exist.
type EdgeKind string

const (
	// Structural — emitted by the tree-sitter extractors (worker.go) and Pass 3.
	EdgeCall     EdgeKind = "call"
	EdgeImport   EdgeKind = "import"
	EdgeContains EdgeKind = "contains"
	EdgeExtends  EdgeKind = "extends"

	// Type hierarchy — Pass 8 method-set satisfaction.
	EdgeImplements EdgeKind = "implements"
	EdgeOverrides  EdgeKind = "overrides"

	// Dataflow — Pass 7.
	EdgeDataFlowsTo EdgeKind = "data_flows_to"

	// Cross-language / service topology — Pass 5 and crossservice.go.
	EdgeHandles        EdgeKind = "handles"
	EdgeHTTPCalls      EdgeKind = "http_calls"
	EdgeCrossHTTPCalls EdgeKind = "cross_http_calls"

	// Derived — semantic.go (embeddings), gitco.go (git history), Pass 5 (tests).
	EdgeSemanticRelated EdgeKind = "semantic_related"
	EdgeCoChangesWith   EdgeKind = "co_changes_with"
	EdgeTests           EdgeKind = "tests"
)

// Resolution answers one question: how do we know this edge points where it
// points? It is a closed domain, and it is paired with target_id — see the
// trigger in schema.go.
//
// It replaces a float `certainty` column that had come to mean three unrelated
// things at once: resolution confidence, heuristic strength, and vector
// similarity. The failure that forced the split was not aesthetic — the value
// 1.0 meant both "same-package exact hit" (maximum confidence) and "no
// candidate found at all" (total ignorance), because the resolver `continue`d
// past no-candidate edges and left them at the column default. 86% of
// unresolved call edges therefore sat at 1.0, invisible to the one diagnostic
// that asked "does this symbol only have low-confidence edges?", while genuine
// heuristic edges (overrides 0.6, data_flows_to 0.5) tripped that same
// threshold and got reported as "possibly called via interface dispatch".
//
// A reader now asks for a member of a four-value set instead of guessing what a
// float meant. Measured strength moved to `score`, which is NULL for the eight
// kinds that never had one.
type Resolution string

const (
	// ResolutionExact — a syntactic fact or a unique-name lookup proved the
	// target. target_id is bound and can be trusted.
	ResolutionExact Resolution = "exact"

	// ResolutionInferred — an analysis pass concluded the target (method-set
	// satisfaction, data flow, override matching, embedding similarity).
	// target_id is bound, but by inference rather than by a fact in the source.
	ResolutionInferred Resolution = "inferred"

	// ResolutionAmbiguous — several candidates matched the name, so we
	// deliberately did not pick one. target_id IS NULL and target_name carries
	// the name; readers fall back to name matching and may surface the whole
	// candidate group.
	ResolutionAmbiguous Resolution = "ambiguous"

	// ResolutionUnresolved — no candidate at all. target_id IS NULL. Also the
	// state a bound edge is demoted to when its target symbol is deleted, which
	// is why this is the column default.
	ResolutionUnresolved Resolution = "unresolved"
)

// boundResolutions and scoredEdgeKinds are the two domain subsets the schema
// CHECKs, the Go predicates below, and every reader's WHERE clause are all
// generated from (see sqlInList / resolutionBoundSQL / scoredKindSQL in
// schema.go). They exist as slices rather than as repeated string comparisons
// because the same fact was previously spelled once in Go and once as a SQL
// literal list: adding a third scored kind would update the Go predicate,
// leave the CHECK behind, and surface as an opaque "CHECK constraint failed"
// on the first insert — a runtime error for a fact both sides already knew.
var (
	boundResolutions = []Resolution{ResolutionExact, ResolutionInferred}
	scoredEdgeKinds  = []EdgeKind{EdgeSemanticRelated, EdgeCoChangesWith}
)

// Bound reports whether this resolution implies a usable target_id. It exists so
// no caller re-derives the pairing with its own string comparison; the SQL side
// of the same predicate is resolutionBoundSQL in schema.go.
func (r Resolution) Bound() bool {
	return slices.Contains(boundResolutions, r)
}

// ScoredEdgeKind reports whether a kind carries a measured `score`.
//
// Exactly two do, and both are real measurements over a continuous range:
// semantic_related holds embedding cosine similarity, co_changes_with holds the
// Jaccard coefficient of git co-change. Every other kind used to store a
// hardcoded constant in that float column (import/contains/handles a literal
// 1.0, tests a literal 0.8, overrides 0.6) — numbers that carried no
// information beyond "this edge is of this kind", which `kind` already says.
// Those are NULL now, so a reader that sorts or filters by score cannot
// silently rank an inference constant against a cosine distance.
func ScoredEdgeKind(k EdgeKind) bool {
	return slices.Contains(scoredEdgeKinds, k)
}

// OrphanResult describes an exported symbol classified as potentially dead.
//
// ReachabilityClass is the canonical value from symbols.reachability_class
// (INV-CKG-SINGLE-CLASS). Certainty is derived: 1.0 for isolated, 0.8 for
// source_only, 0.3 for name_reachable.
type OrphanResult struct {
	Symbol            SymbolEntry
	ReachabilityClass string   // "isolated" | "source_only" | "name_reachable"
	Certainty         float64  // 1.0=isolated, 0.8=source_only, 0.3=name_reachable
	Reason            string   // "no_callers" | "no_incoming" | "name_reachable"
	KnownBlindSpots   []string // ["ambiguous_binding"] for name_reachable
	SimilarNames      []string // CKG symbols with similar names (for AI disambiguation)
	Suggestion        string   // actionable hint for the agent
}

// FileReachProfile holds per-file reachability counts and edge resolution rate.
// Populated by CodeIndex.FileReachabilityProfile — the Agent's per-file CKG
// quality signal (INV-CKG-SINGLE-CLASS: all counts read reachability_class).
type FileReachProfile struct {
	Connected          int     // symbols classified "connected"
	SourceOnly         int     // symbols classified "source_only"
	SinkOnly           int     // symbols classified "sink_only"
	NameReachable      int     // symbols classified "name_reachable"
	Isolated           int     // symbols classified "isolated"
	StructuralExempt   int     // symbols classified "structural_exempt"
	TotalFunctions     int     // total classified symbols (function/method/type/interface/class) in this file
	EdgeResolutionRate float64 // bound call edges / total call edges (0–1; -1 if no edges)
}

// FindOrphanOpts controls the scope of orphan detection.
type FindOrphanOpts struct {
	FileFilter       string   // only check symbols under this path prefix
	KindFilter       []string // only check these symbol kinds (empty=all function/method/type/interface/class)
	ExcludeMain      bool     // exclude main/init functions
	ExcludeTests     bool     // exclude test files (*_test.go, *_test.py, *.test.ts, etc.)
	ExcludeBuildTags bool     // exclude files with //go:build e2e|bench|integration tags
	MinCertainty     float64  // exclude results below this certainty threshold (0 = include all)
	Limit            int
}

// ImpactNode describes a symbol affected by a change, discovered via BFS.
//
// Resolution is the resolution of the *last* edge that reached this node, not a
// minimum along the path. The distinction that matters to consumers is binary
// and structural: BFS has two branches, one walking bound `target_id` and one
// walking `target_name` where `target_id IS NULL`, so `Resolution.Bound()`
// answers "was this node reached by a real edge or by a name match". The
// predecessor field was a float documented as "minimum certainty along the
// path" while holding the last edge's own value — and since unresolved call
// edges carried 1.0 (schema v4's overload), the one consumer's low-confidence
// branch was nearly unreachable. Per-hop weakest-link is not computed; a
// consumer wanting it must fold over Path itself.
type ImpactNode struct {
	Symbol     SymbolEntry
	Depth      int        // BFS layer (1 = direct caller)
	EdgeKind   string     // the edge kind that reached this node
	Resolution Resolution // resolution of the reaching edge; Bound() == real edge
	Path       []string   // symbol names from root to this node
}

// SimilarResult describes a symbol that is structurally or semantically
// similar to a query symbol.
type SimilarResult struct {
	Symbol    SymbolEntry
	Score     float64 // 0-1, higher = more similar
	MatchType string  // "name" | "signature" | "callee_set" | "embedding" | "combined"
}

// FindSimilarOpts controls similarity search parameters.
type FindSimilarOpts struct {
	Signature  string // function signature to compare
	CallerPath string // file path of the caller (for callee set comparison)
	CallerLine int    // line of the caller
	Limit      int
	MinScore   float64
}

func pathSplit(p string) (dir, file string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i], p[i+1:]
		}
	}
	return "", p
}

// PathProximity calculates a proximity score (0.0–1.0) between two file paths
// based on shared directory depth. Files in the same directory score highest;
// files with no shared path beyond the workspace root score lowest.
//
// This is a general heuristic: in any project, files closer in the directory
// tree are more likely to be semantically related than distant files.
func PathProximity(focusFile, candidateFile string) float64 {
	focusDir := dirOf(focusFile)
	candidateDir := dirOf(candidateFile)

	if focusDir == candidateDir {
		return 1.0
	}

	focusParts := splitSlash(focusDir)
	candidateParts := splitSlash(candidateDir)

	shared := 0
	limit := len(focusParts)
	if len(candidateParts) < limit {
		limit = len(candidateParts)
	}
	for i := 0; i < limit; i++ {
		if focusParts[i] != candidateParts[i] {
			break
		}
		shared++
	}

	total := len(focusParts)
	if len(candidateParts) > total {
		total = len(candidateParts)
	}
	if total == 0 {
		return 0.5
	}
	return float64(shared) / float64(total)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return ""
}

func splitSlash(p string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' || p[i] == '\\' {
			if i > start {
				parts = append(parts, p[start:i])
			}
			start = i + 1
		}
	}
	return parts
}
