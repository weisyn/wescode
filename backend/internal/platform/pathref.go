// Package platform hosts the cross-platform contract primitives for wescode.
//
// Business code must only depend on these primitives instead of embedding
// implicit POSIX assumptions (drive letters, colon-delimited refs, tool
// names, path case sensitivity). Each primitive has a single implementation
// with platform-aware behavior and dedicated tests.
package platform

import (
	"regexp"
	"strconv"
)

// refLineColRe matches "path:line:col[: message]".
// The non-greedy path group makes it drive-letter aware: for
// "C:\a\b.go:10:2: err" the first colon that is followed by digits belongs
// to the ref, so the path is "C:\a\b.go".
var refLineColRe = regexp.MustCompile(`^(.+?):(\d+):(\d+)(?:: ?(.*))?$`)

// refLineRe matches "path:line[: message]" (no column).
var refLineRe = regexp.MustCompile(`^(.+?):(\d+)(?:: ?(.*))?$`)

// ParseFileRef parses a "file:line:col: message" style reference into its
// parts. Accepted forms:
//
//	/path/a.go:10:2: undefined: x     (compiler-style, with column)
//	C:\a\b.go:10:2: err               (Windows drive letter, with column)
//	\\server\share\f.go:3:1: err      (UNC path)
//	src/foo.go:42: this is content    (ripgrep match line, no column)
//	main.go:5                         (bare line ref)
//
// ok is false when the line is not a file reference (empty line, plain
// filename without line info, context separators like "--").
func ParseFileRef(line string) (path string, lineNo, col int, rest string, ok bool) {
	if m := refLineColRe.FindStringSubmatch(line); m != nil {
		lineNo, _ = strconv.Atoi(m[2])
		col, _ = strconv.Atoi(m[3])
		return m[1], lineNo, col, m[4], true
	}
	if m := refLineRe.FindStringSubmatch(line); m != nil {
		lineNo, _ = strconv.Atoi(m[2])
		return m[1], lineNo, 0, m[3], true
	}
	return "", 0, 0, "", false
}
