package constraints

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InferStateMachineConstraints (C8) finds enum-like typed const groups that are
// dispatched on in switch statements, and pins each switch site's file to a
// state_machine checker carrying the full value set.
//
// The constraint is per switch-site file, not per type: the checker's question
// is "does the switch in the file being edited still cover every value", which
// can only be answered against a concrete file. A type-level rule ("adding a
// value requires updating all switches") has nothing to evaluate at edit time
// and would be Knowledge, not a constraint (INV-CSE-17).
//
// db is accepted for signature uniformity but not used (pure AST analysis).
// Returns the number of constraints registered.
func InferStateMachineConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if reg == nil {
		return 0, nil
	}
	root := absRoot(workDir)
	if root == "" {
		return 0, nil
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return 0, nil
	}

	// Pass 1: collect typed const groups (enum-like patterns) across the tree,
	// because a switch site rarely lives in the file that declares the values.
	enumTypes := map[string][]string{} // typeName → []constName
	var goFiles []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IsSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		goFiles = append(goFiles, path)

		f, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil
		}
		collectEnumConsts(f, enumTypes)
		return nil
	})
	if err != nil {
		return 0, err
	}

	// Pass 2: per file, record which enum types it dispatches on.
	type site struct {
		relPath  string
		typeName string
	}
	var sites []site
	for _, path := range goFiles {
		f, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			continue
		}
		rel := RelToRoot(root, path)
		if rel == "" {
			continue
		}
		for _, typeName := range switchedEnumTypes(f, enumTypes) {
			sites = append(sites, site{relPath: rel, typeName: typeName})
		}
	}

	count := 0
	for _, s := range sites {
		values := enumTypes[s.typeName]
		if len(values) < 3 {
			continue
		}
		sorted := append([]string(nil), values...)
		sort.Strings(sorted)

		c := Constraint{
			ID: inferID("pattern-c8", s.typeName+":"+s.relPath),
			Rule: fmt.Sprintf(
				"Keep the switch over %s in %s exhaustive — it covers all %d values and has no default.",
				s.typeName, s.relPath, len(sorted),
			),
			Kind:       "quality",
			Priority:   PriorityQuality,
			Status:     StatusCandidate,
			Confidence: 0.55,
			Source:     SourceInferredPattern,
			TTL:        120,
		}.Bind(root, TargetFunction, s.relPath, CheckerSpec{
			Kind:      CheckStateMachine,
			Symbol:    s.typeName,
			Forbidden: sorted,
		})
		reg.AddInferred(c)
		count++
	}
	return count, nil
}

// collectEnumConsts finds const blocks with a shared type (iota pattern).
func collectEnumConsts(f *ast.File, enumTypes map[string][]string) {
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}

		var blockType string
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if ident, ok := vs.Type.(*ast.Ident); ok {
					blockType = ident.Name
				}
			}
			if blockType == "" {
				continue
			}
			for _, name := range vs.Names {
				if name.Name == "_" {
					continue
				}
				enumTypes[blockType] = append(enumTypes[blockType], name.Name)
			}
		}
	}
}

// switchedEnumTypes returns the enum types this file dispatches on exhaustively:
// a switch with no default that covers every value of the type. Only those are
// worth pinning — a switch that already omits values, or delegates the rest to
// default, has nothing for the checker to protect.
func switchedEnumTypes(f *ast.File, enumTypes map[string][]string) []string {
	constToType := map[string]string{}
	for typeName, consts := range enumTypes {
		for _, c := range consts {
			constToType[c] = typeName
		}
	}

	found := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Body == nil {
			return true
		}
		covered := map[string]map[string]bool{}
		hasDefault := false
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			if cc.List == nil {
				hasDefault = true
				continue
			}
			for _, expr := range cc.List {
				ident, ok := expr.(*ast.Ident)
				if !ok {
					continue
				}
				if typeName, isEnum := constToType[ident.Name]; isEnum {
					if covered[typeName] == nil {
						covered[typeName] = map[string]bool{}
					}
					covered[typeName][ident.Name] = true
				}
			}
		}
		if hasDefault {
			return true
		}
		for typeName, values := range covered {
			if len(values) == len(enumTypes[typeName]) && len(values) >= 2 {
				found[typeName] = true
			}
		}
		return true
	})
	return sortedKeys(found)
}
