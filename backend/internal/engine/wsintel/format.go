package wsintel

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// FormatPrompt produces the complete workspace section for system prompt injection.
func (ctx *WorkspaceContext) FormatPrompt() string {
	var sb strings.Builder

	// "Active" is the vocabulary INV-IO-09 sanctions for PrimaryRoot, and the
	// engine's own path block uses it. The qualifier is what was missing: this
	// is the folder chosen in the chat panel and pinned for the session
	// (chatViewPane `_sessionWorkDir`), which in a multi-root workspace is
	// usually just folders[0] and not necessarily what the user is looking at.
	// Bare "Active:", a model reports it as the project the user is working
	// on. Qualified, it says what it is — the same claim the engine makes as
	// "tool commands run here by default" (INV-CTX-81: the wording's certainty
	// may not exceed its source's).
	sb.WriteString("\n[Workspace]\nActive (default cwd): ")
	sb.WriteString(ctx.Primary.Path)
	sb.WriteString(" (")
	sb.WriteString(ctx.Primary.Type)
	sb.WriteString(")")
	// No "multi-module" tag here: it keyed off the marker count, and go.mod
	// plus go.sum are two markers for one module, so every Go repository
	// claimed to be multi-module. The Modules line below answers the same
	// question by naming the directories.
	if ctx.Primary.Description != "" {
		sb.WriteString(" — ")
		sb.WriteString(ctx.Primary.Description)
	}
	if len(ctx.Primary.Modules) > 0 {
		// The root carries no manifest, so build and test commands belong in
		// one of these directories rather than here.
		sb.WriteString("\nModules: ")
		sb.WriteString(formatModules(ctx.Primary.Modules))
	}

	if len(ctx.Primary.Frameworks) > 0 {
		sb.WriteString("\nFrameworks: ")
		sb.WriteString(strings.Join(ctx.Primary.Frameworks, ", "))
	}
	if ctx.Primary.TestPattern != "" {
		sb.WriteString("\nTest Pattern: ")
		sb.WriteString(ctx.Primary.TestPattern)
	}
	if ctx.ActiveArea != "" {
		sb.WriteString("\nActive Area: ")
		sb.WriteString(ctx.ActiveArea)
	}

	if len(ctx.Siblings) > 0 {
		sb.WriteString("\nAlso open:")
		for _, sib := range ctx.Siblings {
			sb.WriteString("\n- ")
			sb.WriteString(sib.Path)
			sb.WriteString(" (")
			sb.WriteString(sib.Type)
			sb.WriteString(")")
			if sib.Description != "" {
				sb.WriteString(" — ")
				sb.WriteString(sib.Description)
			}
			if rel := findRelation(ctx.Relations, ctx.Primary.Path, sib.Path); rel != "" {
				sb.WriteString(" · ")
				sb.WriteString(rel)
			}
		}
	}

	if ctx.BuildInfo != nil {
		sb.WriteString("\n\n[Build]")
		if ctx.BuildInfo.Build != "" {
			sb.WriteString("\nBuild: ")
			sb.WriteString(ctx.BuildInfo.Build)
		}
		if ctx.BuildInfo.Test != "" {
			sb.WriteString("\nTest: ")
			sb.WriteString(ctx.BuildInfo.Test)
		}
		if ctx.BuildInfo.Lint != "" {
			sb.WriteString("\nLint: ")
			sb.WriteString(ctx.BuildInfo.Lint)
		}
	}

	if IsLegacy(ctx) {
		sb.WriteString("\n\n[Legacy Codebase Detected]")
		sb.WriteString("\nSignals:")
		if ctx.Primary.DeprecatedCount > 5 {
			sb.WriteString(fmt.Sprintf(" deprecated=%d", ctx.Primary.DeprecatedCount))
		}
		if ctx.Primary.TestFileCount == 0 {
			sb.WriteString(" no-tests")
		}
		if ctx.Primary.LastCommitAge > 6*30*24*time.Hour {
			sb.WriteString(fmt.Sprintf(" last-commit=%dd-ago", int(ctx.Primary.LastCommitAge.Hours()/24)))
		}
		if !hasCI(ctx.Primary.Path) {
			sb.WriteString(" no-ci")
		}
		if !hasLinter(ctx.Primary.Path) {
			sb.WriteString(" no-linter")
		}
		sb.WriteString("\nApproach: Understand-first. Do NOT apply act-first patterns. Use legacy-navigation skill.")
	}

	if IsEmpty(ctx) {
		sb.WriteString("\n\n[Empty Project]")
		sb.WriteString("\nThis is a new/empty project. Consider bootstrapping with system-design skill.")
	}

	if len(ctx.Boundary.ReadOnlyDirs) > 0 {
		sb.WriteString("\n\n[Read-Only Zones]\n")
		for _, d := range ctx.Boundary.ReadOnlyDirs {
			sb.WriteString("- ")
			sb.WriteString(d)
			sb.WriteString("/ (do not modify)\n")
		}
	}

	sb.WriteString("\nAll listed projects are accessible. Use absolute paths for cross-project access.")

	return sb.String()
}

func formatModules(modules []Module) string {
	parts := make([]string, 0, len(modules))
	for _, m := range modules {
		parts = append(parts, m.Dir+"/ ("+m.Type+")")
	}
	return strings.Join(parts, ", ")
}

func findRelation(relations []Relation, primary, sibling string) string {
	for _, r := range relations {
		if r.From == primary && r.To == sibling {
			return formatRelationKind(r.Kind, primary, sibling)
		}
		if r.From == sibling && r.To == primary {
			return formatRelationKind(r.Kind, sibling, primary)
		}
	}
	return ""
}

func formatRelationKind(kind, from, to string) string {
	fromName := filepath.Base(from)
	toName := filepath.Base(to)
	switch kind {
	case "depends_on":
		return "consumed by " + fromName + " via dependency"
	case "consumed_by":
		return "consumes " + toName
	case "shares_types":
		return "shares types with " + toName
	default:
		return kind
	}
}

// FormatIndexStatus formats code index readiness information for system prompt injection.
func FormatIndexStatus(ckg *CKGStatus) string {
	if ckg == nil {
		return ""
	}

	var block string

	switch {
	case ckg.TotalFiles == 0 && ckg.IndexedFiles == 0:
		block = "\n[Strategy: Greenfield]\n" +
			"New project — no existing code indexed. Code search tools unavailable.\n" +
			"Priorities: 1) Create a plan with verification conditions before writing code. " +
			"2) Write tests alongside implementation. " +
			"3) Establish patterns early — they become project conventions."
		// Greenfield: no indexed data, focus file warning is meaningless.
		return block

	case ckg.Completeness >= 0.9:
		block = "\n[Strategy: Brownfield]\n" +
			"Codebase fully indexed. Before modifications: use impact_analysis to check downstream effects. " +
			"Follow established patterns (pattern-consistency skill active)."

	case ckg.Completeness < 1.0 || ckg.StaleFiles > 0:
		block = fmt.Sprintf("\n[Code Index Status]\nCoverage: %.0f%% (%d/%d files indexed",
			ckg.Completeness*100, ckg.IndexedFiles, ckg.TotalFiles)
		if ckg.StaleFiles > 0 {
			block += fmt.Sprintf(", %d stale", ckg.StaleFiles)
		}
		block += ")"
		if ckg.Indexing {
			block += "\nNote: Background indexing in progress. Results from find_orphans/impact_analysis may be incomplete."
		}
	}

	// Focus file degradation warning (INV-CKG-SINGLE-CLASS).
	// Appended after the index status block so the LLM sees both the global
	// completeness and the per-file quality signal for the file it is about to edit.
	if ckg.FocusFile != "" {
		degraded := ckg.FocusNameReachableCount > 0 || ckg.FocusIsolatedCount > 0 ||
			(ckg.FocusEdgeResolutionRate >= 0 && ckg.FocusEdgeResolutionRate < 0.5)
		if degraded {
			var parts []string
			if ckg.FocusIsolatedCount > 0 {
				parts = append(parts, fmt.Sprintf("%d isolated", ckg.FocusIsolatedCount))
			}
			if ckg.FocusNameReachableCount > 0 {
				parts = append(parts, fmt.Sprintf("%d name-only reachable", ckg.FocusNameReachableCount))
			}
			if ckg.FocusEdgeResolutionRate >= 0 && ckg.FocusEdgeResolutionRate < 0.5 {
				parts = append(parts, fmt.Sprintf("edge resolution %.0f%%", ckg.FocusEdgeResolutionRate*100))
			}
			// The label spells out call graph rather than the subsystem's
			// acronym: this string is injected into the system prompt, and
			// INV-UX-01 keeps internal component names out of anything the model
			// reads. The acronym means nothing to it and leaks straight into
			// user-visible replies. The rest of the sentence already used the
			// general term, so the label now agrees with it.
			block += fmt.Sprintf("\n[Call graph warning: %s] %s — call graph incomplete; verify callers/callees with grep before refactoring.",
				ckg.FocusFile, strings.Join(parts, ", "))
		}
	}

	return block
}
