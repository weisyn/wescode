package codeintel

import (
	"embed"
	"encoding/json"
	"log/slog"
	"strings"
)

// EffectKind represents a single side-effect category.
type EffectKind string

const (
	EffectPure            EffectKind = "pure"
	EffectReadsDB         EffectKind = "reads_db"
	EffectWritesDB        EffectKind = "writes_db"
	EffectNetworkIO       EffectKind = "network_io"
	EffectFSRead          EffectKind = "fs_read"
	EffectFSWrite         EffectKind = "fs_write"
	EffectMutatesGlobal   EffectKind = "mutates_global"
	EffectSpawnsGoroutine EffectKind = "spawns_goroutine"
	EffectPanics          EffectKind = "panics"
	EffectUnknown         EffectKind = "unknown"
)

// EffectSet is the set of side effects a function may produce.
type EffectSet struct {
	Effects []EffectKind `json:"effects"`
}

// Has reports whether the set contains the given effect kind.
func (es *EffectSet) Has(k EffectKind) bool {
	for _, e := range es.Effects {
		if e == k {
			return true
		}
	}
	return false
}

// Add appends a kind if not already present.
func (es *EffectSet) Add(k EffectKind) {
	if !es.Has(k) {
		es.Effects = append(es.Effects, k)
	}
}

// IsPure reports whether the function has no side effects.
func (es *EffectSet) IsPure() bool {
	return len(es.Effects) == 1 && es.Effects[0] == EffectPure
}

// String returns a comma-separated representation, e.g. "reads_db,writes_db".
func (es *EffectSet) String() string {
	parts := make([]string, len(es.Effects))
	for i, e := range es.Effects {
		parts[i] = string(e)
	}
	return strings.Join(parts, ",")
}

// Merge adds all effects from other into es (dedup).
func (es *EffectSet) Merge(other *EffectSet) {
	if other == nil {
		return
	}
	for _, k := range other.Effects {
		es.Add(k)
	}
}

// MarshalJSON returns the JSON encoding of the effect set.
func (es *EffectSet) MarshalJSON() ([]byte, error) {
	if es == nil || len(es.Effects) == 0 {
		return []byte(`{"effects":[]}`), nil
	}
	type alias EffectSet
	return json.Marshal((*alias)(es))
}

//go:embed effects/*.json
var effectSeedFS embed.FS

// seedFileForLang maps a canonical language name to its embedded seed JSON.
var seedFileForLang = map[string]string{
	"go":         "effects/stdlib-go.json",
	"typescript": "effects/stdlib-node.json",
	"javascript": "effects/stdlib-node.json",
	"python":     "effects/stdlib-python.json",
}

// loadSeedTable reads the embedded JSON for the given language and returns
// a map from qualified function name → list of EffectKinds.
func loadSeedTable(lang string) map[string][]EffectKind {
	fname, ok := seedFileForLang[lang]
	if !ok {
		return nil
	}
	data, err := effectSeedFS.ReadFile(fname)
	if err != nil {
		slog.Warn("effects: failed to read seed file", "file", fname, "err", err)
		return nil
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("effects: failed to parse seed file", "file", fname, "err", err)
		return nil
	}
	table := make(map[string][]EffectKind, len(raw))
	for k, kinds := range raw {
		ek := make([]EffectKind, len(kinds))
		for i, s := range kinds {
			ek[i] = EffectKind(s)
		}
		table[k] = ek
	}
	return table
}

// PropagateEffects performs bottom-up effect propagation through CALLS edges.
//
//  1. Load stdlib seed table (embedded JSON) for the given language.
//  2. Mark leaf nodes from seed by matching Node.Name / QualifiedName.
//  3. Iterative propagation: callee effects flow upward to callers.
//  4. Unmarked nodes → EffectUnknown (conservative, INV-P5-06).
//
// Pure graph traversal — no IO (INV-P5-02).
func PropagateEffects(buf *GraphBuffer, lang string) {
	seed := loadSeedTable(lang)

	buf.mu.Lock()
	defer buf.mu.Unlock()

	// Step 1: Build caller→callees adjacency from CALLS edges.
	// callerQName → []calleeQName
	callerToCallees := make(map[string][]string, len(buf.Edges)/4)
	for _, e := range buf.Edges {
		if e.Kind != EdgeCall {
			continue
		}
		targetKey := e.TargetQName
		if targetKey == "" {
			targetKey = e.TargetName
		}
		if targetKey == "" {
			continue
		}
		callerToCallees[e.SourceQName] = append(callerToCallees[e.SourceQName], targetKey)
	}

	// Step 2: Seed leaf nodes from the stdlib table.
	// Match by both full QualifiedName and short Name (the seed keys use stdlib paths).
	for _, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		if n.Effects != nil {
			continue
		}

		// Try exact QualifiedName match, then Name match.
		if kinds, ok := seed[n.QualifiedName]; ok {
			n.Effects = &EffectSet{Effects: kinds}
		} else if kinds, ok := seed[n.Name]; ok {
			n.Effects = &EffectSet{Effects: kinds}
		}
	}

	// Step 3: Iterative propagation (max 100 rounds to prevent infinite loops).
	const maxIterations = 100
	for iter := 0; iter < maxIterations; iter++ {
		changed := false
		for qname, callees := range callerToCallees {
			caller := buf.Nodes[qname]
			if caller == nil {
				continue
			}

			var merged EffectSet
			if caller.Effects != nil {
				merged.Effects = append(merged.Effects, caller.Effects.Effects...)
			}

			prevLen := len(merged.Effects)

			for _, calleeKey := range callees {
				callee := buf.Nodes[calleeKey]
				if callee == nil || callee.Effects == nil {
					continue
				}
				merged.Merge(callee.Effects)
			}

			if len(merged.Effects) > prevLen {
				// Remove "pure" if non-pure effects were merged in.
				if len(merged.Effects) > 1 {
					merged.Effects = removePure(merged.Effects)
				}
				caller.Effects = &EffectSet{Effects: merged.Effects}
				changed = true
			}
		}
		if !changed {
			slog.Info("effects: propagation converged", "iterations", iter+1)
			break
		}
	}

	// Step 4: Mark remaining function/method nodes as EffectUnknown (INV-P5-06).
	for _, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		if n.Effects == nil {
			n.Effects = &EffectSet{Effects: []EffectKind{EffectUnknown}}
		}
	}
}

// removePure strips EffectPure from a slice that also contains non-pure effects.
func removePure(effects []EffectKind) []EffectKind {
	out := effects[:0]
	for _, e := range effects {
		if e != EffectPure {
			out = append(out, e)
		}
	}
	return out
}
