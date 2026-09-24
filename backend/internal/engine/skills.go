package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// skillFrontmatter is a minimal subset of SKILL.md frontmatter fields
// used by skill_drafts.go for draft parsing.
type skillFrontmatter struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description,omitempty"`
	Operators   []string      `yaml:"operators,omitempty"`
	Metadata    skillMetadata `yaml:"metadata,omitempty"`
}

type skillMetadata struct {
	Enabled                *bool    `yaml:"enabled,omitempty"`
	Tags                   []string `yaml:"tags,omitempty"`
	Version                string   `yaml:"version,omitempty"`
	ExecutionMode          string   `yaml:"execution_mode,omitempty"`
	DisableModelInvocation bool     `yaml:"disable_model_invocation,omitempty"`
}

// parseSkillFile reads a SKILL.md and returns frontmatter + raw content.
func parseSkillFile(path string) (skillFrontmatter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillFrontmatter{}, err
	}
	content := string(data)
	var fm skillFrontmatter
	if strings.HasPrefix(content, "---") {
		end := strings.Index(content[3:], "---")
		if end >= 0 {
			yamlBlock := content[3 : 3+end]
			_ = yaml.Unmarshal([]byte(yamlBlock), &fm)
		}
	}
	return fm, nil
}

// skillsDir returns the directory where user skills are stored (per-cell).
// INV-WS-02: empty cellID = no Cell = empty string (caller should handle).
func (s *Service) skillsDir() string {
	s.mu.Lock()
	dir := s.dataDir
	id := s.cellID
	s.mu.Unlock()
	if id == "" || dir == "" {
		return ""
	}
	return filepath.Join(dir, "cells", id, "skills")
}

// SkillInfo is the in-process representation of a skill.
type SkillInfo struct {
	Slug                   string
	Name                   string
	Description            string
	Enabled                bool
	Tags                   []string
	Path                   string
	Operators              []string
	Category               string
	Channel                string
	ExecutionMode          string
	DisableModelInvocation bool
	UsageCount             int64
}

// ListSkills returns metadata for each installed skill. Results are cached
// for 5 seconds to avoid repeated ReadDir + ReadFile on every UI navigation.
func (s *Service) ListSkills() ([]SkillInfo, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return nil, nil
	}

	s.skillCacheMu.RLock()
	if s.skillCacheData != nil && time.Since(s.skillCacheTime) < 5*time.Second {
		cached := s.skillCacheData
		s.skillCacheMu.RUnlock()
		return cached, nil
	}
	s.skillCacheMu.RUnlock()

	infos, err := cell.Skills().List(context.Background())
	if err != nil {
		return nil, err
	}
	stats := cell.Skills().Stats()
	result := make([]SkillInfo, 0, len(infos))
	for _, info := range infos {
		si := SkillInfo{
			Slug:        info.Name,
			Name:        info.Name,
			Description: info.Description,
			Enabled:     info.Enabled,
			Tags:        info.Tags,
			Channel:     info.Channel,
		}
		if st, ok := stats[info.Name]; ok {
			si.UsageCount = st.InvocationCount
		}
		result = append(result, si)
	}

	s.skillCacheMu.Lock()
	s.skillCacheData = result
	s.skillCacheTime = time.Now()
	s.skillCacheMu.Unlock()

	return result, nil
}

// VisibleSkillInfo is the projection of `skill.Info` used by the wesui
// SkillActivationBar (ADR-326). Mirrors the wesclaw /v1/skills/visible shape.
type VisibleSkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Version     string   `json:"version,omitempty"`
	AlwaysOn    bool     `json:"always_on"`
	Enabled     bool     `json:"enabled"`
	Degraded    bool     `json:"degraded,omitempty"`
	Missing     []string `json:"missing,omitempty"`
}

// ListVisibleSkills returns the enabled skills the given agent may activate,
// intersected with its Skills whitelist (INV-SKILL-01). Passing an empty
// agentID or an agent whose Skills is nil disables the whitelist filter and
// returns every installed & enabled skill.
func (s *Service) ListVisibleSkills(ctx context.Context, agentID string) ([]VisibleSkillInfo, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return nil, fmt.Errorf("engine: not initialized")
	}
	infos, err := cell.Skills().List(ctx)
	if err != nil {
		return nil, err
	}

	var whitelist map[string]struct{}
	if agentID != "" {
		cfg, agentErr := cell.Agents().Get(ctx, agentID)
		if agentErr == nil && cfg != nil && cfg.Skills != nil {
			whitelist = make(map[string]struct{}, len(cfg.Skills))
			for _, name := range cfg.Skills {
				whitelist[name] = struct{}{}
			}
		}
	}

	out := make([]VisibleSkillInfo, 0, len(infos))
	for _, info := range infos {
		if !info.Enabled {
			continue
		}
		if whitelist != nil {
			if _, ok := whitelist[info.Name]; !ok {
				continue
			}
		}
		out = append(out, VisibleSkillInfo{
			Name:        info.Name,
			Description: info.Description,
			Tags:        info.Tags,
			Version:     info.Version,
			AlwaysOn:    info.Always,
			Enabled:     info.Enabled,
			Degraded:    info.Degraded,
			Missing:     info.Missing,
		})
	}
	return out, nil
}

// InvalidateSkillCache clears the cached skill list so the next call re-reads from disk.
func (s *Service) InvalidateSkillCache() {
	s.skillCacheMu.Lock()
	s.skillCacheData = nil
	s.skillCacheMu.Unlock()
}

// CreateSkill creates a new skill directory with a template SKILL.md.
func (s *Service) CreateSkill(ctx context.Context, name string) (SkillInfo, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return SkillInfo{}, fmt.Errorf("engine: not initialized")
	}
	slug := skillSlug(name)
	if slug == "" {
		return SkillInfo{}, fmt.Errorf("invalid skill name")
	}
	desc := name + " 的领域知识"
	tmpl := fmt.Sprintf("---\nname: %s\ndescription: %q\nmetadata:\n  enabled: true\n  tags: []\n---\n\n# %s\n\n在此描述该技能的领域知识...\n", slug, desc, name)
	tmpDir, err := os.MkdirTemp("", "skill-create-*")
	if err != nil {
		return SkillInfo{}, err
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(tmpl), 0o644); err != nil {
		return SkillInfo{}, err
	}
	if err := cell.Skills().InstallFromDir(ctx, slug, tmpDir); err != nil {
		return SkillInfo{}, err
	}
	s.InvalidateSkillCache()
	return SkillInfo{Slug: slug, Name: name, Description: desc, Enabled: true, Tags: []string{}}, nil
}

// ToggleSkill enables or disables a skill.
func (s *Service) ToggleSkill(ctx context.Context, slug string, enabled bool) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return fmt.Errorf("engine: not initialized")
	}
	s.InvalidateSkillCache()
	return cell.Skills().SetEnabled(ctx, slug, enabled)
}

// DeleteSkill removes a skill directory.
func (s *Service) DeleteSkill(ctx context.Context, slug string) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return fmt.Errorf("engine: not initialized")
	}
	s.InvalidateSkillCache()
	return cell.Skills().Uninstall(ctx, slug)
}

// ReloadSkills triggers an immediate skill cache refresh.
func (s *Service) ReloadSkills(ctx context.Context) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return nil
	}
	s.InvalidateSkillCache()
	return cell.Skills().Reload(ctx)
}

func (s *Service) reloadSkillsIfReady(ctx context.Context) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell != nil {
		if err := cell.Skills().Reload(ctx); err != nil {
			slog.Warn("[skills] reload after install failed", "error", err)
		}
	}
}

// skillSlug converts a display name to a filesystem-safe slug.
func skillSlug(name string) string {
	slug := strings.ToLower(name)
	var b strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// skillViewToInfo is no longer needed — ListSkills now projects from engine skill.Info directly.
