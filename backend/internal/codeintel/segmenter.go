package codeintel

import (
	"strings"

	"github.com/weisyn/wesgine/tool"

	"github.com/weisyn/wescode/internal/treesitter"
)

// CodeSegmenter implements tool.FileSegmenter using tree-sitter AST parsing.
// It produces function/type/struct level segments for code files, giving
// the read tool precise structural indexes for large source files.
type CodeSegmenter struct {
	ts *treesitter.ParserPool
}

// NewCodeSegmenter creates a CodeSegmenter backed by the given tree-sitter ParserPool.
func NewCodeSegmenter(ts *treesitter.ParserPool) *CodeSegmenter {
	return &CodeSegmenter{ts: ts}
}

func (s *CodeSegmenter) Supports(ext string) bool {
	_, ok := treesitter.DetectLang("dummy" + ext)
	return ok
}

func (s *CodeSegmenter) Segment(content []byte, ext string) ([]tool.FileSegment, error) {
	lang, ok := treesitter.DetectLang("dummy" + ext)
	if !ok {
		return nil, nil
	}

	tree, err := s.ts.Parse(lang, content, nil)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()

	syms := treesitter.ExtractSymbols(lang, tree, content)
	if len(syms) == 0 {
		return nil, nil
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	segments := make([]tool.FileSegment, 0, len(syms))
	for i, sym := range syms {
		startLine := sym.StartLine + 1 // tree-sitter is 0-based, FileSegment is 1-based
		endLine := sym.EndLine + 1

		if endLine <= 0 || endLine > totalLines {
			if i+1 < len(syms) {
				endLine = syms[i+1].StartLine // 0-based next start = 1-based end of current
			} else {
				endLine = totalLines
			}
		}

		charCount := 0
		for j := startLine - 1; j < endLine && j < totalLines; j++ {
			charCount += len(lines[j]) + 1
		}

		sig := sym.Signature
		if len(sig) > 120 {
			sig = sig[:117] + "..."
		}

		segments = append(segments, tool.FileSegment{
			Index:     i + 1,
			Kind:      string(sym.Kind),
			Name:      sym.Name,
			StartLine: startLine,
			EndLine:   endLine,
			CharCount: charCount,
			Signature: sig,
		})
	}
	return segments, nil
}
