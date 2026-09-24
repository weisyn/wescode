package constraints

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// InferPerformanceConstraints (C9) detects potential N+1 query patterns:
// function calls inside for/range loops that likely perform I/O operations
// (database queries, HTTP requests, file reads).
//
// One constraint per file, bound to CheckNPlusOne so PreWrite can re-evaluate
// the edited content instead of replaying a stale line number (INV-CSE-15).
//
// db is accepted for signature uniformity but not used (pure AST analysis).
// Returns the number of constraints registered.
func InferPerformanceConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if reg == nil {
		return 0, nil
	}

	root := absRoot(workDir)
	goMod := filepath.Join(workDir, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
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

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}

		relPath := RelToRoot(root, path)
		if relPath == "" {
			return nil
		}
		sites := detectLoopIO(f, fset, relPath)
		if len(sites) == 0 {
			return nil
		}

		reg.AddInferred(Constraint{
			ID:         inferID("pattern-c9", relPath),
			Rule:       fmt.Sprintf("Potential N+1 problem in %s: I/O calls occur inside loops. Batch the operation instead of querying per iteration.", relPath),
			Kind:       "quality",
			Priority:   PriorityQuality,
			Status:     StatusCandidate,
			Confidence: 0.6,
			Source:     SourceInferredPattern,
			TTL:        120,
			Evidence:   sites,
		}.AtFile(root, relPath, CheckNPlusOne))
		count++
		return nil
	})

	return count, err
}

// ioIndicators are receiver/function name fragments that typically indicate I/O.
var ioIndicators = []string{
	"Query", "QueryRow", "Exec", "ExecContext", "QueryContext",
	"Get", "Post", "Put", "Delete", "Do", "Send",
	"Open", "Create", "ReadFile", "WriteFile",
	"Fetch", "Request", "Dial",
}

// detectLoopIO returns "relPath:line" for every I/O call found inside a loop.
func detectLoopIO(f *ast.File, fset *token.FileSet, relPath string) []string {
	var sites []string

	ast.Inspect(f, func(n ast.Node) bool {
		var body *ast.BlockStmt

		switch stmt := n.(type) {
		case *ast.ForStmt:
			body = stmt.Body
		case *ast.RangeStmt:
			body = stmt.Body
		default:
			return true
		}

		if body == nil {
			return true
		}

		ast.Inspect(body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			funcName := qualifiedCallName(call)
			if funcName == "" || !isIOCall(funcName) {
				return true
			}
			sites = append(sites, fmt.Sprintf("%s:%d", relPath, fset.Position(call.Pos()).Line))
			return true
		})
		return true
	})

	return sites
}

func isIOCall(name string) bool {
	for _, indicator := range ioIndicators {
		if strings.Contains(name, indicator) {
			return true
		}
	}
	return false
}
