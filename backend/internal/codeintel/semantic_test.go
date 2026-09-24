package codeintel

import (
	"math"
	"testing"
)

func TestRandomIndexingDeterminism(t *testing.T) {
	vocab := newRIVocab()

	tokensA := []string{"parse", "file", "read", "buffer", "write"}
	vec1 := buildContextVector(vocab, tokensA)
	vec2 := buildContextVector(vocab, tokensA)

	sim := cosineVec(vec1, vec2)
	if sim < 0.999 {
		t.Errorf("identical token sequences: cosine = %f, want ~1.0", sim)
	}

	tokensB := []string{"network", "socket", "connect", "send", "receive"}
	vec3 := buildContextVector(vocab, tokensB)

	simDiff := cosineVec(vec1, vec3)
	if simDiff > 0.5 {
		t.Errorf("unrelated token sequences: cosine = %f, want < 0.5", simDiff)
	}
}

func TestWeightedSimilarity(t *testing.T) {
	calleesA := map[string]bool{"Open": true, "Read": true, "Close": true}
	calleesB := map[string]bool{"Open": true, "Read": true, "Close": true}
	calleesC := map[string]bool{"Send": true, "Receive": true, "Dial": true}

	typeSig := "func(string)error"

	vocab := newRIVocab()
	tokens := []string{"open", "read", "close", "file", "buffer"}
	vecSame := buildContextVector(vocab, tokens)

	tokensDiff := []string{"network", "dial", "send", "receive", "socket"}
	vecDiff := buildContextVector(vocab, tokensDiff)

	simSame := weightedSimilarity(vecSame, vecSame, calleesA, calleesB, typeSig, typeSig)
	if simSame < 0.9 {
		t.Errorf("identical everything: similarity = %f, want >= 0.9", simSame)
	}

	simDiff := weightedSimilarity(vecSame, vecDiff, calleesA, calleesC, typeSig, "func(int)string")
	if simDiff > 0.3 {
		t.Errorf("different everything: similarity = %f, want <= 0.3", simDiff)
	}

	simOnlyCallees := weightedSimilarity(vecSame, vecSame, calleesA, calleesC, typeSig, typeSig)
	expectedDelta := weightAPI * 1.0
	actualDelta := simSame - simOnlyCallees
	if math.Abs(actualDelta-expectedDelta) > 0.05 {
		t.Errorf("callee-only change: delta = %f, expected ~%f (weightAPI)", actualDelta, expectedDelta)
	}
}

func TestSemanticEdgesThreshold(t *testing.T) {
	buf := NewGraphBuffer()

	buf.AddNode(&Node{
		QualifiedName: "pkg:FuncA:1",
		FilePath:      "/a.go",
		Kind:          NodeFunction,
		Name:          "FuncA",
		Signature:     "func(ctx context.Context, name string) error",
	})
	buf.AddNode(&Node{
		QualifiedName: "pkg:FuncB:5",
		FilePath:      "/b.go",
		Kind:          NodeFunction,
		Name:          "FuncB",
		Signature:     "func(ctx context.Context, name string) error",
	})
	buf.AddNode(&Node{
		QualifiedName: "pkg:FuncC:10",
		FilePath:      "/c.go",
		Kind:          NodeFunction,
		Name:          "FuncC",
		Signature:     "func(x int) int",
	})

	buf.AddEdge(Edge{SourceQName: "pkg:FuncA:1", TargetName: "Open", Kind: EdgeCall, Resolution: ResolutionUnresolved})
	buf.AddEdge(Edge{SourceQName: "pkg:FuncA:1", TargetName: "Read", Kind: EdgeCall, Resolution: ResolutionUnresolved})
	buf.AddEdge(Edge{SourceQName: "pkg:FuncA:1", TargetName: "Close", Kind: EdgeCall, Resolution: ResolutionUnresolved})

	buf.AddEdge(Edge{SourceQName: "pkg:FuncB:5", TargetName: "Open", Kind: EdgeCall, Resolution: ResolutionUnresolved})
	buf.AddEdge(Edge{SourceQName: "pkg:FuncB:5", TargetName: "Read", Kind: EdgeCall, Resolution: ResolutionUnresolved})
	buf.AddEdge(Edge{SourceQName: "pkg:FuncB:5", TargetName: "Close", Kind: EdgeCall, Resolution: ResolutionUnresolved})

	buf.AddEdge(Edge{SourceQName: "pkg:FuncC:10", TargetName: "Sqrt", Kind: EdgeCall, Resolution: ResolutionUnresolved})
	buf.AddEdge(Edge{SourceQName: "pkg:FuncC:10", TargetName: "Pow", Kind: EdgeCall, Resolution: ResolutionUnresolved})

	if err := ComputeSemanticEdges(buf, 0.5); err != nil {
		t.Fatalf("ComputeSemanticEdges: %v", err)
	}

	var semanticEdges []Edge
	for _, e := range buf.Edges {
		if e.Kind == EdgeSemanticRelated {
			semanticEdges = append(semanticEdges, e)
		}
	}

	foundAB := false
	for _, e := range semanticEdges {
		if (e.SourceQName == "pkg:FuncA:1" && e.TargetQName == "pkg:FuncB:5") ||
			(e.SourceQName == "pkg:FuncB:5" && e.TargetQName == "pkg:FuncA:1") {
			foundAB = true
		}
	}
	if !foundAB {
		t.Error("expected semantic edge between FuncA and FuncB (similar callees + same type signature)")
	}

	foundAC := false
	for _, e := range semanticEdges {
		if (e.SourceQName == "pkg:FuncA:1" && e.TargetQName == "pkg:FuncC:10") ||
			(e.SourceQName == "pkg:FuncC:10" && e.TargetQName == "pkg:FuncA:1") {
			foundAC = true
		}
	}
	if foundAC {
		t.Error("unexpected semantic edge between FuncA and FuncC (different callees + different type)")
	}
}
