package lspbridge

import "testing"

func TestLangForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"a.ts", "typescript"},
		{"a.tsx", "typescript"},
		{"lib.rs", "rust"},
		{"unknown.txt", ""},
	}
	for _, tc := range cases {
		if got := LangForPath(tc.path); got != tc.want {
			t.Errorf("LangForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
