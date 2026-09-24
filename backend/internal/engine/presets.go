package engine

import (
	"context"
	"log/slog"
	"strings"

	"github.com/weisyn/wescode/presets"
	"github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/agent"
	"gopkg.in/yaml.v3"

	appengine "github.com/weisyn/wesapp/engine"
)

// PresetAgent is the YAML schema for files under presets/agents/*.yaml.
type PresetAgent struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Emoji        string   `yaml:"emoji"`
	Role         string   `yaml:"role"`
	Goal         string   `yaml:"goal"`
	Intent       string   `yaml:"intent"`
	Category     string   `yaml:"category"`
	Tags         []string `yaml:"tags"`
	Suggestions  []string `yaml:"suggestions"`
	SystemPrompt string   `yaml:"system_prompt"`
}

// loadPresetAgents reads all YAML files from the embedded presets/agents/ directory.
func loadPresetAgents() []PresetAgent {
	entries, err := presets.AgentsFS.ReadDir("agents")
	if err != nil {
		return nil
	}
	var result []PresetAgent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := presets.AgentsFS.ReadFile("agents/" + e.Name())
		if err != nil {
			continue
		}
		var pa PresetAgent
		if err := yaml.Unmarshal(data, &pa); err != nil {
			continue
		}
		if pa.ID == "" || pa.Name == "" {
			continue
		}
		result = append(result, pa)
	}
	return result
}

// cachedPresets is loaded once at init time from embedded YAML.
var cachedPresets = loadPresetAgents()

// seedBuiltinAgentsToCell registers all embedded preset agents into the
// given Cell's AgentStore. Called both at initial boot and on every
// Cool→Warm re-activation via CellSpec.OnStarted.
func seedBuiltinAgentsToCell(ctx context.Context, cell *wesgine.Cell) {
	for _, p := range cachedPresets {
		meta := map[string]string{
			"emoji":    p.Emoji,
			"category": p.Category,
		}
		if len(p.Suggestions) > 0 {
			meta["suggestions"] = strings.Join(p.Suggestions, "\x1f")
		}
		if len(p.Tags) > 0 {
			meta["tags"] = strings.Join(p.Tags, "\x1f")
		}
		cfg := agent.AgentConfig{
			ID:           p.ID,
			Name:         p.Name,
			Role:         p.Role,
			Goal:         p.Goal,
			Intent:       p.Intent,
			SystemPrompt: p.SystemPrompt,
			Metadata:     meta,
		}

		role := DelegationRoleForAgent(p.ID)
		meta["delegation_role"] = string(role)
		switch role {
		case RoleWorker:
			cfg.ToolPolicy = WorkerToolPolicy()
		case RoleOrchestrator:
			cfg.ToolPolicy = OrchestratorToolPolicy()
		}

		_ = cell.Agents().Register(ctx, cfg)
	}
}

// seedBuiltinSkills installs embedded skill directories into the Cell via the
// engine's InstallFromDir path (quota + policy + version snapshot).
func seedBuiltinSkills(ctx context.Context, cell *wesgine.Cell) {
	if err := appengine.SeedSkills(ctx, presets.SkillsFS, "skills", cell, slog.Default()); err != nil {
		slog.Warn("[engine] seed builtin skills failed", "error", err)
	}
}

// ListPresetAgents returns the embedded preset agent catalog for the frontend.
func (s *Service) ListPresetAgents() []PresetAgent {
	return cachedPresets
}
