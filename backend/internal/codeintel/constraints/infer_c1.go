package constraints

import (
	"database/sql"
	"fmt"
	"math"
)

// C1Confidence scales C1 constraint confidence with evidence strength
// (caller count): more callers -> the signature-immutability claim is
// better supported. 1000+ callers reaches the 0.8 activation threshold;
// 5 callers stays candidate until boosted by reconcile signals.
// Exported so tests exercise the real implementation, not a mirror copy.
func C1Confidence(callerCount int) float64 {
	return math.Min(0.9, 0.5+math.Log10(float64(callerCount))*0.1)
}

// InferTypeConstraints (C1) scans the CKG for functions with high caller
// counts and pins their parameter arity via the signature_stable checker.
// Symbols whose recorded signature cannot be parsed into an arity are skipped:
// no checker means no constraint (INV-CSE-15).
//
// This subsumes the former C7 "API compatibility" pass, which pinned the same
// high-fan-in symbols with a flat 0.6 confidence.
//
// Returns the number of constraints registered.
func InferTypeConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if db == nil || reg == nil {
		return 0, nil
	}
	root := absRoot(workDir)

	rows, err := db.Query(`
		SELECT s.name, s.file_path, s.signature, COUNT(e.id) AS caller_count
		FROM symbols s
		JOIN edges e ON e.target_id = s.id
		WHERE e.kind = 'call'
		  AND s.signature IS NOT NULL AND s.signature != ''
		  AND s.kind IN ('function', 'method')
		GROUP BY s.id, s.name, s.file_path, s.signature
		HAVING caller_count >= 5
		ORDER BY caller_count DESC`)
	if err != nil {
		return 0, fmt.Errorf("query type constraints: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name, filePath, signature string
		var callerCount int
		if rows.Scan(&name, &filePath, &signature, &callerCount) != nil {
			continue
		}

		if _, ok := arityFromSignature(signature); !ok {
			continue
		}
		relPath := RelToRoot(root, filePath)
		if relPath == "" {
			continue
		}
		id := inferID("ckg-c1", name+"-"+relPath)
		rule := fmt.Sprintf(
			"Function %q (%s) has %d callers — its type signature %q must not change without updating all call sites.",
			name, relPath, callerCount, signature,
		)

		// Confidence scales with evidence strength: more callers → the
		// signature-immutability claim is better supported. A 1000+-caller
		// function reaches the 0.8 activation threshold directly, while a
		// 5-caller function stays candidate until boosted by reconcile
		// signals (regression/test-pass). Previously the fixed 0.6 made the
		// strongest CKG-edge evidence indistinguishable from the weakest.
		confidence := C1Confidence(callerCount)
		reg.AddInferred(Constraint{
			ID:         id,
			Rule:       rule,
			Kind:       "architecture",
			Priority:   PriorityArchitecture,
			Status:     StatusCandidate,
			Confidence: confidence,
			Source:     SourceInferredCKG,
			TTL:        90,
		}.Bind(root, TargetFunction, relPath, CheckerSpec{
			Kind:      CheckSignatureStable,
			Symbol:    name,
			Signature: signature,
		}))
		count++
	}
	return count, rows.Err()
}
