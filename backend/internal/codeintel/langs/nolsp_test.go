package langs

import (
	"encoding/json"
	"testing"
)

// INV-LSP-09: langs/*.json is CKG / runner config, not a language-server spawn catalog.
func TestLangJSONHasNoLSPSpawnCatalog(t *testing.T) {
	entries, err := langFiles.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded lang JSON files")
	}
	for _, e := range entries {
		b, err := langFiles.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		if _, ok := raw["lsp"]; ok {
			t.Errorf("%s still has an lsp spawn catalog", e.Name())
		}
	}
}
