package langs

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed *.json
var langFiles embed.FS

// Registry holds all loaded language configurations indexed by ID and extension.
type Registry struct {
	byID  map[string]*LanguageConfig
	byExt map[string]*LanguageConfig
}

var (
	defaultRegistry *Registry
	registryOnce    sync.Once
)

// Default returns the global registry (loaded once from embedded JSON files).
func Default() *Registry {
	registryOnce.Do(func() {
		defaultRegistry = mustLoad()
	})
	return defaultRegistry
}

// ByID returns the language config for the given language ID (e.g. "go", "python").
// Returns nil if the language is not registered.
func (r *Registry) ByID(id string) *LanguageConfig {
	if r == nil {
		return nil
	}
	return r.byID[id]
}

// ByExtension returns the language config for the given file extension (e.g. ".go", ".py").
// Returns nil if the extension is not registered.
func (r *Registry) ByExtension(ext string) *LanguageConfig {
	if r == nil {
		return nil
	}
	return r.byExt[strings.ToLower(ext)]
}

// All returns all registered language configurations.
func (r *Registry) All() []*LanguageConfig {
	if r == nil {
		return nil
	}
	configs := make([]*LanguageConfig, 0, len(r.byID))
	for _, cfg := range r.byID {
		configs = append(configs, cfg)
	}
	return configs
}

// AllTopologyFiles returns a deduplicated list of all dependency files
// declared across all registered languages' topology configs.
func (r *Registry) AllTopologyFiles() []DependencyFileConfig {
	if r == nil {
		return nil
	}
	seen := make(map[string]bool)
	var result []DependencyFileConfig
	for _, cfg := range r.byID {
		for _, df := range cfg.Topology.DependencyFiles {
			key := df.File + ":" + df.Format
			if !seen[key] {
				seen[key] = true
				result = append(result, df)
			}
		}
	}
	return result
}

func mustLoad() *Registry {
	entries, err := langFiles.ReadDir(".")
	if err != nil {
		return &Registry{byID: map[string]*LanguageConfig{}, byExt: map[string]*LanguageConfig{}}
	}

	r := &Registry{
		byID:  make(map[string]*LanguageConfig),
		byExt: make(map[string]*LanguageConfig),
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := langFiles.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		var cfg LanguageConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		r.byID[cfg.Language.ID] = &cfg
		for _, ext := range cfg.Language.Extensions {
			r.byExt[strings.ToLower(ext)] = &cfg
		}
	}

	return r
}
