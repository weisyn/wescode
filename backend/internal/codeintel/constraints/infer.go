package constraints

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
)

// InferFromProject scans the project at workDir and infers constraints that
// have an executable Checker (INV-CSE-15). Categories without a Checker
// (naming, layering prose, AGENTS.md rules) are not constraints — they are
// documentation and belong in Knowledge (INV-CSE-17).
//
// ckgReadiness controls which categories are inferred:
//   - < 0.3: security only (pure filesystem)
//   - >= 0.3: security + context propagation + no-panic
func InferFromProject(workDir string, ckgReadiness float64) []Constraint {
	var out []Constraint
	root := absRoot(workDir)

	out = append(out, inferSecurity(root)...)
	if ckgReadiness >= 0.3 {
		out = append(out, inferContextPropagation(root)...)
		out = append(out, inferNoPanic(root)...)
	}

	for i := range out {
		autoPromote(&out[i])
	}
	return out
}

// autoPromote upgrades a freshly inferred constraint to Active when it
// meets automatic activation criteria, avoiding the "all Candidate → never
// triggers" dead state.
func autoPromote(c *Constraint) {
	if c.Confidence >= 0.8 {
		c.Status = StatusActive
		return
	}
	if c.Priority <= PriorityData && c.Confidence >= 0.7 {
		c.Status = StatusActive
	}
}

func inferID(prefix, content string) string {
	h := sha256.Sum256([]byte(content))
	return prefix + "-" + fmt.Sprintf("%x", h[:4])
}

// LearnedID generates a deterministic ID for a learned constraint.
func LearnedID(prefix, content string) string {
	return inferID("learned-"+prefix, content)
}

func inferSecurity(workDir string) []Constraint {
	var out []Constraint
	root := absRoot(workDir)

	authDirs := []string{"auth", "authentication", "user", "users", "account"}
	for _, dir := range authDirs {
		candidates := []string{
			filepath.Join(workDir, dir),
			filepath.Join(workDir, "internal", dir),
			filepath.Join(workDir, "pkg", dir),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				out = append(out, Constraint{
					ID:         inferID("sec", "password-hashing-"+dir),
					Rule:       "Passwords must be hashed (bcrypt/argon2/scrypt) before storage. Never store plaintext passwords.",
					Kind:       "security",
					Priority:   PrioritySecurity,
					Status:     StatusCandidate,
					Confidence: 0.7,
					Source:     SourceInferred,
					TTL:        0,
				}.AtTreeGo(root, CheckPlaintextSecret))
				break
			}
		}
	}
	return out
}

func inferContextPropagation(workDir string) []Constraint {
	root := absRoot(workDir)
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return nil
	}

	withCtx, withoutCtx := countContextParams(workDir)
	total := withCtx + withoutCtx
	if total < 10 {
		return nil
	}

	ratio := float64(withCtx) / float64(total)
	if ratio < 0.6 {
		return nil
	}

	conf := 0.5 + (ratio-0.6)*0.75
	if conf > 0.85 {
		conf = 0.85
	}

	return []Constraint{Constraint{
		ID:         inferID("pattern", "context-propagation"),
		Rule:       "Public functions and methods should accept context.Context as the first parameter for cancellation and deadline propagation.",
		Kind:       "consistency",
		Priority:   PriorityQuality,
		Status:     StatusCandidate,
		Confidence: conf,
		Source:     SourceInferred,
		TTL:        120,
	}.AtTreeGo(root, CheckContextFirst)}
}

// inferNoPanic constrains production code against panic(). The checker skips
// _test.go and init() so the rule text and the predicate agree.
func inferNoPanic(workDir string) []Constraint {
	root := absRoot(workDir)
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return nil
	}

	return []Constraint{Constraint{
		ID:         inferID("pattern", "no-panic-production"),
		Rule:       "Do not use panic() in production code. Return errors instead. panic() is only acceptable in init() functions and test helpers.",
		Kind:       "quality",
		Priority:   PriorityQuality,
		Status:     StatusCandidate,
		Confidence: 0.75,
		Source:     SourceInferred,
		TTL:        120,
	}.AtTreeGo(root, CheckNoPanic)}
}

func countContextParams(workDir string) (int, int) {
	var withCtx, withoutCtx int
	filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && IsSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "func ") {
				continue
			}
			// Only count exported functions (starts with uppercase after "func " or "func (...)").
			parenIdx := strings.Index(trimmed, "(")
			if parenIdx < 0 {
				continue
			}
			nameStart := 5 // len("func ")
			if trimmed[nameStart] == '(' {
				// Method: find the name after receiver
				closeP := strings.Index(trimmed[nameStart:], ")")
				if closeP < 0 {
					continue
				}
				nameStart = nameStart + closeP + 2
			}
			if nameStart >= len(trimmed) {
				continue
			}
			if trimmed[nameStart] < 'A' || trimmed[nameStart] > 'Z' {
				continue
			}
			if strings.Contains(trimmed, "ctx context.Context") || strings.Contains(trimmed, "ctx context.") {
				withCtx++
			} else {
				withoutCtx++
			}
		}
		return nil
	})
	return withCtx, withoutCtx
}

// IsSkipDir checks if a directory name should be excluded from inference scanning.
// Delegates to the unified boundary.Classifier.ShouldSkipDir (safety-core + always-skip).
func IsSkipDir(name string) bool {
	c := boundary.NewClassifier(boundary.ModeFast, nil)
	skip, _ := c.ShouldSkipDir(name, name)
	return skip
}

// InferFromCKG infers constraints from code.db edges/symbols and registers
// them into the Registry. Only C6 (circular imports, Tarjan SCC →
// import_cycle checker) survives here.
//
// C2 (implements), C7 (signature stability — superseded by C1
// InferTypeConstraints, which pins the same high-fan-in signatures with
// evidence-scaled confidence), C12 (layer prose) and the legacy handler→repo /
// large-interface detections were removed: they produce natural-language
// advice with no predicate, so they are Knowledge, not constraints
// (INV-CSE-17).
//
// Returns the number of constraints registered. db==nil returns (0, nil).
func InferFromCKG(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if db == nil || reg == nil {
		return 0, nil
	}

	n, err := inferDependencyDirectionCKG(db, reg, workDir)
	if err != nil {
		return n, fmt.Errorf("C6 dependency direction: %w", err)
	}
	return n, nil
}

// inferDependencyDirectionCKG (C6) builds a directed graph from IMPORTS edges
// and runs Tarjan's SCC algorithm. Each SCC with size > 1 = circular dependency.
// The checker fires only on files whose package is part of the cycle.
func inferDependencyDirectionCKG(db *sql.DB, reg *Registry, workDir string) (int, error) {
	root := absRoot(workDir)
	rows, err := db.Query(`
		SELECT DISTINCT src.package_path, e.target_name
		FROM edges e
		JOIN symbols src ON e.source_id = src.id
		WHERE e.kind = 'import'
		  AND src.package_path IS NOT NULL
		  AND e.target_name IS NOT NULL
		  AND src.package_path != e.target_name`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	graph := map[string][]string{}
	nodes := map[string]bool{}
	for rows.Next() {
		var from, to string
		if rows.Scan(&from, &to) == nil {
			graph[from] = append(graph[from], to)
			nodes[from] = true
			nodes[to] = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sccs := tarjanSCC(graph, nodes)

	count := 0
	for _, scc := range sccs {
		if len(scc) <= 1 {
			continue
		}
		cycle := strings.Join(scc, " → ")
		id := inferID("ckg-c6", cycle)
		rule := fmt.Sprintf("Circular dependency detected: %s. Break the cycle by extracting shared types or introducing an interface.", cycle)
		reg.AddInferred(Constraint{
			ID:         id,
			Rule:       rule,
			Kind:       "architecture",
			Priority:   PriorityArchitecture,
			Status:     StatusCandidate,
			Confidence: 0.7,
			Source:     SourceInferredCKG,
			TTL:        90,
			Evidence:   scc,
		}.Bind(root, TargetTree, "**/*.go", CheckerSpec{
			Kind:      CheckImportCycle,
			Forbidden: scc,
		}))
		count++
	}
	return count, nil
}

// tarjanSCC implements Tarjan's strongly connected components algorithm.
func tarjanSCC(graph map[string][]string, nodes map[string]bool) [][]string {
	var (
		index    int
		stack    []string
		onStack  = map[string]bool{}
		indices  = map[string]int{}
		lowlinks = map[string]int{}
		result   [][]string
	)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range graph[v] {
			if _, visited := indices[w]; !visited {
				strongConnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}

		if lowlinks[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			result = append(result, scc)
		}
	}

	for node := range nodes {
		if _, visited := indices[node]; !visited {
			strongConnect(node)
		}
	}
	return result
}

// RelToRoot converts a CKG file_path (absolute or already relative) into a
// path relative to root. Returns "" when the file lies outside root.
func RelToRoot(root, filePath string) string {
	if filePath == "" {
		return ""
	}
	if !filepath.IsAbs(filePath) {
		return filepath.ToSlash(filepath.Clean(filePath))
	}
	rel, err := filepath.Rel(root, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}
