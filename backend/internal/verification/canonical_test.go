package verification

import (
	"bytes"
	"testing"
)

func TestCanonicalize_Idempotent(t *testing.T) {
	// INV-VF-CANON-01
	inputs := [][]byte{
		[]byte(`{"z":1,"a":2}`),
		[]byte("log 2026-07-26T14:32:01Z started\nid=550e8400-e29b-41d4-a716-446655440000 ptr=0x1400012a080"),
		[]byte("plain text no transforms"),
		[]byte(""),
	}
	for _, input := range inputs {
		once := Canonicalize(input)
		twice := Canonicalize(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("not idempotent:\n  input:  %q\n  once:   %q\n  twice:  %q", input, once, twice)
		}
	}
}

func TestCanonicalize_LineCountPreserved(t *testing.T) {
	// INV-VF-CANON-02
	input := []byte("line1 2026-07-26T14:32:01Z\nline2 550e8400-e29b-41d4-a716-446655440000\nline3 0x1400012a080\n")
	inputLines := bytes.Count(input, []byte("\n"))
	result := Canonicalize(input)
	resultLines := bytes.Count(result, []byte("\n"))
	if inputLines != resultLines {
		t.Errorf("line count changed: %d → %d\n  input:  %q\n  result: %q", inputLines, resultLines, input, result)
	}
}

func TestCanonicalize_JSONKeysPreserved(t *testing.T) {
	// INV-VF-CANON-03
	input := []byte(`{"z_key":"val1","a_key":"val2","m_key":{"nested":true}}`)
	result := Canonicalize(input)
	for _, key := range []string{`"z_key"`, `"a_key"`, `"m_key"`, `"nested"`} {
		if !bytes.Contains(result, []byte(key)) {
			t.Errorf("key %s missing from canonical output: %s", key, result)
		}
	}
	if !bytes.HasPrefix(result, []byte(`{"a_key"`)) {
		t.Errorf("JSON keys not sorted: %s", result)
	}
}

func TestCanonicalize_UUIDUniqueness(t *testing.T) {
	// INV-VF-CANON-04
	input := []byte("id1=550e8400-e29b-41d4-a716-446655440000 id2=6ba7b810-9dad-11d1-80b4-00c04fd430c8 id1_again=550e8400-e29b-41d4-a716-446655440000")
	result := Canonicalize(input)
	if !bytes.Contains(result, []byte("<UUID-1>")) || !bytes.Contains(result, []byte("<UUID-2>")) {
		t.Fatalf("UUIDs not replaced: %s", result)
	}
	count1 := bytes.Count(result, []byte("<UUID-1>"))
	if count1 != 2 {
		t.Errorf("same UUID should map to same placeholder, got %d occurrences of <UUID-1>: %s", count1, result)
	}
	count2 := bytes.Count(result, []byte("<UUID-2>"))
	if count2 != 1 {
		t.Errorf("different UUID should map to different placeholder, got %d occurrences of <UUID-2>: %s", count2, result)
	}
}

func TestCanonicalize_TimestampReplacement(t *testing.T) {
	input := []byte("started at 2026-07-26T14:32:01.123Z finished at 2026-07-26T14:32:05+08:00")
	result := Canonicalize(input)
	if !bytes.Contains(result, []byte("<TIMESTAMP>")) {
		t.Errorf("timestamps not replaced: %s", result)
	}
	if bytes.Contains(result, []byte("2026")) {
		t.Errorf("timestamp year leaked through: %s", result)
	}
}

func TestCanonicalize_PointerReplacement(t *testing.T) {
	input := []byte("ptr=0x1400012a080 another=0xc0000b6000")
	result := Canonicalize(input)
	if !bytes.Contains(result, []byte("<PTR>")) {
		t.Errorf("pointers not replaced: %s", result)
	}
	if bytes.Contains(result, []byte("0x14")) {
		t.Errorf("pointer address leaked through: %s", result)
	}
}

func TestCanonicalize_PlainText(t *testing.T) {
	input := []byte("no special patterns here, just plain text output from a test")
	result := Canonicalize(input)
	if !bytes.Equal(input, result) {
		t.Errorf("plain text should not be transformed:\n  input:  %q\n  result: %q", input, result)
	}
}

func TestCanonicalize_Empty(t *testing.T) {
	result := Canonicalize(nil)
	if result != nil {
		t.Errorf("nil input should return nil, got %q", result)
	}
	result = Canonicalize([]byte{})
	if len(result) != 0 {
		t.Errorf("empty input should return empty, got %q", result)
	}
}

func TestCanonicalize_JSONWithDynamicValues(t *testing.T) {
	// Regression (L2.5 false-positive fix): the JSON branch must NOT skip
	// dynamic-value normalization. Before the fix, JSON output containing
	// timestamps/UUIDs/pointers produced different hashes across identical
	// runs, falsely flagging behavioral regressions for any JSON-producing test.
	input := []byte(`{"ts":"2026-08-04T09:00:00Z","id":"550e8400-e29b-41d4-a716-446655440000","ptr":"0x1400012a080"}`)
	a := Canonicalize(input)
	b := Canonicalize(input)
	if !bytes.Equal(a, b) {
		t.Fatalf("identical JSON with dynamic values produced different output:\n  a: %s\n  b: %s", a, b)
	}
	for _, want := range []string{"<TIMESTAMP>", "<UUID-1>", "<PTR>"} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("dynamic value not normalized inside JSON: missing %s in %s", want, a)
		}
	}
	if bytes.Contains(a, []byte("2026")) || bytes.Contains(a, []byte("550e8400")) || bytes.Contains(a, []byte("0x1400")) {
		t.Errorf("raw dynamic values leaked through JSON canonicalization: %s", a)
	}
	// Key order must not affect the canonical form (INV-VF-CANON-01).
	shuffled := Canonicalize([]byte(`{"id":"550e8400-e29b-41d4-a716-446655440000","ptr":"0x1400012a080","ts":"2026-08-04T09:00:00Z"}`))
	if !bytes.Equal(a, shuffled) {
		t.Errorf("key order changed canonical output:\n  a: %s\n  b: %s", a, shuffled)
	}
	// L2.5 hash stability across identical runs.
	if CanonicalHash(input) != CanonicalHash(input) {
		t.Error("CanonicalHash must be deterministic for identical JSON with dynamic values")
	}
}

func TestCanonicalHash(t *testing.T) {
	a := CanonicalHash([]byte(`{"b":1,"a":2}`))
	b := CanonicalHash([]byte(`{"a":2,"b":1}`))
	if a != b {
		t.Errorf("semantically equal JSON should produce same hash:\n  a: %s\n  b: %s", a, b)
	}
	if a == "" {
		t.Error("hash should not be empty for non-empty input")
	}

	empty := CanonicalHash(nil)
	if empty != "" {
		t.Errorf("nil input should return empty hash, got %s", empty)
	}
}
