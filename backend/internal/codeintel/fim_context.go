package codeintel

import (
	"context"
	"path/filepath"
	"strings"
)

const (
	fimContextMaxChars  = 2000 // INV-FIM-01: cross-file signature budget
	fimMaxImportSymbols = 10   // per-import symbol limit
)

// FIMContextBuilder constructs enriched context for FIM completion prompts.
// It gathers cross-file signatures and project conventions to improve
// completion accuracy beyond the current-file-only approach.
type FIMContextBuilder struct {
	index *CodeIndex
}

// NewFIMContextBuilder creates a builder backed by the code index.
func NewFIMContextBuilder(index *CodeIndex) *FIMContextBuilder {
	return &FIMContextBuilder{index: index}
}

// FIMContext holds the assembled context for a FIM prompt.
type FIMContext struct {
	CurrentFileSignatures string // signatures from the active file
	CrossFileSignatures   string // signatures from imported/related files
	ProjectConventions    string // project naming/import style hints
}

// Build assembles FIM context for the given file path.
func (b *FIMContextBuilder) Build(ctx context.Context, path, prefix string) FIMContext {
	var fc FIMContext
	if b.index == nil {
		return fc
	}

	// Layer 1: current file signatures (existing behavior)
	syms, _ := b.index.ListFileSymbols(ctx, path)
	var currentSigs strings.Builder
	for _, sym := range syms {
		if sym.Kind == "import" || sym.Signature == "" {
			continue
		}
		currentSigs.WriteString("// ")
		currentSigs.WriteString(sym.Signature)
		currentSigs.WriteByte('\n')
	}
	fc.CurrentFileSignatures = currentSigs.String()

	// Layer 2: cross-file signatures from imports
	var crossSigs strings.Builder
	crossBudget := fimContextMaxChars
	imports, _ := b.index.ListImports(ctx, path)
	focusLang := contextLanguageKey(path)
	var langExts []string
	if lc := DetectLanguage(path); lc.Language != "" {
		langExts = lc.Exts
	}

	for _, imp := range imports {
		if crossBudget <= 0 {
			break
		}
		impSyms, _ := b.index.SearchSymbols(ctx, imp, fimMaxImportSymbols, langExts...)
		for _, sym := range impSyms {
			if sym.Signature == "" {
				continue
			}
			if focusLang != "" && contextLanguageKey(sym.FilePath) != focusLang {
				continue
			}
			line := "// " + sym.Signature + "\n"
			if len(line) > crossBudget {
				break
			}
			crossSigs.WriteString(line)
			crossBudget -= len(line)
		}
	}
	fc.CrossFileSignatures = crossSigs.String()

	// Layer 3: project conventions from prefix analysis
	fc.ProjectConventions = inferConventions(prefix, path)

	return fc
}

// FormatFIMPrompt constructs the complete FIM prompt with enriched context.
func FormatFIMPrompt(prefix, suffix string, fc FIMContext, path string) string {
	var sb strings.Builder
	sb.WriteString("You are a code completion engine. Complete the code at the cursor position.\n")
	sb.WriteString("Output ONLY the completion text, no explanation, no markdown.\n\n")

	if fc.ProjectConventions != "" {
		sb.WriteString("// Project conventions:\n")
		sb.WriteString(fc.ProjectConventions)
		sb.WriteByte('\n')
	}

	allSigs := fc.CurrentFileSignatures + fc.CrossFileSignatures
	if allSigs != "" {
		sb.WriteString("// Types and functions in scope:\n")
		sb.WriteString(allSigs)
		sb.WriteByte('\n')
	}

	sb.WriteString("// File: ")
	sb.WriteString(path)
	sb.WriteString("\n// Code before cursor:\n")
	sb.WriteString(prefix)
	sb.WriteString("<CURSOR>")
	if suffix != "" {
		sb.WriteString("\n// Code after cursor:\n")
		sb.WriteString(suffix)
	}
	return sb.String()
}

// inferConventions extracts lightweight style hints from existing code.
func inferConventions(prefix, path string) string {
	ext := filepath.Ext(path)
	var hints []string

	switch ext {
	case ".go":
		if strings.Contains(prefix, "func (") {
			hints = append(hints, "// Go methods use receiver syntax")
		}
		if strings.Contains(prefix, "slog.") {
			hints = append(hints, "// Logging: use log/slog (structured)")
		}
		if strings.Contains(prefix, "fmt.Errorf") {
			hints = append(hints, "// Errors: use fmt.Errorf with %w wrapping")
		}
	case ".ts", ".tsx":
		if strings.Contains(prefix, "import {") {
			hints = append(hints, "// Imports: named imports (not default)")
		}
		if strings.Contains(prefix, "const ") && strings.Contains(prefix, " = (") {
			hints = append(hints, "// Functions: arrow function style")
		}
	}

	if len(hints) == 0 {
		return ""
	}
	return strings.Join(hints, "\n")
}
