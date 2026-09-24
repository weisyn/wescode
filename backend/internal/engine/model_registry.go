package engine

import "github.com/weisyn/wesapp/provider"

// ModelSpec describes a known model's context and output constraints.
type ModelSpec struct {
	ContextWindow int
	MaxOutput     int
}

// LookupModelSpec returns the ModelSpec for a known model ID, matching
// by exact ID first and then by prefix followed by one of `-/.@:`.
func LookupModelSpec(modelID string) (ModelSpec, bool) {
	cat := provider.GetCatalog()
	if cat == nil {
		return ModelSpec{}, false
	}
	m, ok := cat.LookupModelSpec(modelID)
	if !ok {
		return ModelSpec{}, false
	}
	return ModelSpec{ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutput}, true
}
