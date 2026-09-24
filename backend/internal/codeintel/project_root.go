package codeintel

import (
	"github.com/weisyn/wesgine/tool"
)

// projectRoot returns the directory to use for resolving relative file paths
// in codeintel tools. PrimaryRoot (user's project root) is preferred over
// WorkDir (Cell-internal artifact directory).
func projectRoot(tc *tool.ToolContext) string {
	if tc == nil {
		return ""
	}
	if tc.PrimaryRoot != "" {
		return tc.PrimaryRoot
	}
	return tc.WorkDir
}
