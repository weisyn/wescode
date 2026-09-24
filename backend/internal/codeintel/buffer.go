package codeintel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
)

// NodeKind represents a CKG node label.
type NodeKind string

const (
	NodeFunction  NodeKind = "function"
	NodeMethod    NodeKind = "method"
	NodeClass     NodeKind = "class"
	NodeInterface NodeKind = "interface"
	NodeType      NodeKind = "type"
	NodeModule    NodeKind = "module"
	NodeFile      NodeKind = "file"
	NodePackage   NodeKind = "package"
	NodeRoute     NodeKind = "route"
	NodeConfig    NodeKind = "config"
	NodeTest      NodeKind = "test"
	NodeImport    NodeKind = "import"
)

// IsInterfaceMethod returns true if node is a method whose parent is an Interface node.
func (gb *GraphBuffer) IsInterfaceMethod(node *Node) bool {
	if node == nil || (node.Kind != NodeFunction && node.Kind != NodeMethod) || node.Parent == "" {
		return false
	}
	for _, n := range gb.Nodes {
		if n.Kind == NodeInterface && n.Name == node.Parent && n.PackagePath == node.PackagePath {
			return true
		}
	}
	return false
}

// ChildMethods returns all method/function nodes whose Parent matches the given node name and package.
func (gb *GraphBuffer) ChildMethods(parentName, packagePath string) []*Node {
	var methods []*Node
	for _, n := range gb.Nodes {
		if (n.Kind == NodeFunction || n.Kind == NodeMethod) && n.Parent == parentName && n.PackagePath == packagePath {
			methods = append(methods, n)
		}
	}
	return methods
}

// EdgesByKind returns all edges matching the given kind.
func (gb *GraphBuffer) EdgesByKind(kind EdgeKind) []Edge {
	var result []Edge
	for _, e := range gb.Edges {
		if e.Kind == kind {
			result = append(result, e)
		}
	}
	return result
}

// TaintKind represents a data origin category for security analysis.
type TaintKind string

const (
	TaintUserInput   TaintKind = "user_input"
	TaintConfig      TaintKind = "config"
	TaintDBResult    TaintKind = "db_result"
	TaintExternalAPI TaintKind = "external_api"
)

// Node represents a CKG graph node in the buffer.
type Node struct {
	QualifiedName string
	FilePath      string
	Kind          NodeKind
	Name          string
	Qualified     string // fully qualified name (package.name)
	Signature     string
	Doc           string // documentation comment
	Parent        string
	LineStart     int
	LineEnd       int
	ByteStart     int
	ByteEnd       int
	ContentHash   string
	Exported      bool
	Visibility    string
	PackagePath   string
	CommunityID   int         // Leiden community assignment (0 = unassigned)
	Taints        []TaintKind // data origin taints (INV-P5-04: propagated, not persisted as edges)
	Effects       *EffectSet  // populated by PropagateEffects, stored as JSON in DB
	Summary       string      // LLM-generated one-sentence description (optional, Pass 6.5)
	SummaryHash   string      // content_hash when summary was generated (cache key)

	RuntimeFrequency int     // from OTEL traces (0 = no data)
	Coverage         float64 // from go test coverprofile (-1 = unknown, 0.0-1.0 = measured)
	CpuPct           float64 // from pprof (-1 = unknown, percentage)
}

// Edge represents a CKG graph edge in the buffer.
type Edge struct {
	SourceQName string
	TargetQName string
	TargetName  string
	Kind        EdgeKind

	// Resolution says how the target was determined; see the Resolution doc in
	// types.go. Zero value is deliberately not a valid member — an edge that
	// forgets to set it fails the schema CHECK at flush time rather than
	// defaulting into a state that reads as trustworthy.
	Resolution Resolution

	// Score is a measured strength, and only the two kinds in ScoredEdgeKind
	// have one (cosine similarity, Jaccard coefficient). nil everywhere else:
	// the eight remaining kinds used to write a hardcoded constant here, which
	// let readers rank an inference constant against a real distance.
	Score *float64

	Source   string
	FlowType string // "param_pass" | "return_value" | "field_access" (data flow specific)
	ParamIdx int    // parameter index for DATA_FLOWS_TO edges (-1 = not applicable)
	Metadata string // JSON metadata (e.g. {"virtual":true,"source":"method_set_satisfaction"})
}

// flushResolution decides the resolution an edge is stored with, reconciling
// what the producer claimed against whether the target actually landed in this
// buffer. It is the single place that pairing is established, so the schema
// CHECK never has to reject a row a writer could have fixed.
//
// The two mismatches are different kinds of event and are answered differently:
//
//   - Claimed bound, target absent. Legitimate and common — the target lives in
//     a file this run did not parse, or in a third-party package. The edge keeps
//     target_name and degrades to unresolved, which is the state the read-side
//     name fallback already consumes.
//   - Claimed unbound, target present. The producer set TargetQName to a real
//     node while saying it did not pick one. That is a contradiction inside the
//     producer, not a property of the corpus, so it fails the flush. Silently
//     upgrading it would let a Pass that stopped maintaining Resolution keep
//     writing rows that read as proven.
//
// An empty Resolution is also fatal: the zero value is not a member of the
// domain, and letting it default would make "forgot to set it" indistinguishable
// from "looked and found nothing" — the exact confusion the float column had.
func flushResolution(e Edge, bound bool) (Resolution, error) {
	switch e.Resolution {
	case ResolutionExact, ResolutionInferred:
		if !bound {
			return ResolutionUnresolved, nil
		}
		return e.Resolution, nil
	case ResolutionAmbiguous, ResolutionUnresolved:
		if bound {
			return "", fmt.Errorf("edge %s→%s (%s): resolution %q but target is bound",
				e.SourceQName, e.TargetQName, e.Kind, e.Resolution)
		}
		return e.Resolution, nil
	default:
		return "", fmt.Errorf("edge %s→%s (%s): resolution %q is not a member of the domain",
			e.SourceQName, e.TargetQName, e.Kind, e.Resolution)
	}
}

// GraphBuffer is an in-memory accumulator for CKG nodes and edges.
// All Pass 2-9 write into the buffer; Pass 10 flushes to staging.db atomically.
// Thread-safe: multiple Workers can AddNode/AddEdge concurrently.
type GraphBuffer struct {
	mu    sync.RWMutex
	Nodes map[string]*Node // keyed by QualifiedName
	Edges []Edge
	Files map[string]int64 // file_path → mtime_ns
}

// NewGraphBuffer creates an empty buffer.
func NewGraphBuffer() *GraphBuffer {
	return &GraphBuffer{
		Nodes: make(map[string]*Node, 4096),
		Edges: make([]Edge, 0, 16384),
		Files: make(map[string]int64, 1024),
	}
}

// AddNode inserts or updates a node. Last-writer-wins for duplicate QualifiedName.
func (gb *GraphBuffer) AddNode(n *Node) {
	gb.mu.Lock()
	gb.Nodes[n.QualifiedName] = n
	gb.mu.Unlock()
}

// AddEdge appends an edge. Duplicates are allowed (resolved during flush).
func (gb *GraphBuffer) AddEdge(e Edge) {
	gb.mu.Lock()
	gb.Edges = append(gb.Edges, e)
	gb.mu.Unlock()
}

// AddFile records a file with its mtime for hash tracking.
func (gb *GraphBuffer) AddFile(path string, mtimeNs int64) {
	gb.mu.Lock()
	gb.Files[path] = mtimeNs
	gb.mu.Unlock()
}

// NodeCount returns the number of buffered nodes.
func (gb *GraphBuffer) NodeCount() int {
	gb.mu.RLock()
	defer gb.mu.RUnlock()
	return len(gb.Nodes)
}

// EdgeCount returns the number of buffered edges.
func (gb *GraphBuffer) EdgeCount() int {
	gb.mu.RLock()
	defer gb.mu.RUnlock()
	return len(gb.Edges)
}

// FlushToStaging writes all buffered nodes and edges into staging.db
// in a single transaction. Returns nil on success.
func (gb *GraphBuffer) FlushToStaging(stagingDB *sql.DB) error {
	gb.mu.RLock()
	defer gb.mu.RUnlock()

	tx, err := stagingDB.Begin()
	if err != nil {
		return fmt.Errorf("begin staging tx: %w", err)
	}
	defer tx.Rollback()

	symStmt, err := tx.Prepare(`INSERT INTO symbols
		(file_path, kind, name, qualified, signature, doc, parent, line_start, line_end, byte_start, byte_end, content_hash, exported, visibility, package_path, community_id, summary, summary_hash, effects, runtime_frequency, coverage, cpu_pct)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare symbol insert: %w", err)
	}
	defer symStmt.Close()

	// Build QualifiedName → rowid map for edge resolution.
	qnameToID := make(map[string]int64, len(gb.Nodes))

	for _, n := range gb.Nodes {
		exported := 0
		if n.Exported {
			exported = 1
		}
		effectsJSON := ""
		if n.Effects != nil {
			if b, err := json.Marshal(n.Effects); err == nil {
				effectsJSON = string(b)
			}
		}
		res, err := symStmt.Exec(
			IndexPath(n.FilePath), string(n.Kind), n.Name, n.Qualified, n.Signature, n.Doc, n.Parent,
			n.LineStart, n.LineEnd, n.ByteStart, n.ByteEnd,
			n.ContentHash, exported, n.Visibility, IndexPath(n.PackagePath), n.CommunityID,
			n.Summary, n.SummaryHash, effectsJSON,
			n.RuntimeFrequency, n.Coverage, n.CpuPct,
		)
		if err != nil {
			return fmt.Errorf("insert node %s: %w", n.QualifiedName, err)
		}
		id, _ := res.LastInsertId()
		qnameToID[n.QualifiedName] = id
	}

	edgeStmt, err := tx.Prepare(`INSERT INTO edges
		(source_id, target_id, target_name, kind, resolution, score, source, flow_type, param_idx, metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare edge insert: %w", err)
	}
	defer edgeStmt.Close()

	for _, e := range gb.Edges {
		sourceID := qnameToID[e.SourceQName]
		if sourceID == 0 {
			continue // dangling edge: source node not in buffer, skip silently
		}
		var targetID sql.NullInt64
		if tid, ok := qnameToID[e.TargetQName]; ok && tid != 0 {
			targetID = sql.NullInt64{Int64: tid, Valid: true}
		}
		resolution, err := flushResolution(e, targetID.Valid)
		if err != nil {
			return err
		}
		targetName := e.TargetName
		if targetName == "" {
			targetName = e.TargetQName
		}
		paramIdx := e.ParamIdx
		if paramIdx == 0 && e.FlowType == "" {
			paramIdx = -1
		}
		var score any
		if e.Score != nil {
			score = *e.Score
		}
		_, err = edgeStmt.Exec(sourceID, targetID, targetName, string(e.Kind), string(resolution), score, e.Source, e.FlowType, paramIdx, e.Metadata)
		if err != nil {
			return fmt.Errorf("insert edge %s→%s: %w", e.SourceQName, e.TargetQName, err)
		}
	}

	// Write file hashes.
	hashStmt, err := tx.Prepare(`INSERT OR REPLACE INTO file_hashes (file_path, content_hash, mtime_ns) VALUES (?, '', ?)`)
	if err != nil {
		return fmt.Errorf("prepare file_hash insert: %w", err)
	}
	defer hashStmt.Close()

	for path, mtime := range gb.Files {
		if _, err := hashStmt.Exec(IndexPath(path), mtime); err != nil {
			return fmt.Errorf("insert file_hash %s: %w", path, err)
		}
	}

	return tx.Commit()
}

// MergeFrom copies all nodes, edges, and files from another buffer into this one.
// Nodes with the same QualifiedName are overwritten (last-writer-wins).
func (gb *GraphBuffer) MergeFrom(other *GraphBuffer) {
	other.mu.RLock()
	defer other.mu.RUnlock()
	gb.mu.Lock()
	defer gb.mu.Unlock()
	for k, n := range other.Nodes {
		gb.Nodes[k] = n
	}
	gb.Edges = append(gb.Edges, other.Edges...)
	for k, v := range other.Files {
		gb.Files[k] = v
	}
}

// Reset clears the buffer for reuse.
func (gb *GraphBuffer) Reset() {
	gb.mu.Lock()
	gb.Nodes = make(map[string]*Node, 4096)
	gb.Edges = make([]Edge, 0, 16384)
	gb.Files = make(map[string]int64, 1024)
	gb.mu.Unlock()
}
