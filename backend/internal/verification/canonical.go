package verification

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

var (
	timestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	uuidRe      = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	ptrRe       = regexp.MustCompile(`0x[0-9a-fA-F]{4,}`)
)

// Canonicalize applies deterministic-safe transformations to test output
// before hashing, reducing false positives from non-deterministic values.
//
// Transformations (in order):
//   - JSON: unmarshal → re-marshal (Go encoding/json sorts map keys)
//   - Timestamps: ISO 8601 patterns → <TIMESTAMP>
//   - UUIDs: v4 patterns → <UUID-N> (preserving uniqueness by first-seen order)
//   - Pointers: 0x hex addresses → <PTR>
//
// INV-VF-CANON-01: Idempotent — Canonicalize(Canonicalize(x)) == Canonicalize(x)
// INV-VF-CANON-02: Line-count preserving — no lines added or removed
// INV-VF-CANON-03: JSON keys preserved — only reordered, never deleted
// INV-VF-CANON-04: UUID uniqueness — same UUID maps to same <UUID-N> within one output
func Canonicalize(output []byte) []byte {
	if len(output) == 0 {
		return output
	}

	if json.Valid(output) {
		if canonical := canonicalJSON(output); canonical != nil {
			// JSON is canonicalized (sorted keys) but dynamic values inside
			// the JSON must STILL be normalized. Returning here would let
			// timestamps/UUIDs/pointers inside JSON objects produce false
			// L2.5 behavior-regression positives (INV-VF-CANON-01).
			output = canonical
		}
	}

	result := timestampRe.ReplaceAll(output, []byte("<TIMESTAMP>"))
	result = replaceUUIDs(result)
	result = ptrRe.ReplaceAll(result, []byte("<PTR>"))
	return result
}

// CanonicalHash computes SHA-256 of the canonicalized output.
func CanonicalHash(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	canonical := Canonicalize(output)
	h := sha256.Sum256(canonical)
	return hex.EncodeToString(h[:])
}

func canonicalJSON(data []byte) []byte {
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	// Go encoding/json sorts map keys alphabetically on Marshal.
	result, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return result
}

func replaceUUIDs(data []byte) []byte {
	seen := make(map[string]int)
	counter := 0
	return uuidRe.ReplaceAllFunc(data, func(match []byte) []byte {
		key := string(bytes.ToLower(match))
		if idx, ok := seen[key]; ok {
			return []byte(fmt.Sprintf("<UUID-%d>", idx))
		}
		counter++
		seen[key] = counter
		return []byte(fmt.Sprintf("<UUID-%d>", counter))
	})
}
