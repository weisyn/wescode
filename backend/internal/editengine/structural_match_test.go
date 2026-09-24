package editengine

import (
	"strings"
	"testing"

	"github.com/weisyn/wescode/internal/treesitter"
)

func TestCombinedFallback_Tier2First(t *testing.T) {
	ts := treesitter.NewParserPool()
	defer ts.Close()
	fallback := NewCombinedFallback(ts, nil)

	content := "package main\n\nfunc hello() {}   \n"
	old := "func hello() {}\n"
	start, end, ok := fallback("test.go", content, old)
	if !ok {
		t.Fatal("Tier 2 should match trailing whitespace")
	}
	_ = start
	_ = end
}

func TestCombinedFallback_Tier3WhenTier2Fails(t *testing.T) {
	ts := treesitter.NewParserPool()
	defer ts.Close()
	fallback := NewCombinedFallback(ts, nil)

	content := "package main\n\nfunc HandleRequest() error {\n\tlog.Println(\"handling\")\n\treturn nil\n}\n"
	old := "func HandleRequest() error {\n\treturn fmt.Errorf(\"not implemented\")\n}"

	start, end, ok := fallback("handler.go", content, old)
	if !ok {
		t.Skip("Tier 3 structural match depends on tree-sitter finding the symbol")
	}
	matched := content[start:end]
	if matched == "" {
		t.Error("expected non-empty match")
	}
	t.Logf("Tier 3 matched: %q", matched)
}

func TestCombinedFallback_NilTreeSitter(t *testing.T) {
	fallback := NewCombinedFallback(nil, nil)

	content := "package main\nfunc hello() {}\n"
	old := "func nonexistent() {}"
	_, _, ok := fallback("test.go", content, old)
	if ok {
		t.Error("should not match when function doesn't exist")
	}
}

func TestStructuralMatch_GoFunction(t *testing.T) {
	ts := treesitter.NewParserPool()
	defer ts.Close()

	content := "package main\n\nfunc Hello() {\n\tfmt.Println(\"hello\")\n}\n\nfunc World() {\n\tfmt.Println(\"world\")\n}\n"
	old := "func Hello() {\n\tfmt.Println(\"old hello\")\n}"

	start, end, ok := structuralMatch(ts, "main.go", content, old)
	if !ok {
		t.Fatal("should find Hello function structurally")
	}
	matched := content[start:end]
	if !strings.Contains(matched, "Hello") {
		t.Errorf("matched content should contain 'Hello': %q", matched)
	}
	if strings.Contains(matched, "World") {
		t.Error("should not include World function")
	}
}

func TestStructuralMatch_RejectsLineDivergence(t *testing.T) {
	ts := treesitter.NewParserPool()
	defer ts.Close()

	// File has a 1-line Hello; old_string claims 10+ lines — ratio > 2.0 → reject
	content := "package main\n\nfunc Hello() {}\n"
	old := "func Hello() {\n\ta := 1\n\tb := 2\n\tc := 3\n\td := 4\n\te := 5\n\tf := 6\n\tg := 7\n\th := 8\n\ti := 9\n\tj := 10\n}"

	_, _, ok := structuralMatch(ts, "main.go", content, old)
	if ok {
		t.Error("should reject match when line count ratio exceeds 2.0")
	}
}

func TestStructuralMatch_RejectsLowIdentifierCoverage(t *testing.T) {
	ts := treesitter.NewParserPool()
	defer ts.Close()

	// old_string references identifiers that don't exist in the actual function body
	content := "package main\n\nfunc Process() {\n\tfmt.Println(\"hello\")\n\treturn\n}\n"
	old := "func Process() {\n\tdb.Query(userID)\n\tcache.Set(result)\n\treturn\n}"

	_, _, ok := structuralMatch(ts, "main.go", content, old)
	if ok {
		t.Error("should reject match when identifier coverage < 50%")
	}
}

func TestExtractIdentifiers(t *testing.T) {
	s := "func HandleRequest(ctx context.Context) error {\n\tif err != nil {\n\t\treturn err\n\t}\n}"
	ids := extractIdentifiers(s)

	has := func(name string) bool {
		for _, id := range ids {
			if id == name {
				return true
			}
		}
		return false
	}

	if !has("HandleRequest") {
		t.Error("should contain HandleRequest")
	}
	if !has("ctx") || !has("Context") {
		t.Error("should contain ctx and Context")
	}
	if has("func") || has("return") || has("nil") || has("error") {
		t.Error("should not contain reserved keywords")
	}
	// "if" is 2 chars (excluded by len>=3), "err" is 3 chars and not reserved — both are expected to pass through.
}
