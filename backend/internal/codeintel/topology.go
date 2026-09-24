package codeintel

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/langs"
)

type topologyParser func(workDir, filePath string) []string

var parserRegistry = map[string]topologyParser{
	"gomod_replace":           parseGoModReplace,
	"cargo_toml_path":         parseCargoTomlPath,
	"cargo_workspace":         parseCargoWorkspace,
	"package_json_workspaces": parsePackageJSONWorkspaces,
	"tsconfig_references":     parseTsconfigReferences,
	"pnpm_workspace_yaml":     parsePnpmWorkspaceYAML,
	"pyproject_path":          parsePyprojectPath,
	"csproj_project_ref":      parseCsprojProjectRef,
	"sln_projects":            parseSlnProjects,
	"gradle_settings":         parseGradleSettings,
	"swift_package_path":      parseSwiftPackagePath,
	"pubspec_path":            parsePubspecPath,
	"gemfile_path":            parseGemfilePath,
	"composer_path_repo":      parseComposerPathRepo,
	"cmake_add_subdirectory":  parseCMakeAddSubdirectory,
}

// ProjectTopologyDetector discovers local dependency paths from project
// manifests. Driven by the langs.Registry topology config.
type ProjectTopologyDetector struct {
	mu          sync.Mutex
	cache       map[string]topologyResult
	cacheTTL    time.Duration
	scanTimeout time.Duration
	registry    *langs.Registry
}

type topologyResult struct {
	paths     []string
	expiresAt time.Time
}

func NewProjectTopologyDetector() *ProjectTopologyDetector {
	return &ProjectTopologyDetector{
		cache:       make(map[string]topologyResult),
		cacheTTL:    60 * time.Second,
		scanTimeout: 2 * time.Second,
		registry:    langs.Default(),
	}
}

// Detect returns additional project roots discovered from workDir's
// dependency manifests. Results are cached per workDir with 60s TTL.
func (d *ProjectTopologyDetector) Detect(ctx context.Context, workDir string) []string {
	if workDir == "" {
		return nil
	}

	d.mu.Lock()
	if r, ok := d.cache[workDir]; ok && time.Now().Before(r.expiresAt) {
		d.mu.Unlock()
		return r.paths
	}
	d.mu.Unlock()

	scanCtx, cancel := context.WithTimeout(ctx, d.scanTimeout)
	defer cancel()

	var discovered []string

	depFiles := d.registry.AllTopologyFiles()
	for _, df := range depFiles {
		if scanCtx.Err() != nil {
			break
		}
		parser, ok := parserRegistry[df.Format]
		if !ok {
			continue
		}

		var matchedFiles []string
		if strings.ContainsAny(df.File, "*?[") {
			matches, err := filepath.Glob(filepath.Join(workDir, df.File))
			if err == nil {
				matchedFiles = matches
			}
		} else {
			candidate := filepath.Join(workDir, df.File)
			if _, err := os.Stat(candidate); err == nil {
				matchedFiles = []string{candidate}
			}
		}

		for _, filePath := range matchedFiles {
			paths := parser(workDir, filePath)
			discovered = append(discovered, paths...)
		}
	}

	discovered = append(discovered, parseGitSubmodules(workDir)...)

	// Reverse discovery: scan sibling directories for manifests that reference
	// workDir via local path dependencies (e.g. go.mod replace directives).
	// This allows discovering consumers when starting from a library repo.
	discovered = append(discovered, reverseScanSiblings(scanCtx, workDir, depFiles)...)

	var secondLevel []string
	for _, root := range discovered {
		if scanCtx.Err() != nil {
			break
		}
		absRoot, err := filepath.Abs(root)
		if err != nil || absRoot == workDir {
			continue
		}
		for _, df := range depFiles {
			parser, ok := parserRegistry[df.Format]
			if !ok {
				continue
			}
			var matchedFiles []string
			if strings.ContainsAny(df.File, "*?[") {
				matches, _ := filepath.Glob(filepath.Join(absRoot, df.File))
				matchedFiles = matches
			} else {
				candidate := filepath.Join(absRoot, df.File)
				if _, err := os.Stat(candidate); err == nil {
					matchedFiles = []string{candidate}
				}
			}
			for _, fp := range matchedFiles {
				secondLevel = append(secondLevel, parser(absRoot, fp)...)
			}
		}
	}
	discovered = append(discovered, secondLevel...)

	valid := validateAndDedup(workDir, discovered)

	d.mu.Lock()
	d.cache[workDir] = topologyResult{paths: valid, expiresAt: time.Now().Add(d.cacheTTL)}
	d.mu.Unlock()

	if len(valid) > 0 {
		slog.Info("[codeintel] topology discovered", "workDir", workDir, "paths", valid)
	}

	return valid
}

// reverseScanSiblings checks sibling directories of workDir (same parent) for
// dependency manifests that contain a local path reference pointing back to
// workDir. This handles the case where workDir is a library and a sibling repo
// consumes it via e.g. go.mod replace.
func reverseScanSiblings(ctx context.Context, workDir string, depFiles []langs.DependencyFileConfig) []string {
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return nil
	}
	parent := filepath.Dir(absWork)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}

	var found []string
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !entry.IsDir() {
			continue
		}
		siblingAbs := filepath.Join(parent, entry.Name())
		if siblingAbs == absWork {
			continue
		}
		for _, df := range depFiles {
			parser, ok := parserRegistry[df.Format]
			if !ok {
				continue
			}
			candidate := filepath.Join(siblingAbs, df.File)
			if strings.ContainsAny(df.File, "*?[") {
				continue // skip glob patterns for reverse scan
			}
			if _, serr := os.Stat(candidate); serr != nil {
				continue
			}
			paths := parser(siblingAbs, candidate)
			for _, p := range paths {
				ap, aerr := filepath.Abs(p)
				if aerr != nil {
					continue
				}
				if ap == absWork {
					found = append(found, siblingAbs)
					break
				}
			}
		}
	}
	return found
}

func validateAndDedup(workDir string, discovered []string) []string {
	seen := make(map[string]bool)
	absWork, _ := filepath.Abs(workDir)
	seen[absWork] = true

	var valid []string
	for _, p := range discovered {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			seen[abs] = true
			valid = append(valid, abs)
		}
	}
	return valid
}

// DedupPaths deduplicates a list of file paths by absolute path.
func DedupPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var result []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			result = append(result, abs)
		}
	}
	return result
}
