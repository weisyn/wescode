package notify

// Generation of the TypeScript half of the notification contract.
//
// Method's field is unexported, so every Method value in the program is
// constructed in this package. Parsing this package's own source is therefore
// complete by construction — there is no "did we remember to list it" step,
// which is the property that makes this a derivation rather than a second copy.
//
// The emitted union is consumed by an exhaustive switch in the Electron main
// process, so a method added here and not handled there fails tsc. The staleness
// check lives in tsgen_test.go and runs in `make vet`.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// methodTypeName is the type whose composite literals this generator collects.
// Renaming the type without renaming it here makes the walk find nothing, which
// tsgen_test.go turns into a failure rather than an empty union.
const methodTypeName = "Method"

// generatedTSPath is where the emitted union lives, relative to the repo root.
const generatedTSPath = "editor/src/vs/platform/wescode/common/wescodeNotifications.ts"

// Entry is one notification method as declared in this package.
type Entry struct {
	// GoName is the exported identifier, e.g. "IndexComplete".
	GoName string
	// Wire is the JSON-RPC method name, e.g. "codeintel/index-complete".
	Wire string
	// Summary is the first line of the Go doc comment, with a leading "GoName "
	// stripped, or empty when undocumented.
	Summary string
}

// packageDir returns this package's source directory.
//
// Derived from this file's own compile-time path rather than the working
// directory, so the gate behaves the same from `go test ./...` at the module
// root and from an editor running one test.
func packageDir() (string, error) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("notify: runtime.Caller failed; cannot locate package source")
	}
	return filepath.Dir(self), nil
}

// RepoRoot returns the wescode checkout root, verified by layout.
//
// The verification is the point: if the package moves, callers get a named
// error instead of writing a generated file into a path that no build reads.
func RepoRoot() (string, error) {
	dir, err := packageDir()
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(dir, "..", "..", ".."))
	for _, want := range []string{
		filepath.Join("backend", "go.mod"),
		filepath.Join("editor", "src", "vs", "platform", "wescode", "common"),
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			return "", fmt.Errorf("notify: %q does not look like the wescode root (missing %s): %w",
				root, want, err)
		}
	}
	return root, nil
}

// GeneratedTSFile returns the absolute path of the emitted TypeScript file.
func GeneratedTSFile() (string, error) {
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(generatedTSPath)), nil
}

// Methods parses this package and returns every declared notification method,
// ordered by wire name.
//
// Malformed declarations are errors, not skips. A `Method{someVar}` that the
// walk quietly ignored would be a method the backend can send and TypeScript
// has never heard of — the failure this whole package exists to prevent, with
// the generator as the new place it hides.
func Methods() ([]Entry, error) {
	dir, err := packageDir()
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("notify: parse %s: %w", dir, err)
	}

	var (
		entries []Entry
		files   []string
	)
	for _, pkg := range pkgs {
		for name := range pkg.Files {
			files = append(files, name)
		}
	}
	sort.Strings(files) // deterministic error order across runs

	for _, name := range files {
		var file *ast.File
		for _, pkg := range pkgs {
			if f, ok := pkg.Files[name]; ok {
				file = f
				break
			}
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				entry, isMethod, err := parseValueSpec(fset, gd, vs)
				if err != nil {
					return nil, err
				}
				if isMethod {
					entries = append(entries, entry)
				}
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Wire < entries[j].Wire })

	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		if prev, dup := seen[e.Wire]; dup {
			return nil, fmt.Errorf("notify: %s and %s both declare wire name %q; "+
				"two identifiers for one notification means one of them has no listener",
				prev, e.GoName, e.Wire)
		}
		seen[e.Wire] = e.GoName
	}
	return entries, nil
}

// parseValueSpec classifies one `var` spec. It returns isMethod=false for vars
// of other types, and an error for anything that constructs a Method in a shape
// this generator cannot read.
func parseValueSpec(fset *token.FileSet, gd *ast.GenDecl, vs *ast.ValueSpec) (Entry, bool, error) {
	if len(vs.Values) != 1 || len(vs.Names) != 1 {
		return Entry{}, false, nil
	}
	lit, ok := vs.Values[0].(*ast.CompositeLit)
	if !ok {
		return Entry{}, false, nil
	}
	ident, ok := lit.Type.(*ast.Ident)
	if !ok || ident.Name != methodTypeName {
		return Entry{}, false, nil
	}

	pos := fset.Position(vs.Pos())
	name := vs.Names[0].Name

	if !ast.IsExported(name) {
		return Entry{}, false, fmt.Errorf("%s: %s is an unexported %s; "+
			"call sites outside this package could not reference it, so it would be a "+
			"notification the backend can never send", pos, name, methodTypeName)
	}
	if len(lit.Elts) != 1 {
		return Entry{}, false, fmt.Errorf("%s: %s has %d fields, want exactly the wire name",
			pos, name, len(lit.Elts))
	}
	bl, ok := lit.Elts[0].(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return Entry{}, false, fmt.Errorf("%s: %s's wire name is not a string literal; "+
			"this generator reads source, so a computed name cannot reach TypeScript", pos, name)
	}
	wire, err := strconv.Unquote(bl.Value)
	if err != nil {
		return Entry{}, false, fmt.Errorf("%s: %s: unquote %s: %w", pos, name, bl.Value, err)
	}
	if strings.TrimSpace(wire) == "" || wire != strings.TrimSpace(wire) {
		return Entry{}, false, fmt.Errorf("%s: %s has wire name %q; "+
			"an empty or space-padded name cannot match on the wire", pos, name, wire)
	}

	// A single-spec `var` block carries its doc on the GenDecl; a grouped one
	// carries it on the spec.
	doc := vs.Doc
	if doc == nil && len(gd.Specs) == 1 {
		doc = gd.Doc
	}
	return Entry{GoName: name, Wire: wire, Summary: summarize(doc, name)}, true, nil
}

// summarize returns the first sentence of doc with a leading "<name> " removed.
//
// The first sentence, not the first line: Go wraps at 80 columns, so a line is
// an arbitrary cut. Emitting it verbatim produced comments that stopped
// mid-clause ("pushes IM channel login state (QR payload, scanned,"), which
// reads as a truncation bug in the generator.
//
// Only the first sentence, though — the full comment stays in Go. Copying the
// paragraph here would make two texts to keep true, and this is the copy nobody
// edits.
func summarize(doc *ast.CommentGroup, name string) string {
	if doc == nil {
		return ""
	}

	var lines []string
	for _, c := range doc.List {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), "/*"))
		if line == "" {
			break // blank line ends the lead paragraph
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}

	para := strings.TrimSpace(strings.TrimPrefix(strings.Join(lines, " "), name))
	if end := firstSentenceEnd(para); end > 0 {
		return para[:end]
	}
	return para
}

// firstSentenceEnd returns the index just past the first sentence-ending period
// in s, or 0 when there is none.
//
// The rule is on the word, not on the punctuation: a period ends a sentence when
// the word it closes is not itself abbreviation-shaped. Abbreviation-shaped means
// it contains an interior period ("e.g.", "i.e.") or is a single character ("A.",
// initials). This is a rule rather than a list because a list of abbreviations
// would be a second thing to maintain that fails the same way as the method
// names it lives next to — silently, by omission.
func firstSentenceEnd(s string) int {
	wordStart := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			wordStart = i + 1
		case '.':
			if i+1 < len(s) && s[i+1] != ' ' {
				continue // mid-word period: "0.9", "e.g", a path
			}
			if word := s[wordStart : i+1]; !isAbbreviation(word) {
				return i + 1
			}
		}
	}
	return 0
}

// isAbbreviation reports whether word (including its trailing period) is shaped
// like an abbreviation rather than a sentence's last word.
func isAbbreviation(word string) bool {
	stem := strings.TrimSuffix(word, ".")
	return len(stem) <= 1 || strings.Contains(stem, ".")
}

// RenderTypeScript emits the generated union.
func RenderTypeScript(entries []Entry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("notify: refusing to emit an empty method set; " +
			"an empty union types every switch case as an error and would read as a " +
			"dispatch bug rather than a generator one")
	}

	var b bytes.Buffer
	b.WriteString(`/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

// Generated from backend/internal/notify/method.go. Do not edit by hand.
//
// Regenerate:  cd backend && WESCODE_NOTIFY_WRITE=1 go test ./internal/notify
// Verified by: make vet  (the same test, without the env var)
//
// This is the TypeScript half of the notification contract. The Go half owns the
// names; this file is derived from it, so the two cannot disagree. Dispatch
// switches over the union exhaustively and ends in assertNever, which is what
// turns "a notification nobody listens for" from a silent drop into a build
// failure. Editing this file by hand re-opens that gap: the next regeneration
// overwrites the edit, and until then the union describes a backend that does
// not exist.

`)
	b.WriteString("export const wescodeNotificationMethods = [\n")
	for _, e := range entries {
		if e.Summary != "" {
			fmt.Fprintf(&b, "\t// %s: %s\n", e.GoName, e.Summary)
		} else {
			fmt.Fprintf(&b, "\t// %s\n", e.GoName)
		}
		fmt.Fprintf(&b, "\t%s,\n", tsQuote(e.Wire))
	}
	b.WriteString("] as const;\n\n")

	b.WriteString(`export type WescodeNotificationMethod = typeof wescodeNotificationMethods[number];

/**
 * Narrows a method name off the wire.
 *
 * A false result means the backend sent a name this build was not compiled
 * against — a version skew between the packaged extension and the packaged Go
 * binary, not a missing branch. Callers should say which, because the two need
 * opposite fixes.
 */
export function isWescodeNotificationMethod(method: string): method is WescodeNotificationMethod {
	return (wescodeNotificationMethods as readonly string[]).includes(method);
}
`)
	return b.Bytes(), nil
}

// tsQuote renders a wire name as a single-quoted TypeScript string, matching the
// editor's lint config.
func tsQuote(s string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(s) + "'"
}
