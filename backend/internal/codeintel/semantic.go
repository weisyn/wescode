package codeintel

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"math/rand"
	"sort"
	"strings"
)

const (
	riDimensions         = 768
	riNonZero            = 8 // 4 × +1, 4 × -1
	riWindowSize         = 10
	weightRI             = 0.40
	weightAPI            = 0.35
	weightType           = 0.25
	maxSemanticFunctions = 5000
)

// ComputeSemanticEdges generates SEMANTICALLY_RELATED edges using three
// model-free signals. Zero API calls, zero cost, zero privacy leakage.
//
// Signal 1: Random Indexing — 768-dim sparse vectors with co-occurrence window
// Signal 2: API Signature — shared callee set Jaccard similarity
// Signal 3: Type Signature — shared parameter/return type hash
func ComputeSemanticEdges(buf *GraphBuffer, threshold float64) error {
	if threshold <= 0 {
		threshold = 0.7
	}

	buf.mu.RLock()

	type funcInfo struct {
		qname   string
		riVec   []float64
		callees map[string]bool
		typeSig string
	}

	vocab := newRIVocab()

	funcs := make([]funcInfo, 0, len(buf.Nodes)/2)
	for qname, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		tokens := extractTokens(n.Signature + " " + n.Name)
		fi := funcInfo{
			qname:   qname,
			riVec:   buildContextVector(vocab, tokens),
			callees: make(map[string]bool),
			typeSig: typeSignatureHash(n.Signature),
		}
		funcs = append(funcs, fi)
	}

	calleeMap := make(map[string]map[string]bool)
	for _, e := range buf.Edges {
		if e.Kind != EdgeCall {
			continue
		}
		if calleeMap[e.SourceQName] == nil {
			calleeMap[e.SourceQName] = make(map[string]bool)
		}
		name := e.TargetName
		if name == "" {
			name = e.TargetQName
		}
		calleeMap[e.SourceQName][name] = true
	}
	for i := range funcs {
		if cs, ok := calleeMap[funcs[i].qname]; ok {
			funcs[i].callees = cs
		}
	}

	if len(funcs) > maxSemanticFunctions {
		sort.Slice(funcs, func(i, j int) bool { return len(funcs[i].callees) > len(funcs[j].callees) })
		funcs = funcs[:maxSemanticFunctions]
		slog.Info("semantic: truncated to top-N functions by callee count", "n", maxSemanticFunctions)
	}

	var newEdges []Edge
	for i := 0; i < len(funcs); i++ {
		for j := i + 1; j < len(funcs); j++ {
			fi, fj := funcs[i], funcs[j]

			ni := buf.Nodes[fi.qname]
			nj := buf.Nodes[fj.qname]
			if ni != nil && nj != nil && ni.FilePath == nj.FilePath {
				continue
			}

			sim := weightedSimilarity(fi.riVec, fj.riVec, fi.callees, fj.callees, fi.typeSig, fj.typeSig)
			if sim >= threshold {
				newEdges = append(newEdges, Edge{
					SourceQName: fi.qname,
					TargetQName: fj.qname,
					TargetName:  fj.qname,
					Kind:        EdgeSemanticRelated,
					// `exact` is about the binding, not the relation: both
					// endpoints are buffer nodes this loop already holds, so
					// no name was looked up. How related they are is the score
					// — a real measurement, comparable against other rows of
					// this kind and meaningless against any other kind.
					Resolution: ResolutionExact,
					Score:      &sim,
					Source:     "semantic-vectors",
				})
			}
		}
	}

	buf.mu.RUnlock()

	if len(newEdges) > 0 {
		buf.mu.Lock()
		buf.Edges = append(buf.Edges, newEdges...)
		buf.mu.Unlock()
	}

	slog.Info("semantic edges computed", "functions", len(funcs), "edges", len(newEdges))
	return nil
}

// --- Signal 1: Random Indexing ---

// riVocab caches the sparse random index vector for each unique token.
type riVocab struct {
	cache map[string][]riElement
}

type riElement struct {
	dim int
	val float64 // +1 or -1
}

func newRIVocab() *riVocab {
	return &riVocab{cache: make(map[string][]riElement)}
}

// indexVector returns the sparse RI vector for a token. Deterministic: same
// token always produces the same vector via FNV-based seed.
func (v *riVocab) indexVector(token string) []riElement {
	if elems, ok := v.cache[token]; ok {
		return elems
	}
	h := fnv.New64a()
	h.Write([]byte(token))
	seed := int64(h.Sum64())
	rng := rand.New(rand.NewSource(seed))

	perm := rng.Perm(riDimensions)
	elems := make([]riElement, riNonZero)
	for i := 0; i < 4; i++ {
		elems[i] = riElement{dim: perm[i], val: 1.0}
	}
	for i := 4; i < 8; i++ {
		elems[i] = riElement{dim: perm[i], val: -1.0}
	}
	v.cache[token] = elems
	return elems
}

// buildContextVector accumulates RI vectors within a sliding co-occurrence
// window of size riWindowSize over the token sequence.
func buildContextVector(vocab *riVocab, tokens []string) []float64 {
	vec := make([]float64, riDimensions)
	n := len(tokens)
	if n == 0 {
		return vec
	}

	for i := 0; i < n; i++ {
		windowStart := i - riWindowSize/2
		windowEnd := i + riWindowSize/2
		if windowStart < 0 {
			windowStart = 0
		}
		if windowEnd >= n {
			windowEnd = n - 1
		}
		for j := windowStart; j <= windowEnd; j++ {
			if j == i {
				continue
			}
			for _, elem := range vocab.indexVector(tokens[j]) {
				vec[elem.dim] += elem.val
			}
		}
	}
	return vec
}

// cosineVec computes cosine similarity between two dense float64 vectors.
func cosineVec(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// --- Signal 2: API Signature (Jaccard) ---

// jaccardSimilarity between two sets.
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// --- Signal 3: Type Signature ---

// typeSignatureHash creates a normalized hash of function type signature
// (parameter types + return types) for Type Signature matching.
func typeSignatureHash(sig string) string {
	if sig == "" {
		return ""
	}
	normalized := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' {
			return -1
		}
		return r
	}, sig)
	if len(normalized) < 3 {
		return ""
	}
	return normalized
}

// --- Combined ---

func weightedSimilarity(
	vecA, vecB []float64,
	calleesA, calleesB map[string]bool,
	typeSigA, typeSigB string,
) float64 {
	s1 := cosineVec(vecA, vecB)
	s2 := jaccardSimilarity(calleesA, calleesB)
	var s3 float64
	if typeSigA != "" && typeSigB != "" && typeSigA == typeSigB {
		s3 = 1.0
	}
	return weightRI*s1 + weightAPI*s2 + weightType*s3
}

// --- Tokenizer ---

// extractTokens splits a string into a token sequence (preserving order for
// co-occurrence window). Splits on non-alphanumeric/underscore, lowercases,
// and applies camelCase splitting.
func extractTokens(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	})
	var tokens []string
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		parts := splitCamel(w)
		for _, p := range parts {
			if len(p) >= 2 {
				tokens = append(tokens, p)
			}
		}
	}
	return tokens
}

// splitCamel splits camelCase/snake_case into subwords.
func splitCamel(s string) []string {
	var parts []string
	var cur strings.Builder
	for i, r := range s {
		if r == '_' {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			continue
		}
		if i > 0 && r >= 'A' && r <= 'Z' {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			cur.WriteRune(r + 32) // toLower
		} else {
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// ComputeSemanticEdgesWithEmbedding uses dense vectors from a Cell Provider embedding
// endpoint to compute high-quality SEMANTICALLY_RELATED edges. This is the premium path
// that produces better results than the local RI approach (which remains as fallback).
func ComputeSemanticEdgesWithEmbedding(ctx context.Context, buf *GraphBuffer, embed EmbeddingFunc, threshold float64) error {
	if threshold <= 0 {
		threshold = 0.7
	}

	buf.mu.RLock()
	type funcEntry struct {
		qname string
		text  string
	}
	var funcs []funcEntry
	for qname, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		text := n.Name + " " + n.Signature
		if n.Doc != "" {
			text = n.Doc + " " + text
		}
		funcs = append(funcs, funcEntry{qname: qname, text: text})
	}
	buf.mu.RUnlock()

	if len(funcs) == 0 {
		return nil
	}
	if len(funcs) > maxSemanticFunctions {
		funcs = funcs[:maxSemanticFunctions]
	}

	texts := make([]string, len(funcs))
	for i, f := range funcs {
		texts[i] = f.text
	}

	vectors, err := embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embedding API call: %w", err)
	}
	if len(vectors) != len(funcs) {
		return fmt.Errorf("embedding returned %d vectors for %d inputs", len(vectors), len(funcs))
	}

	var newEdges []Edge
	for i := 0; i < len(funcs); i++ {
		for j := i + 1; j < len(funcs); j++ {
			sim := cosineSimilarity(vectors[i], vectors[j])
			if sim >= threshold {
				newEdges = append(newEdges, Edge{
					SourceQName: funcs[i].qname,
					TargetQName: funcs[j].qname,
					Kind:        EdgeSemanticRelated,
					Resolution:  ResolutionExact, // endpoints are known nodes; see above
					Score:       &sim,
					Source:      "embedding",
				})
			}
		}
	}

	buf.mu.Lock()
	buf.Edges = append(buf.Edges, newEdges...)
	buf.mu.Unlock()

	slog.Info("pass5: dense embedding semantic edges", "edges", len(newEdges), "functions", len(funcs))
	return nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
