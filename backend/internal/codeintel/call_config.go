package codeintel

import (
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel/langs"
	"github.com/weisyn/wescode/internal/treesitter"
)

// CallConfigForFile returns a treesitter.CallNodeConfig derived from the
// langs registry for the given file path. Returns nil if no config is
// available for the language (falls back to hardcoded extraction).
// WIRE-06: bridges langs.json call declarations to treesitter extraction.
func CallConfigForFile(path string) *treesitter.CallNodeConfig {
	ext := strings.ToLower(filepath.Ext(path))
	reg := langs.Default()
	if reg == nil {
		return nil
	}
	lc := reg.ByExtension(ext)
	if lc == nil {
		return nil
	}
	fc := lc.Calls.FunctionCall
	mc := lc.Calls.MethodCall
	if fc.NodeType == "" {
		return nil
	}

	cfg := &treesitter.CallNodeConfig{
		FuncCallNodeType: fc.NodeType,
		FuncField:        fc.FunctionField,
	}
	if mc.NodeType != "" {
		cfg.MethodNodeType = mc.NodeType
		cfg.ReceiverField = mc.ReceiverField
		cfg.MethodField = mc.MethodField
	}
	return cfg
}
