package lspbridge

import (
	"path/filepath"
	"strings"
)

// LangForPath returns the language ID based on file extension.
// Used by CKG enrichment and Agent tools to pick a language, not to spawn a server.
func LangForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".py":
		return "python"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return "typescript"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".dart":
		return "dart"
	case ".cs":
		return "csharp"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".php":
		return "php"
	default:
		return ""
	}
}
