package constraints

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InferSecurityConstraints (C10) registers one constraint per file for the two
// security classes that have an executable predicate:
//   - SQL built by fmt.Sprintf / string concatenation → sql_concat
//   - secret-named variables assigned string literals → plaintext_secret
//
// Detection reuses the same checker the constraint is bound to, so a PreWrite
// re-check evaluates the edited content instead of replaying a stale line
// number (INV-CSE-15).
//
// db is accepted for signature uniformity but not used (pure AST analysis).
// Returns the number of constraints registered.
func InferSecurityConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if reg == nil {
		return 0, nil
	}

	root := absRoot(workDir)
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return 0, nil
	}

	count := 0
	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && IsSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relPath := RelToRoot(root, path)
		if relPath == "" {
			return nil
		}

		if failed, msg := checkSQLConcat(path, ""); failed {
			reg.AddInferred(Constraint{
				ID:         inferID("pattern-c10-sqli", relPath),
				Rule:       fmt.Sprintf("SQL injection risk in %s: queries are assembled by string formatting or concatenation. Use parameterized queries ($1 / ?) instead.", relPath),
				Kind:       "security",
				Priority:   PrioritySecurity,
				Status:     StatusCandidate,
				Confidence: 0.85,
				Source:     SourceInferredPattern,
				TTL:        0,
				Evidence:   []string{msg},
			}.AtFile(root, relPath, CheckSQLConcat))
			count++
		}

		if failed, msg := checkPlaintextSecret(path, ""); failed {
			reg.AddInferred(Constraint{
				ID:         inferID("pattern-c10-secret", relPath),
				Rule:       fmt.Sprintf("Hardcoded secret in %s: a credential-named variable is assigned a string literal. Read it from the environment or a secret manager.", relPath),
				Kind:       "security",
				Priority:   PrioritySecurity,
				Status:     StatusCandidate,
				Confidence: 0.85,
				Source:     SourceInferredPattern,
				TTL:        0,
				Evidence:   []string{msg},
			}.AtFile(root, relPath, CheckPlaintextSecret))
			count++
		}
		return nil
	})

	return count, err
}
