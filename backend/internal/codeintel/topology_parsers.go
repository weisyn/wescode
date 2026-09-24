package codeintel

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Go ---

func parseGoModReplace(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var paths []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "replace (" {
			inBlock = true
			continue
		}
		if inBlock && trimmed == ")" {
			inBlock = false
			continue
		}
		if strings.HasPrefix(trimmed, "replace ") || inBlock {
			if idx := strings.Index(trimmed, "=>"); idx >= 0 {
				rhs := strings.TrimSpace(trimmed[idx+2:])
				parts := strings.Fields(rhs)
				if len(parts) >= 1 {
					target := parts[0]
					if !strings.Contains(target, "://") && !isRemoteModule(target) {
						if !filepath.IsAbs(target) {
							target = filepath.Join(workDir, target)
						}
						paths = append(paths, target)
					}
				}
			}
		}
	}
	return paths
}

func isRemoteModule(s string) bool {
	parts := strings.SplitN(s, "/", 2)
	return len(parts) >= 2 && strings.Contains(parts[0], ".")
}

// --- Rust ---

var cargoPathRe = regexp.MustCompile(`path\s*=\s*"([^"]+)"`)

func parseCargoTomlPath(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range cargoPathRe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

var cargoMembersRe = regexp.MustCompile(`"([^"]+)"`)

func parseCargoWorkspace(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var paths []string
	inWorkspace := false
	inMembers := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[workspace]" {
			inWorkspace = true
			continue
		}
		if inWorkspace && strings.HasPrefix(trimmed, "[") {
			inWorkspace = false
			inMembers = false
			continue
		}
		if inWorkspace && strings.HasPrefix(trimmed, "members") {
			inMembers = true
		}
		if inMembers {
			for _, m := range cargoMembersRe.FindAllStringSubmatch(trimmed, -1) {
				p := m[1]
				matches, err := filepath.Glob(filepath.Join(workDir, p))
				if err == nil && len(matches) > 0 {
					paths = append(paths, matches...)
				} else {
					if !filepath.IsAbs(p) {
						p = filepath.Join(workDir, p)
					}
					paths = append(paths, p)
				}
			}
			if strings.Contains(trimmed, "]") {
				inMembers = false
			}
		}
	}
	return paths
}

// --- TypeScript/JavaScript ---

func parsePackageJSONWorkspaces(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || len(pkg.Workspaces) == 0 {
		return nil
	}

	var globs []string
	if err := json.Unmarshal(pkg.Workspaces, &globs); err != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if err := json.Unmarshal(pkg.Workspaces, &obj); err == nil {
			globs = obj.Packages
		}
	}

	var paths []string
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(workDir, g))
		if err == nil {
			paths = append(paths, matches...)
		}
	}
	return paths
}

func parseTsconfigReferences(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var tsconfig struct {
		References []struct {
			Path string `json:"path"`
		} `json:"references"`
	}
	if err := json.Unmarshal(data, &tsconfig); err != nil {
		return nil
	}
	var paths []string
	for _, ref := range tsconfig.References {
		p := ref.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- pnpm ---

func parsePnpmWorkspaceYAML(workDir, filePath string) []string {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var paths []string
	inPackages := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "packages:" {
			inPackages = true
			continue
		}
		if inPackages {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(trimmed, "-") {
				break
			}
			if strings.HasPrefix(trimmed, "- ") {
				pkg := strings.Trim(strings.TrimPrefix(trimmed, "- "), "'\"")
				matches, err := filepath.Glob(filepath.Join(workDir, pkg))
				if err == nil {
					paths = append(paths, matches...)
				}
			}
		}
	}
	return paths
}

// --- Python ---

var pyprojectPathRe = regexp.MustCompile(`\{[^}]*path\s*=\s*"([^"]+)"`)

func parsePyprojectPath(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range pyprojectPathRe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- C# ---

var csprojRefRe = regexp.MustCompile(`<ProjectReference\s+Include="([^"]+)"`)

func parseCsprojProjectRef(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range csprojRefRe.FindAllStringSubmatch(string(data), -1) {
		ref := m[1]
		ref = strings.ReplaceAll(ref, "\\", "/")
		dir := filepath.Dir(ref)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(filePath), dir)
		}
		paths = append(paths, dir)
	}
	return paths
}

var slnProjectRe = regexp.MustCompile(`Project\([^)]*\)\s*=\s*"[^"]*",\s*"([^"]+)"`)

func parseSlnProjects(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range slnProjectRe.FindAllStringSubmatch(string(data), -1) {
		ref := m[1]
		ref = strings.ReplaceAll(ref, "\\", "/")
		dir := filepath.Dir(ref)
		if dir == "." {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workDir, dir)
		}
		paths = append(paths, dir)
	}
	return paths
}

// --- Java/Kotlin (Gradle) ---

var gradleProjectDirRe = regexp.MustCompile(`projectDir\s*=\s*file\(\s*["']([^"']+)["']\s*\)`)

func parseGradleSettings(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range gradleProjectDirRe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- Swift ---

var swiftPathRe = regexp.MustCompile(`\.package\s*\(\s*(?:name:\s*"[^"]*",\s*)?path:\s*"([^"]+)"`)

func parseSwiftPackagePath(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range swiftPathRe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- Dart ---

var pubspecPathRe = regexp.MustCompile(`(?m)^\s+path:\s+(.+)$`)

func parsePubspecPath(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range pubspecPathRe.FindAllStringSubmatch(string(data), -1) {
		p := strings.TrimSpace(m[1])
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- Ruby ---

var gemfilePathRe = regexp.MustCompile(`gem\s+['"][^'"]+['"],\s*path:\s*['"]([^'"]+)['"]`)

func parseGemfilePath(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range gemfilePathRe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}

// --- PHP ---

func parseComposerPathRepo(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var composer struct {
		Repositories []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(data, &composer); err != nil {
		return nil
	}
	var paths []string
	for _, repo := range composer.Repositories {
		if repo.Type == "path" && repo.URL != "" {
			p := repo.URL
			matches, err := filepath.Glob(filepath.Join(workDir, p))
			if err == nil && len(matches) > 0 {
				paths = append(paths, matches...)
			} else {
				if !filepath.IsAbs(p) {
					p = filepath.Join(workDir, p)
				}
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// --- C/C++ ---

var cmakeSubdirRe = regexp.MustCompile(`add_subdirectory\s*\(\s*([^\s)]+)`)

func parseCMakeAddSubdirectory(workDir, filePath string) []string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range cmakeSubdirRe.FindAllStringSubmatch(string(data), -1) {
		p := strings.Trim(m[1], "\"'")
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		absWork, _ := filepath.Abs(workDir)
		if !strings.HasPrefix(abs, absWork+string(filepath.Separator)) {
			paths = append(paths, abs)
		}
	}
	return paths
}

// --- Git Submodules ---

var gitSubmodulePathRe = regexp.MustCompile(`(?m)^\s*path\s*=\s*(.+)$`)

func parseGitSubmodules(workDir string) []string {
	data, err := os.ReadFile(filepath.Join(workDir, ".gitmodules"))
	if err != nil {
		return nil
	}
	var paths []string
	for _, m := range gitSubmodulePathRe.FindAllStringSubmatch(string(data), -1) {
		p := strings.TrimSpace(m[1])
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		paths = append(paths, p)
	}
	return paths
}
