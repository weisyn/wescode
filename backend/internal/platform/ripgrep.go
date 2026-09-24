package platform

import (
	"regexp"
	"strconv"
)

// ripgrepLineRe matches ripgrep --no-heading output: "path:line:content".
// The path group is non-greedy so a Windows drive letter (C:\...) is kept
// whole; content is everything after the line number and is preserved
// verbatim (it may itself contain ":" and digits, e.g. "see ref:3:4").
var ripgrepLineRe = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)

// ParseRipgrepLine parses one line of ripgrep --no-heading output
// ("path:line:content") into path and content. It differs from ParseFileRef:
// rg has no column field, so content is kept exactly as printed — a leading
// numeric segment or embedded ":" must not be mistaken for a column.
// ok is false for non-match lines (empty, "--" separator, context lines
// using "-" instead of ":").
func ParseRipgrepLine(line string) (path string, lineNo int, content string, ok bool) {
	m := ripgrepLineRe.FindStringSubmatch(line)
	if m == nil {
		return "", 0, "", false
	}
	lineNo, _ = strconv.Atoi(m[2])
	return m[1], lineNo, m[3], true
}
