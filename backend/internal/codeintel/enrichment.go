package codeintel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

// LLMEnricher generates one-sentence semantic summaries for exported functions.
// This is an optional Pass 6.5 — skipped when no LLM provider is available.
//
// INV-P5-01: Summary errors or missing do NOT affect graph structure correctness.
// INV-P5-05: Provider unavailable → skip entirely (no error).
// INV-P5-07: Pass 6.5 does not block Pass 10 Dump.
type LLMEnricher struct {
	GenerateFn     func(ctx context.Context, prompt string) (string, error)
	MaxConcurrency int // default 4
}

// SelectEnrichmentTargets selects which nodes need LLM summary generation.
// Criteria: exported + (function or method) + body > 5 lines + no existing valid summary.
func SelectEnrichmentTargets(buf *GraphBuffer) []*Node {
	buf.mu.RLock()
	defer buf.mu.RUnlock()

	var targets []*Node
	for _, n := range buf.Nodes {
		if !n.Exported {
			continue
		}
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		if (n.LineEnd - n.LineStart) <= 5 {
			continue
		}
		// Skip if summary is already valid for current content.
		if n.Summary != "" && n.SummaryHash == contentHashShort(n.ContentHash) {
			continue
		}
		targets = append(targets, n)
	}
	return targets
}

// Enrich generates summaries for selected nodes using the LLM.
// Uses content_hash to skip unchanged functions (cache hit).
// Returns the number of nodes enriched.
func (e *LLMEnricher) Enrich(ctx context.Context, buf *GraphBuffer) int {
	if e.GenerateFn == nil {
		return 0 // INV-P5-05: no provider → skip
	}
	targets := SelectEnrichmentTargets(buf)
	if len(targets) == 0 {
		return 0
	}

	concurrency := e.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var count int32

	for _, node := range targets {
		sem <- struct{}{}
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			prompt := buildEnrichmentPrompt(n)
			summary, err := e.GenerateFn(ctx, prompt)
			if err != nil {
				slog.Debug("[enrichment] LLM call failed", "node", n.Name, "err", err)
				return
			}
			summary = strings.TrimSpace(summary)
			if len(summary) > 200 {
				runes := []rune(summary)
				if len(runes) > 200 {
					summary = string(runes[:200])
				}
			}

			buf.mu.Lock()
			n.Summary = summary
			n.SummaryHash = contentHashShort(n.ContentHash)
			buf.mu.Unlock()
			atomic.AddInt32(&count, 1)
		}(node)
	}
	wg.Wait()
	return int(atomic.LoadInt32(&count))
}

func buildEnrichmentPrompt(n *Node) string {
	var sb strings.Builder
	sb.WriteString("Describe this function's business purpose in one sentence (max 30 words):\n")
	if n.Signature != "" {
		sb.WriteString(n.Signature)
		sb.WriteString("\n")
	} else {
		sb.WriteString(n.Name)
		sb.WriteString("\n")
	}
	return sb.String()
}

// contentHashShort produces a short hex digest from an input string,
// used as the cache key linking a summary to the function content version.
func contentHashShort(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}
