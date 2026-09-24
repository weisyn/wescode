package editengine

import (
	"regexp"
	"strings"
)

var reMultiNewline = regexp.MustCompile(`\n{3,}`)

// detectIndentWidth infers the file's indent width from leading whitespace.
// Returns 4 as the default when indeterminate.
func detectIndentWidth(s string) int {
	counts := make(map[int]int)
	for _, line := range strings.Split(s, "\n") {
		if len(line) == 0 || line[0] != ' ' {
			continue
		}
		spaces := 0
		for _, ch := range line {
			if ch == ' ' {
				spaces++
			} else {
				break
			}
		}
		if spaces >= 2 && spaces <= 8 {
			counts[spaces]++
		}
	}
	best, bestCount := 4, 0
	for width := 2; width <= 8; width++ {
		if counts[width] > bestCount {
			best = width
			bestCount = counts[width]
		}
	}
	return best
}

// normalize transforms text to a canonical form for fuzzy matching:
//  1. Unify CRLF → LF
//  2. Replace all leading tabs with spaces (using inferred indent width)
//  3. Trim trailing whitespace per line
//  4. Collapse 3+ consecutive newlines to 2
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	indentWidth := detectIndentWidth(s)
	tabReplacement := strings.Repeat(" ", indentWidth)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = expandTabs(line, tabReplacement)
		lines[i] = strings.TrimRight(line, " \t")
	}
	s = strings.Join(lines, "\n")
	s = reMultiNewline.ReplaceAllString(s, "\n\n")
	return s
}

// expandTabs replaces each tab character with the given space string.
func expandTabs(line, tabReplacement string) string {
	if !strings.Contains(line, "\t") {
		return line
	}
	return strings.ReplaceAll(line, "\t", tabReplacement)
}

// NormalizedMatch finds old in content after normalizing both, then maps the
// match back to the original content's byte range.
//
// Returns (start, end, true) if a unique normalized match is found,
// or (-1, -1, false) otherwise.
//
// This function is called by NewCombinedFallback as the Tier 2 matching step.
// It does not use the file path (language-agnostic normalization).
func NormalizedMatch(content, old string) (int, int, bool) {
	normContent := normalize(content)
	normOld := normalize(old)

	idx := strings.Index(normContent, normOld)
	if idx < 0 {
		return -1, -1, false
	}
	if strings.Contains(normContent[idx+1:], normOld) {
		return -1, -1, false
	}

	origStart := mapNormOffsetToOriginal(content, normContent, idx)
	origEnd := mapNormOffsetToOriginal(content, normContent, idx+len(normOld))

	return origStart, origEnd, true
}

// mapNormOffsetToOriginal maps a byte offset in the normalized string back
// to the corresponding offset in the original string. Handles CRLF removal,
// trailing whitespace trimming, tab expansion, and blank line collapse.
func mapNormOffsetToOriginal(original, normalized string, normOffset int) int {
	normPos := 0
	origPos := 0
	origBytes := []byte(original)
	normBytes := []byte(normalized)

	for normPos < normOffset && origPos < len(origBytes) {
		if normPos < len(normBytes) && origPos < len(origBytes) {
			normByte := normBytes[normPos]
			origByte := origBytes[origPos]

			if normByte == origByte {
				normPos++
				origPos++
				continue
			}

			if origByte == '\r' {
				origPos++
				continue
			}

			// Tab in original expands to spaces in normalized.
			// Advance normPos by the tab-expansion width while consuming 1 orig byte.
			if origByte == '\t' && normByte == ' ' {
				origPos++
				for normPos < normOffset && normPos < len(normBytes) && normBytes[normPos] == ' ' {
					normPos++
				}
				continue
			}

			if origByte == ' ' || origByte == '\t' {
				origPos++
				continue
			}
			if origByte == '\n' && normByte != '\n' {
				origPos++
				continue
			}
		}
		normPos++
		origPos++
	}
	for origPos < len(origBytes) && normPos == normOffset {
		b := origBytes[origPos]
		if b == '\r' || b == ' ' || b == '\t' {
			origPos++
		} else {
			break
		}
	}
	return origPos
}
