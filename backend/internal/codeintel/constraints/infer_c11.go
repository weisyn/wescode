package constraints

import (
	"database/sql"
	"fmt"
	"strings"
)

// InferCompatibilityConstraints (C11) pins the parameter arity of exported
// functions in non-internal packages: those are the public API, so a signature
// change is a breaking change regardless of whether callers are visible in this
// repository (which is what separates C11 from C1's fan-in threshold).
//
// Exported struct/interface stability used to be inferred here too. It had no
// predicate — "removing or renaming fields is breaking" cannot be evaluated
// against an edited file — so it is Knowledge, not a constraint (INV-CSE-17).
//
// Returns the number of constraints registered.
func InferCompatibilityConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if db == nil || reg == nil {
		return 0, nil
	}
	root := absRoot(workDir)

	rows, err := db.Query(`
		SELECT s.name, s.file_path, s.signature, s.package_path
		FROM symbols s
		WHERE s.exported = 1
		  AND s.kind IN ('function', 'method')
		  AND s.signature IS NOT NULL AND s.signature != ''
		  AND s.package_path NOT LIKE '%/internal/%'
		ORDER BY s.name`)
	if err != nil {
		return 0, fmt.Errorf("C11 exported signatures: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name, filePath, signature, pkgPath string
		if rows.Scan(&name, &filePath, &signature, &pkgPath) != nil {
			continue
		}
		if _, ok := arityFromSignature(signature); !ok {
			continue
		}
		relPath := RelToRoot(root, filePath)
		if relPath == "" {
			continue
		}

		reg.AddInferred(Constraint{
			ID: inferID("ckg-c11", pkgPath+"."+name),
			Rule: fmt.Sprintf(
				"Exported function %s.%s (signature: %s) is part of the public API — changing its signature is a breaking change.",
				shortPkg(pkgPath), name, signature,
			),
			Kind:       "architecture",
			Priority:   PriorityArchitecture,
			Status:     StatusCandidate,
			Confidence: 0.7,
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

func shortPkg(pkgPath string) string {
	parts := strings.Split(pkgPath, "/")
	if len(parts) == 0 {
		return pkgPath
	}
	return parts[len(parts)-1]
}
