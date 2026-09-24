// Package constraints implements the Constraint Registry — a persistent
// store of project-level rules (R2 prohibitions, R6 quality, R7 consistency)
// that the AI must follow when writing code.
//
// Constraints are persisted to code.db (the CKG SQLite database), co-located
// with the structural knowledge they derive from. This ensures constraint
// lifecycle is bound to the index, not to the Memory subsystem (which is
// reserved for human-readable AI cognitive memory).
//
// Business logic on top of raw persistence:
//   - Confidence-based lifecycle (Candidate → Active → Retired)
//   - TTL-based decay for unused constraints
//   - PreWriteCheck integration (warn when code violates a constraint)
//   - Conflict detection with priority resolution
package constraints

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Priority defines the enforcement level of a constraint.
type Priority int

const (
	PrioritySecurity     Priority = 1 // never violate
	PriorityData         Priority = 2 // never violate
	PriorityArchitecture Priority = 3 // can violate with explicit rationale
	PriorityQuality      Priority = 4 // can weigh trade-offs
	PriorityConsistency  Priority = 5 // can make exceptions
)

// Status is the lifecycle state of a constraint.
type Status string

const (
	StatusCandidate Status = "candidate" // inferred, confidence < 0.8
	StatusActive    Status = "active"    // confirmed or high-confidence
	StatusRetired   Status = "retired"   // expired or superseded
)

// RetiredBy records who retired a constraint. The distinction decides whether a
// later inference pass may resurrect it: a human dismissal is a verdict that
// outlives the code that triggered it, while a mechanical retirement is just a
// statement that the evidence went away and may legitimately come back.
type RetiredBy string

const (
	RetiredByUser      RetiredBy = "user"      // dismissed by a human — permanent
	RetiredByDecay     RetiredBy = "decay"     // TTL expired — resurrectable
	RetiredByReconcile RetiredBy = "reconcile" // evidence vanished — resurrectable
)

// Constraint sources. Machine sources are re-derived on every inference pass and
// therefore reconcilable; human sources (learned/declared) and the cold-start
// baseline (seed) are not.
const (
	SourceInferred        = "inferred"         // project structure scan
	SourceInferredCKG     = "inferred-ckg"     // CKG edges (types, compatibility, dep direction)
	SourceInferredPattern = "inferred-pattern" // AST pattern frequency
	SourceLearned         = "learned"          // user rejected/modified an edit
	SourceDeclared        = "declared"         // human-authored
	SourceSeed            = "seed"             // language baseline, cold start
)

// machineSources is the set of sources produced by inference. Only these are
// eligible for reconcile-driven retirement.
var machineSources = map[string]struct{}{
	SourceInferred:        {},
	SourceInferredCKG:     {},
	SourceInferredPattern: {},
}

// IsMachineSource reports whether src is re-derived by an inference pass.
func IsMachineSource(src string) bool {
	_, ok := machineSources[src]
	return ok
}

// legacySources are sources from deleted subsystems. Rows carrying them are
// purged on load rather than migrated: the code that could interpret them no
// longer exists, so keeping them would mean shipping constraints nothing can
// evaluate (INV-CSE-15).
var legacySources = []string{"agents_md", "convention_mining", "convention_cse"}

// Constraint is a single project-level rule. Rule is human-readable only
// (INV-CSE-16). Judging uses Checker; matching uses Root+TargetKind+TargetPath.
type Constraint struct {
	ID         string      `json:"id"`
	Rule       string      `json:"rule"`
	Kind       string      `json:"kind"` // "security", "architecture", "quality", "consistency", "data"
	Priority   Priority    `json:"priority"`
	Status     Status      `json:"status"`
	Confidence float64     `json:"confidence"`
	Source     string      `json:"source"` // one of the Source* constants above
	Root       string      `json:"root"`   // workspace root that produced this constraint; empty is illegal
	TargetKind TargetKind  `json:"target_kind"`
	TargetPath string      `json:"target_path"` // relative to Root, never empty
	Checker    CheckerSpec `json:"checker"`
	TTL        int         `json:"ttl_days"` // 0 = never expires (human-confirmed)
	UsageCount int         `json:"usage_count"`
	LastUsed   time.Time   `json:"last_used"`
	LastSeen   time.Time   `json:"last_seen"` // last inference pass that re-derived this
	CreatedAt  time.Time   `json:"created_at"`
	RetiredBy  RetiredBy   `json:"retired_by,omitempty"`

	// Evidence is what the inferrer saw: the packages forming the cycle, the
	// call sites missing a guard. It is the human panel's answer to "why does
	// this constraint exist" (INV-CSE-13) — without it a reviewer has to
	// re-derive the finding by hand before deciding to confirm or dismiss.
	// Never sent to the model; the model gets the checker's verdict.
	Evidence []string `json:"evidence,omitempty"`
}

// IsActive reports whether this constraint should be enforced.
func (c *Constraint) IsActive() bool {
	return c.Status == StatusActive
}

// ShouldRetire reports whether this constraint has expired.
func (c *Constraint) ShouldRetire() bool {
	if c.Confidence >= 1.0 {
		return false // human-confirmed never expires
	}
	if c.TTL <= 0 {
		return false
	}
	ttl := time.Duration(c.TTL) * 24 * time.Hour
	return time.Since(c.LastUsed) > ttl
}

// Activation thresholds. The gap between them is deliberate hysteresis: a
// constraint hovering at the boundary would otherwise flip between enforced and
// silent on every feedback signal, making the advisory stream look random.
const (
	activateThreshold   = 0.8 // candidate → active
	deactivateThreshold = 0.5 // active → candidate
)

// Promote increases confidence and upgrades status if threshold is met.
func (c *Constraint) Promote(delta float64) {
	c.Confidence += delta
	if c.Confidence > 1.0 {
		c.Confidence = 1.0
	}
	if c.Confidence >= activateThreshold && c.Status == StatusCandidate {
		c.Status = StatusActive
	}
	c.UsageCount++
	c.LastUsed = time.Now()
}

// Demote decreases confidence and deactivates once confidence falls below
// deactivateThreshold.
//
// The status change is the whole point. Without it a constraint the AI ignored
// repeatedly while tests stayed green keeps firing advisories forever — the
// learning signal is recorded but has no enforcement effect, which is the same
// as not learning. Deactivation is not retirement: the constraint stays a
// candidate and Promote can bring it back at activateThreshold.
func (c *Constraint) Demote(delta float64) {
	c.Confidence -= delta
	if c.Confidence < 0.0 {
		c.Confidence = 0.0
	}
	if c.Confidence < deactivateThreshold && c.Status == StatusActive {
		c.Status = StatusCandidate
	}
	c.LastUsed = time.Now()
}

// Confirm sets confidence to 1.0 and status to Active (human confirmation).
func (c *Constraint) Confirm() {
	c.Confidence = 1.0
	c.Status = StatusActive
	c.RetiredBy = ""
	c.TTL = 0 // never expires
	c.LastUsed = time.Now()
}

// Dismiss retires a constraint on human authority. Unlike decay or reconcile
// retirement this survives re-inference: the row is kept as a tombstone so the
// next pass re-deriving the same evidence does not resurrect a rule the user
// already rejected.
func (c *Constraint) Dismiss() {
	c.Status = StatusRetired
	c.RetiredBy = RetiredByUser
	c.LastUsed = time.Now()
}

// PriorityFromKind maps a constraint kind to its enforcement priority.
func PriorityFromKind(kind string) Priority {
	switch strings.ToLower(kind) {
	case "security":
		return PrioritySecurity
	case "data":
		return PriorityData
	case "architecture":
		return PriorityArchitecture
	case "quality":
		return PriorityQuality
	case "consistency":
		return PriorityConsistency
	default:
		return PriorityQuality
	}
}

// Registry manages constraints for a workspace.
type Registry struct {
	mu          sync.RWMutex
	constraints map[string]*Constraint // id -> constraint
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		constraints: make(map[string]*Constraint),
	}
}

// AddInferred registers a freshly inferred constraint with automatic
// activation promotion (autoPromote): high-confidence inference (>= 0.8)
// becomes Active immediately instead of lingering as Candidate until an
// external reconcile/teach signal arrives. Inference paths must use this
// instead of plain Add so the "evidence strength -> active constraint" chain
// stays intact.
func (r *Registry) AddInferred(c Constraint) *Constraint {
	autoPromote(&c)
	return r.Add(c)
}

// Add upserts a constraint, fail-closed on invalid input (INV-CSE-15/05).
//
// Upsert, not replace. Every re-index re-derives the same constraints from the
// same code, so a blind overwrite would reset Confidence, Status, UsageCount and
// the human-confirmed 1.0 marker on each pass — the feedback loop would keep
// writing learning signals that the next index run silently erases, and the
// confidence=1.0 guard in ReconcileRoot would never hold long enough to protect
// anything. Derivable fields (rule text, checker, priority, target) come from
// the incoming constraint; lifecycle fields are earned state and stay.
//
// A user-dismissed constraint is not resurrected by re-inference: only its
// derivable fields refresh, the tombstone holds.
//
// Returns the stored row (a copy) so callers see the merge result rather than
// their own pre-merge draft — a caller that reported its draft would claim a
// user-dismissed constraint is an active candidate. nil means the constraint was
// rejected fail-closed.
func (r *Registry) Add(c Constraint) *Constraint {
	if err := c.Validate(); err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if c.Priority == 0 {
		c.Priority = PriorityFromKind(c.Kind)
	}
	c.Root = filepath.Clean(c.Root)
	c.TargetPath = filepath.ToSlash(c.TargetPath)
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.LastUsed.IsZero() {
		c.LastUsed = c.CreatedAt
	}
	c.LastSeen = now

	if prev, ok := r.constraints[c.ID]; ok {
		c.CreatedAt = prev.CreatedAt
		c.UsageCount = prev.UsageCount
		c.LastUsed = prev.LastUsed
		// Fresh evidence wins: this pass just looked at the code, so its view of
		// the cycle or the offending sites is the current one. Only fall back to
		// the stored evidence when the incoming path does not collect any
		// (teach / L2.5 carry a predicate, not a finding) — otherwise a taught
		// constraint landing on an inferred ID would erase the inferrer's trail.
		if len(c.Evidence) == 0 {
			c.Evidence = prev.Evidence
		}
		// Learned confidence outranks the inferrer's prior. Keep the incoming
		// value only when it is a human confirmation (1.0) arriving over a
		// machine estimate.
		if prev.Confidence >= c.Confidence {
			c.Confidence = prev.Confidence
		}
		if prev.Status == StatusRetired && prev.RetiredBy == RetiredByUser {
			c.Status = StatusRetired
			c.RetiredBy = RetiredByUser
		} else {
			c.Status = statusFor(c.Confidence, prev.Status)
			c.RetiredBy = ""
		}
		if prev.TTL == 0 && prev.Confidence >= 1.0 {
			c.TTL = 0
		}
	}

	r.constraints[c.ID] = &c
	stored := c
	return &stored
}

// statusFor resolves the lifecycle status of a re-derived constraint, keeping
// the hysteresis band from flipping on re-index. A previously-active constraint
// whose confidence sits in [deactivate, activate) stays active; a candidate in
// the same band stays a candidate.
func statusFor(confidence float64, prev Status) Status {
	switch {
	case confidence >= activateThreshold:
		return StatusActive
	case confidence < deactivateThreshold:
		return StatusCandidate
	case prev == StatusActive:
		return StatusActive
	default:
		return StatusCandidate
	}
}

// Get returns a constraint by ID.
func (r *Registry) Get(id string) *Constraint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.constraints[id]
	if !ok {
		return nil
	}
	copy := *c
	return &copy
}

// Active returns all constraints with Status == Active, sorted by priority.
func (r *Registry) Active() []Constraint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Constraint
	for _, c := range r.constraints {
		if c.IsActive() {
			out = append(out, *c)
		}
	}
	return out
}

// All returns all non-retired constraints.
func (r *Registry) All() []Constraint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Constraint
	for _, c := range r.constraints {
		if c.Status != StatusRetired {
			out = append(out, *c)
		}
	}
	return out
}

// MatchingFile returns active constraints whose Root contains absPath
// and whose TargetPath matches the path relative to Root.
// Relative paths never match (INV-CSE-05). Invalid constraints never match.
func (r *Registry) MatchingFile(absPath string) []Constraint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Constraint
	for _, c := range r.constraints {
		if !c.IsActive() {
			continue
		}
		if c.MatchesFile(absPath) {
			out = append(out, *c)
		}
	}
	return out
}

// RunDecay retires constraints that have exceeded their TTL.
// Returns the number of constraints retired.
func (r *Registry) RunDecay() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, c := range r.constraints {
		if c.ShouldRetire() {
			c.Status = StatusRetired
			c.RetiredBy = RetiredByDecay
			count++
		}
	}
	return count
}

// ReconcileRoot retires machine-derived constraints owned by root that the
// inference pass starting at since did not re-derive — the evidence is gone
// (auth/ was deleted, the mutex pattern disappeared) so the constraint no
// longer describes the code.
//
// Freshness is a LastSeen timestamp rather than an ID set because inference is
// not one pass but seven independent ones (structure scan, CKG edges, AST
// patterns). Collecting IDs would require every inferrer to return its output,
// and a single failing inferrer would then look like "this whole source
// produced nothing" and wipe its constraints. sources names only the inferrers
// that actually ran, so a failure narrows the sweep instead of widening it.
//
// Scoping to root is required, not an optimization: a multi-root workspace
// re-infers one root at a time, so a global sweep would let each root's pass
// retire every other root's constraints and leave only the last root standing
// (INV-CSE-10). Human-confirmed (confidence=1.0), human-sourced and seed
// constraints are never reconciled away.
func (r *Registry) ReconcileRoot(root string, sources []string, since time.Time) int {
	root = filepath.Clean(root)
	if root == "" || root == "." || len(sources) == 0 {
		return 0
	}
	sweep := make(map[string]struct{}, len(sources))
	for _, s := range sources {
		if IsMachineSource(s) {
			sweep[s] = struct{}{}
		}
	}
	if len(sweep) == 0 {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, c := range r.constraints {
		if c.Root != root || c.Status == StatusRetired {
			continue
		}
		if _, ok := sweep[c.Source]; !ok {
			continue
		}
		if c.Confidence >= 1.0 {
			continue
		}
		if c.LastSeen.Before(since) {
			c.Status = StatusRetired
			c.RetiredBy = RetiredByReconcile
			count++
		}
	}
	return count
}

// Count returns the number of non-retired constraints.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, c := range r.constraints {
		if c.Status != StatusRetired {
			count++
		}
	}
	return count
}

// CountForRoot returns the number of non-retired constraints owned by root.
// Cold-start seeding is a per-root decision: in a multi-root workspace a
// global count is non-zero as soon as any one root is covered, which would
// leave the remaining roots with no baseline at all.
func (r *Registry) CountForRoot(root string) int {
	root = filepath.Clean(root)
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, c := range r.constraints {
		if c.Root == root && c.Status != StatusRetired {
			count++
		}
	}
	return count
}

// matchGlob does a simple glob-like match (supports * prefix/suffix).
// Empty pattern never matches (INV-CSE-05: TargetPath is required).
func matchGlob(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*/") {
		return strings.HasSuffix(path, pattern[1:])
	}
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(path, pattern[:len(pattern)-1])
	}
	if strings.Contains(pattern, "*") {
		parts := strings.SplitN(pattern, "*", 2)
		return strings.HasPrefix(path, parts[0]) && strings.HasSuffix(path, parts[1])
	}
	return strings.Contains(path, pattern)
}

// PromoteByID increases confidence of a constraint by delta.
// Used by the learning feedback loop when AI obeys a constraint and tests pass.
// Returns the updated row (a copy), or nil when no constraint carries that ID —
// IDs are root-scoped by Bind, so an unscoped ID from an ID-builder never
// matches and a silent no-op would look like a successful promotion.
func (r *Registry) PromoteByID(id string, delta float64) *Constraint {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.constraints[id]
	if !ok {
		return nil
	}
	c.Promote(delta)
	updated := *c
	return &updated
}

// DemoteByID decreases confidence of a constraint by delta.
// Used when AI ignores a constraint and tests still pass.
func (r *Registry) DemoteByID(id string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.constraints[id]; ok {
		c.Demote(delta)
	}
}

// PromoteAll promotes multiple constraints at once (batch operation after
// a successful Run where the AI obeyed the warned constraints).
func (r *Registry) PromoteAll(ids []string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if c, ok := r.constraints[id]; ok {
			c.Promote(delta)
		}
	}
}

// DemoteAll demotes multiple constraints (AI ignored them but code still works).
func (r *Registry) DemoteAll(ids []string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if c, ok := r.constraints[id]; ok {
			c.Demote(delta)
		}
	}
}

// DismissByID records a human rejection of a constraint. Distinct from decay
// and reconcile retirement: the next inference pass re-derives the same
// constraint from the same unchanged code, so without a durable tombstone the
// dismissal would be undone within minutes.
func (r *Registry) DismissByID(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.constraints[id]; ok {
		c.Dismiss()
		return true
	}
	return false
}

// ConfirmByID marks a constraint as human-confirmed (confidence=1.0, never expires).
func (r *Registry) ConfirmByID(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.constraints[id]; ok {
		c.Confirm()
		return true
	}
	return false
}

// DetectConflicts finds pairs of active constraints that may conflict
// (same scope, different rules at different priority levels).
func (r *Registry) DetectConflicts() []ConflictPair {
	active := r.Active()
	var conflicts []ConflictPair
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			a, b := active[i], active[j]
			if a.Priority != b.Priority && targetOverlaps(a, b) {
				if RulesContradict(a.Rule, b.Rule) {
					higher, lower := a, b
					if b.Priority < a.Priority {
						higher, lower = b, a
					}
					conflicts = append(conflicts, ConflictPair{
						Higher: higher,
						Lower:  lower,
					})
				}
			}
		}
	}
	return conflicts
}

// ConflictPair records two constraints that may conflict.
type ConflictPair struct {
	Higher Constraint // higher priority (lower number)
	Lower  Constraint // lower priority (higher number)
}

func (cp ConflictPair) String() string {
	return fmt.Sprintf("conflict: [%s p%d] %q vs [%s p%d] %q — higher priority wins unless explicitly overridden",
		cp.Higher.Kind, cp.Higher.Priority, cp.Higher.Rule,
		cp.Lower.Kind, cp.Lower.Priority, cp.Lower.Rule)
}

func targetOverlaps(a, b Constraint) bool {
	if a.Root == "" || b.Root == "" || filepath.Clean(a.Root) != filepath.Clean(b.Root) {
		return false
	}
	return a.TargetPath == b.TargetPath ||
		strings.HasPrefix(a.TargetPath, b.TargetPath) ||
		strings.HasPrefix(b.TargetPath, a.TargetPath)
}

// RulesContradict is a heuristic check for contradictory rules.
// Looks for opposing verbs (must vs must not, allow vs deny).
func RulesContradict(a, b string) bool {
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)

	contradictions := [][2]string{
		{"must not", "must"},
		{"禁止", "必须"},
		{"不允许", "允许"},
		{"deny", "allow"},
		{"avoid", "prefer"},
	}
	for _, pair := range contradictions {
		if (strings.Contains(aLower, pair[0]) && strings.Contains(bLower, pair[1])) ||
			(strings.Contains(aLower, pair[1]) && strings.Contains(bLower, pair[0])) {
			return true
		}
	}
	return false
}

// ── Readiness ───────────────────────────────────────────────────────────────

// ConstraintReadiness reports how ready the constraint set is for consumption.
type ConstraintReadiness struct {
	Source       string  `json:"source"`       // "constraint"
	Total        int     `json:"total"`        // non-retired constraints
	Active       int     `json:"active"`       // active constraints
	Inferred     int     `json:"inferred"`     // source=inferred
	Completeness float64 `json:"completeness"` // active / total (1.0 if total=0)
}

// Readiness returns the constraint registry's readiness state.
func (r *Registry) Readiness() ConstraintReadiness {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	active := 0
	inferred := 0
	for _, c := range r.constraints {
		if c.Status == StatusRetired {
			continue
		}
		total++
		if c.IsActive() {
			active++
		}
		if IsMachineSource(c.Source) {
			inferred++
		}
	}

	completeness := 1.0
	if total > 0 {
		completeness = float64(active) / float64(total)
	}

	return ConstraintReadiness{
		Source:       "constraint",
		Total:        total,
		Active:       active,
		Inferred:     inferred,
		Completeness: completeness,
	}
}

// ── Persistence (code.db) ────────────────────────────────────────────────────

// SaveToSQLite writes the registry to the code.db constraints table.
//
// Retirement is not one state but two, and they persist differently:
//
//   - RetiredByUser is a human verdict on a constraint the code still supports.
//     The next inference pass will re-derive it from the same unchanged
//     evidence, so the row is kept as a tombstone; Add reads it back and
//     refuses the resurrection.
//   - RetiredByDecay / RetiredByReconcile mean the evidence itself is gone. The
//     row is deleted: if the pattern ever returns the constraint should return
//     with it, and a persistent tombstone would silently suppress it forever.
//
// Copies the snapshot under RLock, then releases the lock before doing I/O to
// avoid blocking Add/Promote/Demote.
func (r *Registry) SaveToSQLite(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("constraints: db is nil")
	}

	r.mu.RLock()
	keep := make([]Constraint, 0, len(r.constraints))
	var drop []string
	for _, c := range r.constraints {
		if c.Status == StatusRetired && c.RetiredBy != RetiredByUser {
			drop = append(drop, c.ID)
			continue
		}
		keep = append(keep, *c)
	}
	r.mu.RUnlock()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("constraints: begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, id := range drop {
		if _, err := tx.Exec(`DELETE FROM constraints WHERE id = ?`, id); err != nil {
			return fmt.Errorf("constraints: delete %s: %w", id, err)
		}
	}

	for _, c := range keep {
		dataJSON, _ := json.Marshal(c)
		_, err := tx.Exec(`INSERT OR REPLACE INTO constraints
			(id, rule, kind, priority, status, confidence, source, scope, ttl_days,
			 usage_count, last_used, created_at, data_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.Rule, c.Kind, int(c.Priority), string(c.Status), c.Confidence,
			c.Source, c.TargetPath, c.TTL,
			c.UsageCount, c.LastUsed.Unix(), c.CreatedAt.Unix(), string(dataJSON),
		)
		if err != nil {
			return fmt.Errorf("constraints: upsert %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

// LoadFromSQLite restores constraints from the code.db constraints table.
//
// Loads retired rows too: the only rows that survive a save are user
// tombstones, and dropping them here would make every dismissal last exactly
// one process lifetime.
//
// Rows from retired inference paths (agents_md, convention mining) are deleted
// rather than skipped — the natural-language era of CSE had no Checker, so
// those constraints can never be evaluated and would sit in the table forever.
func (r *Registry) LoadFromSQLite(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("constraints: db is nil")
	}

	for _, src := range legacySources {
		if _, err := db.Exec(`DELETE FROM constraints WHERE source = ?`, src); err != nil {
			return fmt.Errorf("constraints: purge %s: %w", src, err)
		}
	}

	rows, err := db.Query(`SELECT data_json FROM constraints`)
	if err != nil {
		return fmt.Errorf("constraints: query: %w", err)
	}
	defer rows.Close()

	r.mu.Lock()
	defer r.mu.Unlock()
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			continue
		}
		var c Constraint
		if err := json.Unmarshal([]byte(dataJSON), &c); err != nil {
			continue
		}
		// Validate is the same gate Add enforces: a persisted row with no
		// Checker or no root scope predates the current contract and can never
		// produce a verdict.
		if c.ID == "" || c.Validate() != nil {
			continue
		}
		r.constraints[c.ID] = &c
	}
	return rows.Err()
}
