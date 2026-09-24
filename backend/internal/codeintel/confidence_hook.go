package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/weisyn/wesgine/tool"
)

var codeIndexPtrType = reflect.TypeOf((*CodeIndex)(nil))

// DependsOnCKG reports whether t reads the code index, by inspecting whether
// its concrete type holds a *CodeIndex field.
//
// This replaces a hand-written allowlist of tool names. The allowlist was a
// second copy of the tool set and it had drifted: entries named tools that do
// not exist ("callers" where the registered name is "find_callers"), so the
// highest-traffic tools were exempt from disclosure while the list still read
// as complete. A type cannot drift from itself.
//
// The scan is one level deep on purpose. Every CKG tool in this package takes
// the index as a constructor argument and stores it as a direct field, so that
// is the only shape worth checking; chasing the index transitively through
// handles to other subsystems would classify nearly every tool in the process
// and stamp a readiness footer onto terminal output.
//
// What no type walk can see: an index held behind an interface field or captured
// in a closure (NewGetTrendTool takes a func() *sql.DB). A tool shaped that way
// goes dark here, which is why TestCKGToolSet_RecognizesRegisteredTools pins the
// tools that matter by name — it is the only check that fails when a tool's
// shape, rather than its list entry, is what drifted.
func DependsOnCKG(t tool.Tool) bool {
	if t == nil {
		return false
	}
	rt := reflect.TypeOf(t)
	for rt != nil && rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt == nil || rt.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Type == codeIndexPtrType {
			return true
		}
	}
	return false
}

// CKGToolSet maps registered tool names to their CKGBias.
//
// It is populated at registration time, so its keys are the same strings the
// executor dispatches on. That identity is the whole point: the previous design
// stated the names a second time by hand, and the copy was free to be wrong
// without anything failing.
type CKGToolSet struct {
	mu   sync.RWMutex
	bias map[string]CKGBias
}

func NewCKGToolSet() *CKGToolSet {
	return &CKGToolSet{bias: make(map[string]CKGBias)}
}

// Observe records t if it reads the code index, and is a no-op otherwise.
//
// Callers pass every tool they register without pre-filtering — a caller that
// has to remember which tools are CKG-backed is the allowlist again, just
// spelled as control flow.
func (s *CKGToolSet) Observe(t tool.Tool) {
	if s == nil || !DependsOnCKG(t) {
		return
	}
	bias := BiasUnderReport
	if d, ok := t.(CKGBiasDeclarer); ok {
		bias = d.CKGBias()
	}
	s.mu.Lock()
	s.bias[t.Name()] = bias
	s.mu.Unlock()
}

// Lookup reports the bias recorded for name.
func (s *CKGToolSet) Lookup(name string) (CKGBias, bool) {
	if s == nil {
		return BiasUnderReport, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bias[name]
	return b, ok
}

// Names returns the recognized tool names in sorted order.
func (s *CKGToolSet) Names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.bias))
	for name := range s.bias {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// NewConfidenceEnrichHook returns a PostCallHook that attaches CKG readiness
// disclosure to results from every tool in set (INV-CKG-CONF-01).
//
// This is the only disclosure point. Tools used to append their own footers,
// which produced four thresholds, four wordings, and coverage that varied by
// result branch — impact_analysis disclosed when it found nothing and stayed
// silent on the branch that listed rows the index might be missing.
//
// set is shared by pointer with the registration loop and is still empty when
// this closure is built; by the time a tool call arrives it is populated.
func NewConfidenceEnrichHook(ci *CodeIndex, set *CKGToolSet) tool.PostCallHook {
	return func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, tc *tool.ToolContext) {
		if result == nil || ci == nil {
			return
		}
		bias, ok := set.Lookup(call.Name)
		if !ok {
			return
		}
		ci.EnrichResult(result, bias)

		// Per-file reachability enrichment for file-targeted tools.
		enrichFileReachability(ci, call, result, tc)
	}
}

// extractFilePath pulls a file path from a tool call's JSON input.
// Returns "" when no recognisable path key is found.
func extractFilePath(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "target_file", "file", "path", "target"} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// enrichFileReachability attaches per-file CKG quality signals when the tool
// targets a specific file and that file has degraded reachability.
func enrichFileReachability(ci *CodeIndex, call tool.ToolCall, result *tool.ToolResult, tc *tool.ToolContext) {
	if result.IsError {
		return
	}
	raw := extractFilePath(call.Input)
	if raw == "" {
		return
	}

	// Convert absolute path to CKG-relative path.
	rel := raw
	if tc != nil && tc.PrimaryRoot != "" && filepath.IsAbs(raw) {
		if r, err := filepath.Rel(tc.PrimaryRoot, raw); err == nil {
			rel = r
		}
	}
	rel = IndexPath(rel)

	fp, err := ci.FileReachabilityProfile(rel)
	if err != nil || fp.TotalFunctions == 0 {
		return
	}

	degraded := fp.NameReachable > 0 || fp.Isolated > 0 ||
		(fp.EdgeResolutionRate >= 0 && fp.EdgeResolutionRate < 0.5)
	if !degraded {
		return
	}

	if result.Metadata == nil {
		result.Metadata = make(map[string]any, 4)
	}
	result.Metadata["file_reachability"] = map[string]any{
		"connected":       fp.Connected,
		"name_reachable":  fp.NameReachable,
		"isolated":        fp.Isolated,
		"total_functions": fp.TotalFunctions,
		"edge_resolution": fp.EdgeResolutionRate,
	}

	var parts []string
	if fp.Isolated > 0 {
		parts = append(parts, fmt.Sprintf("%d isolated", fp.Isolated))
	}
	if fp.NameReachable > 0 {
		parts = append(parts, fmt.Sprintf("%d name-only reachable", fp.NameReachable))
	}
	if fp.EdgeResolutionRate >= 0 && fp.EdgeResolutionRate < 0.5 {
		parts = append(parts, fmt.Sprintf("edge resolution %.0f%%", fp.EdgeResolutionRate*100))
	}
	result.Content += "\n[File CKG: " + rel + " — " + strings.Join(parts, ", ") +
		" of " + fmt.Sprintf("%d", fp.TotalFunctions) + " symbols; verify call relationships before editing]"
}
