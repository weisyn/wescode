package codeintel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// GitDiffSummary holds a cached git working tree diff for context injection.
type GitDiffSummary struct {
	HasChanges bool
	Summary    string // formatted diff stat + truncated diff content
}

// Retriever performs multi-stage code retrieval.
type Retriever struct {
	index    *CodeIndex
	ts       *treesitter.ParserPool
	lsp      LSPBridge
	metrics  *RetrievalMetrics
	fp       tool.FileProvider
	gitDiff  *GitDiffSummary
	lspCache *LSPCache
	strategy *ContextStrategy
}

// SetStrategy configures the context strategy for the current task.
func (r *Retriever) SetStrategy(task TaskType) {
	s := GetStrategy(task)
	r.strategy = &s
}

// NewRetriever creates a retriever backed by the given index.
func NewRetriever(index *CodeIndex, ts *treesitter.ParserPool, opts ...RetrieverOption) *Retriever {
	r := &Retriever{
		index: index,
		ts:    ts,
		lsp:   NoopLSP{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RetrieverOption configures a Retriever.
type RetrieverOption func(*Retriever)

// WithLSP injects an LSP bridge for enhanced type information.
func WithLSP(lsp LSPBridge) RetrieverOption {
	return func(r *Retriever) { r.lsp = lsp }
}

// WithFileProvider injects a FileProvider for overlay-aware file reading.
func WithFileProvider(fp tool.FileProvider) RetrieverOption {
	return func(r *Retriever) { r.fp = fp }
}

// WithMetrics injects a metrics collector for retrieval quality tracking.
func WithMetrics(m *RetrievalMetrics) RetrieverOption {
	return func(r *Retriever) { r.metrics = m }
}

// SetGitDiff updates the cached git diff summary for Stage 2 injection.
func (r *Retriever) SetGitDiff(diff *GitDiffSummary) {
	r.gitDiff = diff
}

// LSPCache returns the retriever's LSP result cache.
func (r *Retriever) LSPCache() *LSPCache {
	if r.lspCache == nil {
		r.lspCache = NewLSPCache()
	}
	return r.lspCache
}

// preWarmLSPDefinitions fires parallel LSP Definition requests for all call
// sites and populates the cache. The sequential loop then hits cache instead
// of making serial LSP calls. INV-CI-22: 3s total timeout.
func (r *Retriever) preWarmLSPDefinitions(ctx context.Context, focusFile string, fnStartLine int, callSites []treesitter.CallSite, cache *LSPCache) {
	warmCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, cs := range callSites {
		cs := cs
		wg.Add(1)
		go func() {
			defer wg.Done()
			absLine := fnStartLine + cs.Line
			absCol := cs.Col
			_, _ = cache.Definition(warmCtx, r.lsp, focusFile, absLine, absCol)
		}()
	}
	wg.Wait()
}

func (r *Retriever) readFile(path string) ([]byte, error) {
	if r.fp != nil {
		return r.fp.ReadFile(context.Background(), path)
	}
	return os.ReadFile(path)
}

func (r *Retriever) deterministicBase(ctx context.Context, state EditorState, taskType TaskType, focusIsUserTarget bool) []CodeFragment {
	var fragments []CodeFragment

	if state.FocusFile == "" {
		return fragments
	}

	content, err := r.readFile(state.FocusFile)
	if err != nil {
		return fragments
	}

	lineCount := strings.Count(string(content), "\n") + 1

	// When the user names a different file, reduce focus file priority
	// so tool search results take precedence in model reasoning.
	focusFileScore := 1.0
	outlineScore := 0.9
	if !focusIsUserTarget {
		focusFileScore = 0.4
		outlineScore = 0.3
	}

	if lineCount <= 500 {
		fragments = append(fragments, CodeFragment{
			Path:       state.FocusFile,
			StartLine:  0,
			EndLine:    lineCount - 1,
			Content:    string(content),
			Kind:       FragmentFocusFile,
			Reason:     "currently active file",
			TokenCost:  estimateTokens(string(content)),
			ValueScore: focusFileScore,
		})
	} else {
		syms, _ := r.index.ListFileSymbols(ctx, state.FocusFile)
		outline := buildSymbolOutline(syms)
		fragments = append(fragments, CodeFragment{
			Path:       state.FocusFile,
			Content:    outline,
			Kind:       FragmentSignature,
			Reason:     "large file symbol outline (>500 lines)",
			TokenCost:  estimateTokens(outline),
			ValueScore: outlineScore,
		})
		// CE-13: large file visible range — inject what the user is looking at
		if state.VisibleRange != nil && state.VisibleRange.End > state.VisibleRange.Start {
			visContent := r.readLines(state.FocusFile, state.VisibleRange.Start, state.VisibleRange.End)
			if visContent != "" {
				fragments = append(fragments, CodeFragment{
					Path:       state.FocusFile,
					StartLine:  state.VisibleRange.Start,
					EndLine:    state.VisibleRange.End,
					Content:    visContent,
					Kind:       FragmentVisibleRange,
					Reason:     "currently visible in editor viewport",
					TokenCost:  estimateTokens(visContent),
					ValueScore: 0.85,
				})
			}
		}
	}

	if state.Selection != nil && state.Selection.Text != "" {
		fragments = append(fragments, CodeFragment{
			Path:       state.FocusFile,
			StartLine:  state.Selection.StartLine,
			EndLine:    state.Selection.EndLine,
			Content:    state.Selection.Text,
			Kind:       FragmentSelection,
			Reason:     "user-selected code",
			TokenCost:  estimateTokens(state.Selection.Text),
			ValueScore: 1.0,
		})
	}

	lang, ok := treesitter.DetectLang(state.FocusFile)
	var langExts []string
	if ok {
		langExts = treesitter.LangExtensions(lang)
	}

	if ok {
		tree, err := r.ts.Parse(lang, content, nil)
		if err == nil {
			defer tree.Close()
			syms := treesitter.ExtractSymbols(lang, tree, content)
			if fn := treesitter.FindFunctionAt(syms, state.CursorLine); fn != nil {
				fnContent := string(content[fn.StartByte:fn.EndByte])
				fragments = append(fragments, CodeFragment{
					Path:       state.FocusFile,
					StartLine:  fn.StartLine,
					EndLine:    fn.EndLine,
					Content:    fnContent,
					Kind:       FragmentFunction,
					Symbol:     fn.Name,
					Reason:     "function at cursor position",
					TokenCost:  estimateTokens(fnContent),
					ValueScore: 0.95,
				})

				// TaskType-differentiated call chain depth (driven by ContextStrategy.MaxDepth)
				callDepth := 1
				if r.strategy != nil && r.strategy.MaxDepth > 1 {
					callDepth = r.strategy.MaxDepth
					if callDepth > 4 {
						callDepth = 4
					}
				}

				// Use CallSites (with position info) for type-aware call chain.
				callSites := treesitter.ExtractCallSites(lang, r.ts, content[fn.StartByte:fn.EndByte])
				lspCache := r.LSPCache()
				_, isNoopLSP := r.lsp.(NoopLSP)

				// INV-CI-22: pre-warm LSP cache in parallel (3s total timeout).
				if !isNoopLSP && len(callSites) > 0 {
					r.preWarmLSPDefinitions(ctx, state.FocusFile, fn.StartLine, callSites, lspCache)
				}

				for _, cs := range callSites {
					absLine := fn.StartLine + cs.Line
					absCol := cs.Col

					// INV-CI-20: Try LSP Definition first (type-precise); fallback to name search.
					var lspResolved bool
					if !isNoopLSP {
						defs, err := lspCache.Definition(ctx, r.lsp, state.FocusFile, absLine, absCol)
						if err == nil && len(defs) > 0 {
							def := defs[0]
							defContent := r.readLines(def.Path, max(def.Line-2, 0), def.Line+15)
							if defContent != "" {
								reason := "definition of " + cs.Name
								if cs.IsMethod {
									reason = "definition of " + cs.Receiver + "." + cs.Name
								}
								fragments = append(fragments, CodeFragment{
									Path:       def.Path,
									StartLine:  def.Line,
									EndLine:    def.Line + 15,
									Content:    defContent,
									Kind:       FragmentLSPDef,
									Symbol:     cs.Name,
									Reason:     reason,
									TokenCost:  estimateTokens(defContent),
									ValueScore: 0.85,
								})
								lspResolved = true
							}
						}
					}

					// Fallback: name-based search (existing logic)
					if !lspResolved {
						var callSyms []SymbolEntry

						if cs.IsMethod {
							hoverInfo, _ := r.lsp.Hover(ctx, state.FocusFile, absLine, absCol)
							if r.metrics != nil {
								r.metrics.RecordLSPHover(hoverInfo != "")
							}
							parentType := extractParentType(hoverInfo, cs.Receiver)
							if parentType != "" {
								allSyms, _ := r.index.FindSymbolInLang(ctx, cs.Name, langExts)
								for _, sym := range allSyms {
									if matchesParent(sym.Parent, parentType) {
										callSyms = append(callSyms, sym)
									}
								}
								if len(callSyms) > 0 && r.metrics != nil {
									r.metrics.RecordDisambiguation()
								}
							}
						}

						if len(callSyms) == 0 {
							callSyms, _ = r.index.FindSymbolInLang(ctx, cs.Name, langExts)
						}

						for _, sym := range callSyms {
							reason := "called by " + fn.Name + " (1-level call chain)"
							if cs.IsMethod && sym.Parent != "" {
								reason = "called by " + fn.Name + " → " + sym.Parent + "." + sym.Name
							}
							fragments = append(fragments, CodeFragment{
								Path:       sym.FilePath,
								StartLine:  sym.LineStart,
								EndLine:    sym.LineEnd,
								Content:    sym.Signature,
								Kind:       FragmentSignature,
								Symbol:     sym.Name,
								Reason:     reason,
								TokenCost:  estimateTokens(sym.Signature),
								ValueScore: 0.8,
							})

							if callDepth >= 2 {
								calleeBody := r.readLines(sym.FilePath, sym.LineStart, sym.LineEnd)
								if calleeBody != "" {
									deepCalls := treesitter.ExtractCallNamesWithConfig(lang, r.ts, []byte(calleeBody), CallConfigForFile(sym.FilePath))
									for _, deepName := range deepCalls {
										deepSyms, _ := r.index.FindSymbolInLang(ctx, deepName, langExts)
										for _, ds := range deepSyms {
											fragments = append(fragments, CodeFragment{
												Path:       ds.FilePath,
												StartLine:  ds.LineStart,
												EndLine:    ds.LineEnd,
												Content:    ds.Signature,
												Kind:       FragmentSignature,
												Symbol:     ds.Name,
												Reason:     "called by " + sym.Name + " (2-level call chain)",
												TokenCost:  estimateTokens(ds.Signature),
												ValueScore: 0.6,
											})
										}
									}
								}
							}
						}
					}
				}

				// TaskType-differentiated reverse references
				injectReverse := taskType != TaskImplement
				if injectReverse && fn.Name != "" {
					var lspRefsResolved bool

					// INV-CI-21: Try LSP References first (type-precise); fallback to SearchSymbols.
					if !isNoopLSP {
						refs, err := lspCache.References(ctx, r.lsp, state.FocusFile, fn.StartLine, 0)
						if err == nil && len(refs) > 0 {
							lspRefsResolved = true
							refLimit := 15
							if taskType == TaskRefactor {
								refLimit = 50
							}
							count := 0
							for _, ref := range refs {
								if count >= refLimit {
									break
								}
								if ref.Path == state.FocusFile {
									continue
								}
								contextLines := 3
								valScore := 0.75
								reason := "references " + fn.Name + " (LSP precise)"
								if taskType == TaskRefactor {
									contextLines = 15
									valScore = 0.85
									reason = "references " + fn.Name + " (LSP refactor scope)"
								}
								refContent := r.readLines(ref.Path, max(ref.Line-contextLines, 0), ref.Line+contextLines)
								if refContent == "" {
									continue
								}
								fragments = append(fragments, CodeFragment{
									Path:       ref.Path,
									StartLine:  ref.Line - contextLines,
									EndLine:    ref.Line + contextLines,
									Content:    refContent,
									Kind:       FragmentLSPRef,
									Reason:     reason,
									TokenCost:  estimateTokens(refContent),
									ValueScore: valScore,
								})
								count++
							}
						}
					}

					// Fallback: FTS5 name search (existing logic)
					if !lspRefsResolved {
						refLimit := 20
						if taskType == TaskRefactor {
							refLimit = 100
						}
						callerSyms, _ := r.index.SearchSymbols(ctx, fn.Name, refLimit)
						callerSyms = filterByLang(callerSyms, langExts)
						for _, sym := range callerSyms {
							if sym.FilePath == state.FocusFile && sym.Name == fn.Name {
								continue
							}
							refContent := sym.Signature
							valScore := 0.75
							reason := "references " + fn.Name + " (reverse call chain)"
							if taskType == TaskRefactor {
								body := r.readLines(sym.FilePath, sym.LineStart, sym.LineEnd)
								if body != "" {
									refContent = body
									valScore = 0.85
									reason = "references " + fn.Name + " (refactor: full body)"
								}
							}
							fragments = append(fragments, CodeFragment{
								Path:       sym.FilePath,
								StartLine:  sym.LineStart,
								EndLine:    sym.LineEnd,
								Content:    refContent,
								Kind:       FragmentSignature,
								Symbol:     sym.Name,
								Reason:     reason,
								TokenCost:  estimateTokens(refContent),
								ValueScore: valScore,
							})
						}
					}

					// D4: Consume pattern extraction for callers (FTS5 path only;
					// LSP refs path already provides precise context).
					if !lspRefsResolved && (taskType == TaskExplain || taskType == TaskReview) {
						callerSymsFTS, _ := r.index.SearchSymbols(ctx, fn.Name, 20)
						callerSymsFTS = filterByLang(callerSymsFTS, langExts)
						for _, sym := range callerSymsFTS {
							if sym.FilePath == state.FocusFile && sym.Name == fn.Name {
								continue
							}
							patterns := r.extractConsumePatterns(sym.FilePath, sym.LineStart, sym.LineEnd, fn.Name)
							for _, pat := range patterns {
								fragments = append(fragments, CodeFragment{
									Path:       sym.FilePath,
									StartLine:  pat.Line,
									EndLine:    pat.Line,
									Content:    pat.Pattern,
									Kind:       FragmentRelevantCode,
									Symbol:     sym.Name,
									Reason:     "consume pattern: " + pat.Kind + " in " + sym.Name,
									TokenCost:  estimateTokens(pat.Pattern),
									ValueScore: 0.65,
								})
							}
						}
					}
				}

				// INV-DF-02: Variable data flow injection — when cursor is on a variable
				// (not a function name), inject the def-use chain within the function.
				if fn != nil && state.CursorLine >= fn.StartLine && state.CursorLine <= fn.EndLine {
					varFlow := treesitter.ExtractVarFlow(lang, r.ts, content[fn.StartByte:fn.EndByte])
					if len(varFlow) > 0 {
						cursorLineInFn := state.CursorLine - fn.StartLine
						cursorVar := identifyVarAtCursor(varFlow, cursorLineInFn, state.CursorCol)
						if cursorVar != "" {
							dfText := formatVarFlowChain(varFlow, cursorVar, fn.StartLine)
							if dfText != "" {
								fragments = append(fragments, CodeFragment{
									Path:       state.FocusFile,
									Content:    dfText,
									Kind:       FragmentDataFlow,
									Symbol:     cursorVar,
									Reason:     "data flow: variable " + cursorVar,
									TokenCost:  estimateTokens(dfText),
									ValueScore: 0.9,
								})
							}
						}
					}
				}

				// Control flow summary for complex functions (Explain/FixBug/Refactor).
				if fn != nil && (taskType == TaskExplain || taskType == TaskFixBug || taskType == TaskRefactor) {
					cfEntries := treesitter.ExtractControlFlow(lang, r.ts, content[fn.StartByte:fn.EndByte])
					if len(cfEntries) > 3 {
						cfText := formatControlFlowSummary(cfEntries, fn.StartLine, state.CursorLine)
						if cfText != "" {
							fragments = append(fragments, CodeFragment{
								Path:       state.FocusFile,
								Content:    cfText,
								Kind:       FragmentControlFlow,
								Symbol:     fn.Name,
								Reason:     "control flow structure of " + fn.Name,
								TokenCost:  estimateTokens(cfText),
								ValueScore: 0.7,
							})
						}
					}
				}

				// TaskType-specific LSP enhancements
				if taskType == TaskFixBug {
					diags, _ := r.lsp.Diagnostics(ctx, state.FocusFile)
					for _, d := range diags {
						if d.Severity <= DiagWarning {
							diagText := fmt.Sprintf("%s:%d:%d: [%s] %s", d.Path, d.Line, d.Column, d.Source, d.Message)
							fragments = append(fragments, CodeFragment{
								Path:       d.Path,
								StartLine:  d.Line,
								EndLine:    d.Line,
								Content:    diagText,
								Kind:       FragmentDiagnostic,
								Reason:     "LSP diagnostic (fix_bug)",
								TokenCost:  estimateTokens(diagText),
								ValueScore: 0.9,
							})
						}
					}
				}

				if taskType == TaskExplain {
					hover, _ := r.lsp.Hover(ctx, state.FocusFile, state.CursorLine, state.CursorCol)
					if hover != "" {
						fragments = append(fragments, CodeFragment{
							Path:       state.FocusFile,
							StartLine:  state.CursorLine,
							EndLine:    state.CursorLine,
							Content:    hover,
							Kind:       FragmentSignature,
							Reason:     "LSP hover type info (explain)",
							TokenCost:  estimateTokens(hover),
							ValueScore: 0.85,
						})
					}
				}
			}
		}
	}

	imports, _ := r.index.ListImports(ctx, state.FocusFile)
	for _, imp := range imports {
		syms, _ := r.index.SearchSymbols(ctx, imp, 5, langExts...)
		for _, sym := range syms {
			if sym.Kind == "function" || sym.Kind == "method" || sym.Kind == "type" || sym.Kind == "interface" {
				fragments = append(fragments, CodeFragment{
					Path:       sym.FilePath,
					StartLine:  sym.LineStart,
					EndLine:    sym.LineEnd,
					Content:    sym.Signature,
					Kind:       FragmentSignature,
					Symbol:     sym.Name,
					Reason:     "imported dependency signature",
					TokenCost:  estimateTokens(sym.Signature),
					ValueScore: 0.7,
				})
			}
		}
	}

	// CE-10: open tab signatures — inject symbol outlines from other open files
	tabSeen := map[string]bool{state.FocusFile: true}
	for _, tabFile := range state.OpenFiles {
		if tabSeen[tabFile] {
			continue
		}
		tabSeen[tabFile] = true
		tabSyms, _ := r.index.ListFileSymbols(ctx, tabFile)
		outline := buildSymbolOutline(tabSyms)
		if outline == "" {
			continue
		}
		fragments = append(fragments, CodeFragment{
			Path:       tabFile,
			Content:    outline,
			Kind:       FragmentOpenTab,
			Reason:     "open in editor tab",
			TokenCost:  estimateTokens(outline),
			ValueScore: 0.5,
		})
	}

	return fragments
}

func (r *Retriever) heuristicExpand(ctx context.Context, state EditorState, userMessage string, taskType TaskType) []CodeFragment {
	var fragments []CodeFragment

	switch taskType {
	case TaskExplain:
		words := extractIdentifiers(userMessage)
		focusLang := contextLanguageKey(state.FocusFile)
		// Limit matched symbols to avoid context pollution from cross-language hits
		// In mixed projects (e.g. Go + JS), the same identifier name can match
		// different-language symbols. Filter by focus file language.
		var deduped []string
		seen := make(map[string]bool)
		for _, w := range words {
			if seen[w] {
				continue
			}
			seen[w] = true
			deduped = append(deduped, w)
		}
		for _, w := range deduped {
			syms, _ := r.index.FindSymbol(ctx, w)
			for _, sym := range syms {
				// Cross-language guard: skip symbols whose file extension doesn't match
				// the focus file's language. This prevents e.g. "HandleRequest" in JavaScript
				// from polluting a Go-focused explanation.
				if focusLang != "" && contextLanguageKey(sym.FilePath) != focusLang {
					continue
				}
				content := r.readLines(sym.FilePath, sym.LineStart, sym.LineEnd)
				fragments = append(fragments, CodeFragment{
					Path:       sym.FilePath,
					StartLine:  sym.LineStart,
					EndLine:    sym.LineEnd,
					Content:    content,
					Kind:       FragmentRelevantCode,
					Symbol:     sym.Name,
					Reason:     "definition of " + sym.Name + " mentioned in message",
					TokenCost:  estimateTokens(content),
					ValueScore: 0.6,
				})
			}
		}

	case TaskImplement:
		if state.FocusFile != "" {
			dir := filepath.Dir(state.FocusFile)
			fragments = append(fragments, r.projectTreeFragment(dir)...)
		}

	case TaskFixBug:
		// heuristic-level LSP diagnostic expansion: inject surrounding context for error areas
		if r.lsp != nil && state.FocusFile != "" {
			diags, _ := r.lsp.Diagnostics(ctx, state.FocusFile)
			for _, d := range diags {
				if d.Severity > DiagWarning {
					continue
				}
				// Expand 3 lines above and below the diagnostic location
				startLine := max(d.Line-3, 1)
				endLine := d.Line + 3
				content := r.readLines(d.Path, startLine, endLine)
				if content == "" {
					continue
				}
				fragments = append(fragments, CodeFragment{
					Path:       d.Path,
					StartLine:  startLine,
					EndLine:    endLine,
					Content:    content,
					Kind:       FragmentDiagnostic,
					Symbol:     d.Source,
					Reason:     fmt.Sprintf("diagnostic context: %s (line %d)", d.Message, d.Line),
					TokenCost:  estimateTokens(content),
					ValueScore: 0.85,
				})
			}
		}

	case TaskRefactor:
		if state.FocusFile != "" {
			syms, _ := r.index.ListFileSymbols(ctx, state.FocusFile)
			if fn := findSymbolAtLine(syms, state.CursorLine); fn != nil {
				refs, _ := r.index.SearchSymbols(ctx, fn.Name, 20)
				for _, ref := range refs {
					if ref.FilePath == state.FocusFile {
						continue
					}
					content := r.readLines(ref.FilePath, ref.LineStart, ref.LineEnd)
					fragments = append(fragments, CodeFragment{
						Path:       ref.FilePath,
						StartLine:  ref.LineStart,
						EndLine:    ref.LineEnd,
						Content:    content,
						Kind:       FragmentRelevantCode,
						Symbol:     ref.Name,
						Reason:     "reference to " + ref.Name + " (refactor scope)",
						TokenCost:  estimateTokens(content),
						ValueScore: 0.5,
					})
				}
			}
		}

	case TaskReview, TaskCompletion, TaskGeneral:
		// handled by universal context below
	}

	// ── Universal context (all TaskTypes, including TaskGeneral) ──────────

	// CE-11: recent edits — inject code recently modified in other files
	now := time.Now().UnixMilli()
	const recentEditMaxAge = 10 * 60 * 1000 // 10 minutes in ms
	recentSeen := map[string]bool{}
	if state.FocusFile != "" {
		recentSeen[state.FocusFile] = true
	}
	for _, edit := range state.RecentEdits {
		if recentSeen[edit.Path] {
			continue
		}
		recentSeen[edit.Path] = true
		elapsed := now - edit.Timestamp
		if elapsed > recentEditMaxAge || elapsed < 0 {
			continue
		}
		content := r.readLines(edit.Path, edit.StartLine, edit.EndLine)
		if content == "" {
			continue
		}
		// Linear decay: 0.65 at 0min → 0.3 at 10min
		decay := 1.0 - float64(elapsed)/float64(recentEditMaxAge)
		if decay < 0 {
			decay = 0
		}
		score := 0.3 + 0.35*decay

		agoMin := elapsed / 60000
		reason := fmt.Sprintf("recently edited (%dm ago)", agoMin)
		fragments = append(fragments, CodeFragment{
			Path:       edit.Path,
			StartLine:  edit.StartLine,
			EndLine:    edit.EndLine,
			Content:    content,
			Kind:       FragmentRecentEdit,
			Reason:     reason,
			TokenCost:  estimateTokens(content),
			ValueScore: score,
		})
	}

	// CE-12: git diff context — inject working tree changes summary
	if r.gitDiff != nil && r.gitDiff.HasChanges {
		gitScore := 0.5
		if taskType == TaskFixBug {
			gitScore = 0.8
		}
		fragments = append(fragments, CodeFragment{
			Content:    r.gitDiff.Summary,
			Kind:       FragmentGitDiff,
			Reason:     "git working tree changes",
			TokenCost:  estimateTokens(r.gitDiff.Summary),
			ValueScore: gitScore,
		})
	}

	// INV-XLANG-04: cross-language API route mapping (queries code.db edges)
	if r.index != nil && state.FocusFile != "" {
		lang, ok := treesitter.DetectLang(state.FocusFile)
		if ok {
			focusContent, _ := r.readFile(state.FocusFile)
			if len(focusContent) > 0 {
				db := r.index.DB()

				// Direction 1: focus file has API calls → inject backend handlers
				apiCalls := treesitter.ExtractAPICalls(lang, r.ts, focusContent, state.FocusFile)
				for _, call := range apiCalls {
					rows, err := db.QueryContext(ctx,
						`SELECT s.file_path, s.name, s.line_start, e.target_name
						 FROM edges e JOIN symbols s ON e.source_id = s.id
						 WHERE e.kind = 'handles' AND s.kind = 'route'`)
					if err != nil {
						continue
					}
					for rows.Next() {
						var filePath, routeName, targetName string
						var line int
						if rows.Scan(&filePath, &routeName, &line, &targetName) != nil {
							continue
						}
						parts := strings.SplitN(routeName, " ", 2)
						if len(parts) != 2 || !pathMatches(parts[1], call.URL) {
							continue
						}
						sig := r.readLines(filePath, line, line+10)
						if sig == "" {
							continue
						}
						fragments = append(fragments, CodeFragment{
							Path:       filePath,
							StartLine:  line,
							EndLine:    line + 10,
							Content:    sig,
							Kind:       FragmentCrossLang,
							Symbol:     targetName,
							Reason:     fmt.Sprintf("backend handler for %s %s", call.Method, call.URL),
							TokenCost:  estimateTokens(sig),
							ValueScore: 0.6,
						})
					}
					rows.Close()
				}

				// Direction 2: focus file has route registrations → inject frontend callers
				routes := treesitter.ExtractRoutes(lang, r.ts, focusContent, state.FocusFile)
				for _, route := range routes {
					rows, err := db.QueryContext(ctx,
						`SELECT s_caller.file_path, s_caller.line_start, s_route.name
						 FROM edges e
						 JOIN symbols s_route ON e.target_id = s_route.id
						 JOIN symbols s_caller ON e.source_id = s_caller.id
						 WHERE e.kind = 'http_calls' AND s_route.kind = 'route'`)
					if err != nil {
						continue
					}
					for rows.Next() {
						var callerFile, routeName string
						var callerLine int
						if rows.Scan(&callerFile, &callerLine, &routeName) != nil {
							continue
						}
						parts := strings.SplitN(routeName, " ", 2)
						if len(parts) != 2 || !pathMatches(parts[1], route.Path) {
							continue
						}
						callerContent := r.readLines(callerFile, max(callerLine-2, 0), callerLine+2)
						if callerContent == "" {
							continue
						}
						fragments = append(fragments, CodeFragment{
							Path:       callerFile,
							StartLine:  callerLine,
							EndLine:    callerLine + 2,
							Content:    callerContent,
							Kind:       FragmentCrossLang,
							Reason:     fmt.Sprintf("frontend caller of %s %s", parts[0], route.Path),
							TokenCost:  estimateTokens(callerContent),
							ValueScore: 0.6,
						})
					}
					rows.Close()
				}
			}
		}
	}

	return fragments
}

// applyProximityBoost adjusts ValueScore of each fragment based on how close
// it is to the focus file in the directory tree. Same-directory fragments
// keep their full score; distant fragments are slightly penalized.
// This ensures that in large workspaces, nearby files are preferred over
// structurally distant but equally scored files.
func applyProximityBoost(fragments []CodeFragment, focusFile string) {
	if focusFile == "" {
		return
	}
	for i := range fragments {
		if fragments[i].Path == focusFile {
			continue
		}
		proximity := PathProximity(focusFile, fragments[i].Path)
		fragments[i].ValueScore = fragments[i].ValueScore*0.7 + fragments[i].ValueScore*proximity*0.3
	}
}

// applyStrategyWeights adjusts fragment ValueScores using ContextStrategy CKGWeights.
// Each fragment's source category maps to a strategy weight that amplifies or dampens
// its priority relative to other fragments for this TaskType.
func applyStrategyWeights(fragments []CodeFragment, strategy *ContextStrategy) {
	if strategy == nil || len(strategy.CKGWeights) == 0 {
		return
	}
	for i := range fragments {
		cat := fragmentCategory(fragments[i].Kind)
		if cat == "" {
			continue
		}
		weight := strategy.ToolWeight(cat)
		if weight > 0 {
			fragments[i].ValueScore *= weight
		}
	}
}

// applyCommunityBias adjusts ValueScores based on Leiden community membership.
// Same-community fragments get a boost defined by ContextStrategy.CommunityBias.
func applyCommunityBias(fragments []CodeFragment, focusFile string, strategy *ContextStrategy, index *CodeIndex) {
	if strategy == nil || strategy.CommunityBias <= 1.0 || index == nil {
		return
	}
	focusCommunity := index.LeidenCommunity(focusFile)
	if focusCommunity <= 0 {
		return
	}
	for i := range fragments {
		fragCommunity := index.LeidenCommunity(fragments[i].Path)
		if fragCommunity > 0 && fragCommunity == focusCommunity {
			fragments[i].ValueScore = strategy.ApplyCommunityBias(fragments[i].ValueScore, true)
		}
	}
}

// fragmentCategory maps a CodeFragment.Kind to the strategy tool-weight key.
func fragmentCategory(kind FragmentKind) string {
	switch kind {
	case FragmentSignature:
		return "find_callers"
	case FragmentLSPRef:
		return "find_callers"
	case FragmentDataFlow:
		return "impact_analysis"
	case FragmentRelevantCode:
		return "semantic_search"
	case FragmentConstraint:
		return "check_constraints"
	case FragmentGitDiff:
		return "find_co_changed_files"
	case FragmentSiblingImpl:
		return "find_implementations"
	case FragmentProjectMap:
		return "get_architecture_overview"
	case FragmentCrossLang:
		return "impact_analysis"
	case FragmentControlFlow:
		return "impact_analysis"
	default:
		return ""
	}
}

func budgetPack(fragments []CodeFragment, budget int) []CodeFragment {
	if budget <= 0 {
		return fragments
	}

	sort.Slice(fragments, func(i, j int) bool {
		ri := fragments[i].ValueScore / float64(max(fragments[i].TokenCost, 1))
		rj := fragments[j].ValueScore / float64(max(fragments[j].TokenCost, 1))
		return ri > rj
	})

	var result []CodeFragment
	remaining := budget
	for _, f := range fragments {
		if f.TokenCost <= remaining {
			result = append(result, f)
			remaining -= f.TokenCost
		}
	}
	return result
}

func buildSymbolOutline(syms []SymbolEntry) string {
	var sb strings.Builder
	for _, sym := range syms {
		if sym.Kind == "import" {
			continue
		}
		sb.WriteString(sym.Signature)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (r *Retriever) readLines(path string, startLine, endLine int) string {
	content, err := r.readFile(path)
	if err != nil {
		return ""
	}
	return extractLines(string(content), startLine, endLine)
}

func extractLines(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}
	if startLine > endLine {
		return ""
	}
	return strings.Join(lines[startLine:endLine+1], "\n")
}

func extractIdentifiers(text string) []string {
	var ids []string
	word := strings.Builder{}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			word.WriteRune(r)
		} else {
			if w := word.String(); len(w) >= 3 && (w[0] >= 'A' && w[0] <= 'Z') {
				ids = append(ids, w)
			}
			word.Reset()
		}
	}
	if w := word.String(); len(w) >= 3 && (w[0] >= 'A' && w[0] <= 'Z') {
		ids = append(ids, w)
	}
	return ids
}

// contextLanguageKey returns a language key for disambiguation based on file extension.
func contextLanguageKey(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".ts", ".tsx", ".jsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	default:
		return ext
	}
}

func findSymbolAtLine(syms []SymbolEntry, line int) *SymbolEntry {
	for i := range syms {
		if line >= syms[i].LineStart && line <= syms[i].LineEnd {
			return &syms[i]
		}
	}
	return nil
}

func (r *Retriever) projectTreeFragment(dir string) []CodeFragment {
	var sb strings.Builder
	var entries []os.DirEntry
	var err error
	if r.fp != nil {
		entries, err = r.fp.ReadDir(context.Background(), dir)
	} else {
		entries, err = os.ReadDir(dir)
	}
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			sb.WriteString(e.Name() + "/\n")
		} else {
			sb.WriteString(e.Name() + "\n")
		}
	}
	content := sb.String()
	if content == "" {
		return nil
	}
	return []CodeFragment{{
		Path:       dir,
		Content:    content,
		Kind:       FragmentProjectTree,
		Reason:     "project structure around focus file",
		TokenCost:  estimateTokens(content),
		ValueScore: 0.2,
	}}
}

// autoExpand generates additional fragments when the initial retrieval
// underutilizes the token budget (<50%). Prioritizes call graph adjacent
// nodes (2nd-level callees, callers of callees) over same-file siblings.
func (r *Retriever) autoExpand(ctx context.Context, state EditorState, existing []CodeFragment, remainingBudget int) []CodeFragment {
	var extra []CodeFragment
	if state.FocusFile == "" || remainingBudget <= 0 {
		return extra
	}

	included := make(map[string]struct{})
	for _, f := range existing {
		if f.Symbol != "" {
			included[f.Symbol] = struct{}{}
		}
	}

	// Priority 1: 2nd-level call chain — functions called by functions
	// that the cursor function calls (depth 2).
	for _, f := range existing {
		if f.Reason == "" || !strings.Contains(f.Reason, "1-level call chain") {
			continue
		}
		secondLevel, _ := r.index.SearchSymbols(ctx, f.Symbol, 5)
		for _, sym := range secondLevel {
			if _, ok := included[sym.Name]; ok {
				continue
			}
			if sym.Name == f.Symbol {
				continue
			}
			extra = append(extra, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    sym.Signature,
				Kind:       FragmentSignature,
				Symbol:     sym.Name,
				Reason:     "related to " + f.Symbol + " (graph expansion)",
				TokenCost:  estimateTokens(sym.Signature),
				ValueScore: 0.25,
			})
			included[sym.Name] = struct{}{}
		}
	}

	// Priority 2: same-file siblings (fallback when call graph is sparse)
	if len(extra) < 5 {
		syms, _ := r.index.ListFileSymbols(ctx, state.FocusFile)
		for _, sym := range syms {
			if _, ok := included[sym.Name]; ok {
				continue
			}
			if sym.Kind != "function" && sym.Kind != "method" && sym.Kind != "type" {
				continue
			}
			extra = append(extra, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    sym.Signature,
				Kind:       FragmentSignature,
				Symbol:     sym.Name,
				Reason:     "same-file sibling (budget expansion)",
				TokenCost:  estimateTokens(sym.Signature),
				ValueScore: 0.2,
			})
			included[sym.Name] = struct{}{}
		}
	}

	return extra
}

func filterByLang(syms []SymbolEntry, exts []string) []SymbolEntry {
	if len(exts) == 0 {
		return syms
	}
	var filtered []SymbolEntry
	for _, s := range syms {
		ext := strings.ToLower(filepath.Ext(s.FilePath))
		for _, allowed := range exts {
			if ext == allowed {
				filtered = append(filtered, s)
				break
			}
		}
	}
	if len(filtered) == 0 {
		return syms
	}
	return filtered
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// extractParentType attempts to extract the receiver type name from an LSP
// Hover response. For Go methods, hover typically returns something like:
//
//	"func (*sync.Mutex).Lock()"  → "Mutex"
//	"func (io.Reader).Read(p []byte) (n int, err error)"  → "Reader"
//
// Falls back to parsing the receiver expression (e.g. "s.mu" → "mu").
// identifyVarAtCursor finds which variable the cursor is on, using the
// extracted var flow entries. Returns empty if cursor is not on a known variable.
func identifyVarAtCursor(flow []treesitter.VarFlowEntry, cursorLine, cursorCol int) string {
	bestDist := 999
	bestName := ""
	for _, e := range flow {
		if e.Line != cursorLine {
			continue
		}
		dist := cursorCol - e.Col
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist && dist <= len(e.Name)+5 {
			bestDist = dist
			bestName = e.Name
		}
	}
	return bestName
}

// formatVarFlowChain formats the def-use chain of a variable for LLM context.
func formatVarFlowChain(flow []treesitter.VarFlowEntry, varName string, fnStartLine int) string {
	var sb strings.Builder
	sb.WriteString("[Data Flow: variable `" + varName + "`]\n")
	count := 0
	for _, e := range flow {
		if e.Name != varName {
			continue
		}
		absLine := fnStartLine + e.Line + 1 // 1-based for display
		switch e.Kind {
		case treesitter.VarFlowDef:
			sb.WriteString(fmt.Sprintf("DEF  line %d: %s := %s\n", absLine, e.Name, e.RHSExpr))
		case treesitter.VarFlowDefUse:
			sb.WriteString(fmt.Sprintf("SET  line %d: %s = %s\n", absLine, e.Name, e.RHSExpr))
		case treesitter.VarFlowUse:
			sb.WriteString(fmt.Sprintf("USE  line %d: %s\n", absLine, e.Name))
		case treesitter.VarFlowReturn:
			sb.WriteString(fmt.Sprintf("RET  line %d: return %s\n", absLine, e.Name))
		case treesitter.VarFlowParam:
			sb.WriteString(fmt.Sprintf("PARAM line %d: %s\n", absLine, e.Name))
		}
		count++
		if count >= 20 {
			sb.WriteString("... (truncated)\n")
			break
		}
	}
	if count == 0 {
		return ""
	}
	return sb.String()
}

// formatControlFlowSummary builds a text summary of a function's control flow
// structure, highlighting the path the cursor is on and error-handling paths.
func formatControlFlowSummary(entries []treesitter.ControlFlowEntry, fnStartLine, cursorLine int) string {
	var sb strings.Builder
	sb.WriteString("[Control Flow Structure]\n")

	for _, e := range entries {
		absLine := fnStartLine + e.Line + 1
		indent := strings.Repeat("  ", e.Depth)
		marker := " "
		if absLine == cursorLine+1 {
			marker = ">"
		}

		switch e.Kind {
		case treesitter.CFNodeIf:
			errTag := ""
			if e.IsError {
				errTag = " [ERROR PATH]"
			}
			sb.WriteString(fmt.Sprintf("%s%sL%d: if %s%s\n", marker, indent, absLine, e.Condition, errTag))
		case treesitter.CFNodeElse:
			sb.WriteString(fmt.Sprintf("%s%sL%d: else\n", marker, indent, absLine))
		case treesitter.CFNodeElseIf:
			sb.WriteString(fmt.Sprintf("%s%sL%d: else if\n", marker, indent, absLine))
		case treesitter.CFNodeFor:
			sb.WriteString(fmt.Sprintf("%s%sL%d: for %s\n", marker, indent, absLine, e.Condition))
		case treesitter.CFNodeSwitch:
			sb.WriteString(fmt.Sprintf("%s%sL%d: switch %s\n", marker, indent, absLine, e.Condition))
		case treesitter.CFNodeCase:
			sb.WriteString(fmt.Sprintf("%s%sL%d: case\n", marker, indent, absLine))
		case treesitter.CFNodeDefault:
			sb.WriteString(fmt.Sprintf("%s%sL%d: default\n", marker, indent, absLine))
		case treesitter.CFNodeReturn:
			sb.WriteString(fmt.Sprintf("%s%sL%d: return\n", marker, indent, absLine))
		case treesitter.CFNodeDefer:
			sb.WriteString(fmt.Sprintf("%s%sL%d: defer\n", marker, indent, absLine))
		case treesitter.CFNodePanic:
			sb.WriteString(fmt.Sprintf("%s%sL%d: panic/fatal [TERMINAL]\n", marker, indent, absLine))
		case treesitter.CFNodeTry:
			sb.WriteString(fmt.Sprintf("%s%sL%d: try\n", marker, indent, absLine))
		case treesitter.CFNodeCatch:
			sb.WriteString(fmt.Sprintf("%s%sL%d: catch [ERROR PATH]\n", marker, indent, absLine))
		case treesitter.CFNodeFinally:
			sb.WriteString(fmt.Sprintf("%s%sL%d: finally\n", marker, indent, absLine))
		case treesitter.CFNodeSelect:
			sb.WriteString(fmt.Sprintf("%s%sL%d: select\n", marker, indent, absLine))
		case treesitter.CFNodeBreak:
			sb.WriteString(fmt.Sprintf("%s%sL%d: break\n", marker, indent, absLine))
		case treesitter.CFNodeContinue:
			sb.WriteString(fmt.Sprintf("%s%sL%d: continue\n", marker, indent, absLine))
		case treesitter.CFNodeGoto:
			sb.WriteString(fmt.Sprintf("%s%sL%d: goto\n", marker, indent, absLine))
		}
	}

	// Summary stats
	errorPaths := 0
	returns := 0
	for _, e := range entries {
		if e.IsError || e.Kind == treesitter.CFNodeCatch {
			errorPaths++
		}
		if e.Kind == treesitter.CFNodeReturn {
			returns++
		}
	}
	if errorPaths > 0 || returns > 1 {
		sb.WriteString(fmt.Sprintf("\nSummary: %d return points, %d error-handling paths\n", returns, errorPaths))
	}

	// Change impact: identify which branch context the cursor is in
	cursorRelLine := cursorLine - fnStartLine
	var enclosingBranches []string
	for _, e := range entries {
		if e.Line > cursorRelLine {
			break
		}
		switch e.Kind {
		case treesitter.CFNodeIf:
			tag := "if " + e.Condition
			if e.IsError {
				tag += " [error path]"
			}
			enclosingBranches = append(enclosingBranches, tag)
		case treesitter.CFNodeElse, treesitter.CFNodeElseIf:
			enclosingBranches = append(enclosingBranches, string(e.Kind))
		case treesitter.CFNodeFor:
			enclosingBranches = append(enclosingBranches, "for "+e.Condition)
		case treesitter.CFNodeSwitch:
			enclosingBranches = append(enclosingBranches, "switch "+e.Condition)
		case treesitter.CFNodeTry:
			enclosingBranches = append(enclosingBranches, "try")
		case treesitter.CFNodeCatch:
			enclosingBranches = append(enclosingBranches, "catch [error path]")
		default:
			// CFNodeCase/Default/Return/Defer/Panic/Finally/Break/Continue/Goto/Select — not enclosing branches
		}
	}
	if len(enclosingBranches) > 0 {
		// Keep only the deepest 3 enclosing branches
		start := 0
		if len(enclosingBranches) > 3 {
			start = len(enclosingBranches) - 3
		}
		sb.WriteString(fmt.Sprintf("Cursor context: %s\n", strings.Join(enclosingBranches[start:], " → ")))
	}

	return sb.String()
}

func extractParentType(hoverInfo, receiver string) string {
	if hoverInfo != "" {
		// Pattern: "func (*pkg.Type).Method" or "func (pkg.Type).Method"
		// Also handles "func (Type).Method" without package prefix.
		if idx := strings.Index(hoverInfo, ")."); idx > 0 {
			prefix := hoverInfo[:idx]
			// Find the type name (last identifier before the closing paren)
			prefix = strings.TrimRight(prefix, " ")
			// Remove pointer prefix
			prefix = strings.TrimPrefix(prefix, "func (")
			prefix = strings.TrimPrefix(prefix, "func (*")
			prefix = strings.TrimPrefix(prefix, "*")
			// "sync.Mutex" → "Mutex", "Reader" → "Reader"
			if dotIdx := strings.LastIndex(prefix, "."); dotIdx >= 0 {
				return prefix[dotIdx+1:]
			}
			return prefix
		}
	}
	// Fallback: use the last segment of the receiver expression.
	// "s.mu" → "mu", "self.client" → "client"
	if receiver != "" {
		if dotIdx := strings.LastIndex(receiver, "."); dotIdx >= 0 {
			return receiver[dotIdx+1:]
		}
	}
	return ""
}

// matchesParent checks if a symbol's parent type matches the disambiguated type.
// Handles partial matches: "Mutex" matches "sync.Mutex" or just "Mutex".
func matchesParent(symbolParent, targetType string) bool {
	if symbolParent == "" || targetType == "" {
		return false
	}
	if symbolParent == targetType {
		return true
	}
	// Strip pointer and package prefix for comparison
	clean := func(s string) string {
		s = strings.TrimPrefix(s, "*")
		if dot := strings.LastIndex(s, "."); dot >= 0 {
			return s[dot+1:]
		}
		return s
	}
	return clean(symbolParent) == clean(targetType)
}

// ConsumePattern describes how a caller consumes a function's return value.
type ConsumePattern struct {
	Kind    string // "channel_range", "type_switch", "error_check", "assign"
	Line    int
	Pattern string // the relevant code snippet
}

// extractConsumePatterns analyzes a caller's body using tree-sitter AST to
// identify how it consumes targetFn's return value. Three patterns are recognized:
//   - channel_range:  `for ... := range <call_to_targetFn>`
//   - type_switch:    `switch ... := <call_to_targetFn>.(type)` or `switch <expr>.Type`
//   - error_check:    `if err := <call_to_targetFn>; err != nil`
//
// Falls back to text scanning when tree-sitter parsing fails.
func (r *Retriever) extractConsumePatterns(filePath string, startLine, endLine int, targetFn string) []ConsumePattern {
	body := r.readLines(filePath, startLine, endLine)
	if body == "" {
		return nil
	}

	lang, ok := treesitter.DetectLang(filePath)
	if !ok {
		return extractConsumePatternsText(body, startLine, targetFn)
	}

	pool := treesitter.NewParserPool()
	defer pool.Close()
	tree, err := pool.Parse(lang, []byte(body), nil)
	if err != nil {
		return extractConsumePatternsText(body, startLine, targetFn)
	}
	defer tree.Close()

	// AST-guided consume pattern extraction (multi-language).
	// Walk top-level statements looking for language-specific consumption patterns.
	var patterns []ConsumePattern
	lines := strings.Split(body, "\n")
	root := tree.RootNode()
	walkConsumeNodes(root, lines, startLine, targetFn, &patterns)

	if len(patterns) == 0 {
		return extractConsumePatternsText(body, startLine, targetFn)
	}
	return patterns
}

// extractConsumePatternsText is the text-scanning fallback, enhanced to reduce
// false positives by requiring the target function call to be a standalone
// identifier (not a substring of another name).
func extractConsumePatternsText(body string, startLine int, targetFn string) []ConsumePattern {
	var patterns []ConsumePattern
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		absLine := startLine + i

		// Require targetFn to appear as a call (followed by '(' or preceded by '.')
		if !containsCall(trimmed, targetFn) {
			continue
		}

		if isForRangePattern(trimmed) {
			patterns = append(patterns, ConsumePattern{
				Kind:    "channel_range",
				Line:    absLine,
				Pattern: trimmed,
			})
		}
		if isSwitchPattern(trimmed) {
			patterns = append(patterns, ConsumePattern{
				Kind:    "type_switch",
				Line:    absLine,
				Pattern: trimmed,
			})
		}
		if isErrorCheckPattern(trimmed) {
			patterns = append(patterns, ConsumePattern{
				Kind:    "error_check",
				Line:    absLine,
				Pattern: trimmed,
			})
		}
	}
	return patterns
}

// containsCall checks if targetFn appears as a function call (not a substring).
// Matches: targetFn(, .targetFn(, but not longerTargetFn(.
func containsCall(line, targetFn string) bool {
	idx := strings.Index(line, targetFn)
	if idx < 0 {
		return false
	}
	endIdx := idx + len(targetFn)
	if endIdx >= len(line) {
		return false
	}
	// Must be followed by '(' to be a call
	if line[endIdx] != '(' {
		// Also accept '.targetFn' patterns (selector)
		if endIdx < len(line) && line[endIdx] == '.' {
			return true
		}
		return false
	}
	// Must not be preceded by an alphanumeric (would be part of longer name)
	if idx > 0 {
		prev := line[idx-1]
		if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
			return false
		}
	}
	return true
}

func isForRangePattern(line string) bool {
	return strings.HasPrefix(line, "for") && strings.Contains(line, "range")
}

func isSwitchPattern(line string) bool {
	return strings.HasPrefix(line, "switch")
}

func isErrorCheckPattern(line string) bool {
	return strings.Contains(line, "if") && strings.Contains(line, "err") && strings.Contains(line, "!= nil")
}

// walkConsumeNodes recursively walks AST nodes looking for language-specific
// consumption patterns. Supports:
//   - Go: for/range, type_switch, if err != nil
//   - Python: for_statement (async_for), with_statement, try/except, if/isinstance
//   - TypeScript/JS: for_of, await_expression, try_statement, .then/.catch
//   - Rust: for_expression, match_expression, if_let, ? operator
//   - Java: for_statement, try_statement, if (instanceof)
func walkConsumeNodes(node *tree_sitter.Node, lines []string, startLine int, targetFn string, patterns *[]ConsumePattern) {
	if node == nil {
		return
	}

	grammarName := node.GrammarName()
	childLine := int(node.StartPosition().Row)
	absLine := startLine + childLine

	var lineTxt string
	if childLine >= 0 && childLine < len(lines) {
		lineTxt = strings.TrimSpace(lines[childLine])
	}

	if lineTxt != "" && containsCall(lineTxt, targetFn) {
		if kind := classifyConsumePattern(grammarName, lineTxt); kind != "" {
			*patterns = append(*patterns, ConsumePattern{Kind: kind, Line: absLine, Pattern: lineTxt})
		}
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			walkConsumeNodes(child, lines, startLine, targetFn, patterns)
		}
	}
}

// classifyConsumePattern maps an AST node type + line content to a consume pattern kind.
func classifyConsumePattern(grammarName, lineTxt string) string {
	switch grammarName {
	// ── Iteration patterns ──
	case "for_statement", "for_range_clause":
		// Go: for x := range fn()
		if strings.Contains(lineTxt, "range") {
			return "iteration"
		}
		return "iteration"
	case "for_in_statement":
		// Python: for x in fn()
		return "iteration"
	case "for_of_statement":
		// TypeScript/JS: for (const x of fn())
		return "iteration"
	case "for_expression":
		// Rust: for x in fn()
		return "iteration"

	// ── Async iteration / context patterns ──
	case "with_statement":
		// Python: with fn() as x / async with
		return "context_manager"
	case "await_expression":
		// TS/JS: await fn()
		return "async_await"

	// ── Type narrowing patterns ──
	case "type_switch_statement", "expression_switch_statement":
		// Go: switch x := fn().(type)
		return "type_narrowing"
	case "match_expression", "match_statement":
		// Rust: match fn() { ... }
		return "type_narrowing"
	case "if_statement":
		// Go: if err := fn(); err != nil
		if strings.Contains(lineTxt, "err") && strings.Contains(lineTxt, "!= nil") {
			return "error_check"
		}
		// Python: if isinstance(fn(), Type)
		if strings.Contains(lineTxt, "isinstance") {
			return "type_narrowing"
		}
		// Rust: if let Some(x) = fn()
		if strings.Contains(lineTxt, "if let") {
			return "type_narrowing"
		}
		return ""
	case "if_let_expression":
		// Rust: if let Ok(x) = fn()
		return "type_narrowing"

	// ── Error handling patterns ──
	case "try_statement":
		// Python: try: fn() except: / TS: try { fn() } catch
		return "error_handling"
	case "try_expression":
		// Rust: fn()?
		return "error_handling"

	// ── Callback/promise patterns ──
	case "call_expression":
		// TS/JS: fn().then(...).catch(...)
		if strings.Contains(lineTxt, ".then(") || strings.Contains(lineTxt, ".catch(") {
			return "promise_chain"
		}
		return ""
	}
	return ""
}

// fileTarget represents a file reference extracted from user text.
// Raw preserves any path prefix (e.g. "loop/engine.go"), Base is just the filename.
type fileTarget struct {
	Raw  string // original reference as written by user (may include path segments)
	Base string // filepath.Base(Raw)
}

// isFocusFileUserTarget determines whether the focusFile is the confirmed target
// of the user's request. Returns true ONLY when:
//   - The user does not mention any file name (focusFile is general context)
//   - The user mentions a path-qualified reference that matches the focusFile
//
// A bare-basename match (user says "engine.go" and focusFile is also named
// engine.go) returns FALSE because it's ambiguous — multiple files may share
// the same basename. The model must use tools (glob/grep) to disambiguate.
func isFocusFileUserTarget(focusFile, userMessage string) bool {
	targets := extractFileTargets(userMessage)
	if len(targets) == 0 {
		return true
	}

	for _, t := range targets {
		if t.Raw != t.Base {
			// User gave a path-qualified reference (e.g. "loop/engine.go").
			// Check if focusFile path contains this fragment.
			if strings.Contains(focusFile, t.Raw) ||
				strings.Contains(strings.ToLower(focusFile), strings.ToLower(t.Raw)) {
				return true
			}
		}
		// Bare basename match is NOT sufficient to confirm the focusFile is the
		// target — skip it. This forces demotion when multiple files share a name.
	}
	return false
}

// extractFileTargets extracts file name references from user text.
// Preserves path prefixes when present (e.g. "internal/engine.go" → Raw="internal/engine.go").
func extractFileTargets(text string) []fileTarget {
	var targets []fileTarget
	seen := make(map[string]bool)

	words := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';' ||
			r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '\'' || r == '"' || r == '`'
	})

	exts := []string{
		".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java",
		".c", ".cpp", ".h", ".hpp", ".cs", ".rb", ".swift", ".kt",
		".vue", ".svelte", ".md", ".yaml", ".yml", ".json", ".toml",
	}

	for _, w := range words {
		w = strings.TrimRight(w, ".,;:!?")
		for _, ext := range exts {
			if strings.HasSuffix(w, ext) && len(w) > len(ext) {
				base := filepath.Base(w)
				if !seen[w] {
					seen[w] = true
					targets = append(targets, fileTarget{Raw: w, Base: base})
				}
				break
			}
		}
	}
	return targets
}
