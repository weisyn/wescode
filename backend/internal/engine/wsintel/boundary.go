package wsintel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
)

// ProbeBoundary detects read-only reference directories and generated file zones.
// Delegates to the unified boundary package.
func ProbeBoundary(path string) Boundary {
	return Boundary{
		ReadOnlyDirs:  boundary.ProbeReadOnlyDirs(path),
		GeneratedDirs: boundary.ProbeGeneratedDirs(path),
	}
}

// BuildUnknowns generates a list of things the AI should not assume, based on
// what was NOT detected in the workspace.
func BuildUnknowns(ctx *WorkspaceContext) string {
	var unknowns []string

	if ctx.BuildInfo == nil {
		unknowns = append(unknowns, "build/test commands not detected — probe before assuming")
	}
	if ctx.Primary.Type == "unknown" {
		unknowns = append(unknowns, "project type not identified — check file structure first")
	}
	if !hasCI(ctx.Primary.Path) {
		unknowns = append(unknowns, "no CI config detected — do not assume test/lint behavior")
	}
	if !hasLinter(ctx.Primary.Path) {
		unknowns = append(unknowns, "no linter config detected — do not claim code passes lint")
	}

	if len(unknowns) == 0 {
		return ""
	}
	return "[Unknown — verify before assuming]\n- " + strings.Join(unknowns, "\n- ")
}

var ciPaths = []string{
	".github/workflows",
	".gitlab-ci.yml",
	".circleci",
	"Jenkinsfile",
	".travis.yml",
	"azure-pipelines.yml",
	"bitbucket-pipelines.yml",
}

func hasCI(root string) bool {
	for _, p := range ciPaths {
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err == nil {
			return true
		}
	}
	return false
}

var linterPaths = []string{
	".eslintrc",
	".eslintrc.js",
	".eslintrc.json",
	".eslintrc.yml",
	"eslint.config.js",
	"eslint.config.mjs",
	".golangci.yml",
	".golangci.yaml",
	".rubocop.yml",
	".flake8",
	"pyproject.toml",
	".clang-tidy",
	".editorconfig",
	"biome.json",
	"deno.json",
}

func hasLinter(root string) bool {
	for _, p := range linterPaths {
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err == nil {
			return true
		}
	}
	return false
}
