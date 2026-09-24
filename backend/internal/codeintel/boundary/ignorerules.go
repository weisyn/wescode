package boundary

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// IgnoreRules represents parsed .wescodeignore rules.
// Supports gitignore-style patterns: directory names, glob patterns, negation.
type IgnoreRules struct {
	dirs     map[string]bool
	patterns []string
}

// EmptyIgnoreRules returns an IgnoreRules that matches nothing.
func EmptyIgnoreRules() *IgnoreRules {
	return &IgnoreRules{dirs: make(map[string]bool)}
}

// LoadWescodeignore loads .wescodeignore from the given workspace root.
// Returns empty rules if file doesn't exist.
func LoadWescodeignore(workDir string) *IgnoreRules {
	path := filepath.Join(workDir, ".wescodeignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return EmptyIgnoreRules()
	}
	return parseIgnoreFile(string(data))
}

func parseIgnoreFile(content string) *IgnoreRules {
	rules := &IgnoreRules{dirs: make(map[string]bool)}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Negation rules: not supported for safety-core dirs
		if strings.HasPrefix(line, "!") {
			continue
		}
		// Directory patterns: "dirname/" or bare "dirname" without glob chars
		cleaned := strings.TrimSuffix(line, "/")
		if !strings.ContainsAny(cleaned, "*?[]") {
			rules.dirs[cleaned] = true
		} else {
			rules.patterns = append(rules.patterns, line)
		}
	}
	return rules
}

// MatchDir checks if a directory name matches the ignore rules.
func (r *IgnoreRules) MatchDir(name string) bool {
	if r == nil {
		return false
	}
	return r.dirs[name]
}

// MatchPath checks if a file path matches the ignore rules.
func (r *IgnoreRules) MatchPath(relPath string) bool {
	if r == nil {
		return false
	}
	base := filepath.Base(relPath)
	if r.dirs[base] {
		return true
	}
	parts := strings.Split(relPath, string(filepath.Separator))
	for _, part := range parts {
		if r.dirs[part] {
			return true
		}
	}
	for _, pattern := range r.patterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}
	return false
}

// ── Global cache for .wescodeignore ──

var (
	ignoreCacheMu sync.RWMutex
	ignoreCache   = make(map[string]*IgnoreRules)
)

// LoadWescodeignoreCached returns cached .wescodeignore rules for a workspace.
func LoadWescodeignoreCached(workDir string) *IgnoreRules {
	ignoreCacheMu.RLock()
	if rules, ok := ignoreCache[workDir]; ok {
		ignoreCacheMu.RUnlock()
		return rules
	}
	ignoreCacheMu.RUnlock()

	rules := LoadWescodeignore(workDir)

	ignoreCacheMu.Lock()
	ignoreCache[workDir] = rules
	ignoreCacheMu.Unlock()

	return rules
}

// InvalidateIgnoreCache clears the cache for a specific workspace.
func InvalidateIgnoreCache(workDir string) {
	ignoreCacheMu.Lock()
	delete(ignoreCache, workDir)
	ignoreCacheMu.Unlock()
}
