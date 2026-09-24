package codeintel

import "strings"

// PatternFingerprint captures the structural pattern of a function for convention mining.
type PatternFingerprint struct {
	EntryPattern  string `json:"entry"`  // first 3 statement AST node types
	ExitPattern   string `json:"exit"`   // last 3 statement AST node types
	ErrorPattern  string `json:"error"`  // error handling pattern (none/early_return/wrap/panic)
	ParamPattern  string `json:"param"`  // parameter type signature pattern
	ReturnPattern string `json:"return"` // return type signature pattern
}

// ExtractFingerprint generates a PatternFingerprint from a function's signature.
// Signature format: "func Name(params) returns" or "(receiver) Name(params) returns".
func ExtractFingerprint(sig string) PatternFingerprint {
	fp := PatternFingerprint{
		EntryPattern:  "unknown",
		ExitPattern:   "unknown",
		ErrorPattern:  classifyErrorPattern(sig),
		ParamPattern:  extractParamPattern(sig),
		ReturnPattern: extractReturnPattern(sig),
	}
	return fp
}

func classifyErrorPattern(sig string) string {
	ret := extractReturnPattern(sig)
	switch {
	case strings.Contains(ret, "error"):
		return "early_return"
	case strings.Contains(sig, "panic"):
		return "panic"
	default:
		return "none"
	}
}

func extractParamPattern(sig string) string {
	openParen := strings.Index(sig, "(")
	if openParen < 0 {
		return ""
	}
	// Skip receiver if present: "(recv) Name(params)"
	start := openParen
	if strings.HasPrefix(sig, "(") {
		closeParen := strings.Index(sig, ")")
		if closeParen > 0 {
			rest := sig[closeParen+1:]
			next := strings.Index(rest, "(")
			if next >= 0 {
				start = closeParen + 1 + next
			}
		}
	}

	sub := sig[start:]
	close := strings.Index(sub, ")")
	if close < 0 {
		return ""
	}
	params := sub[1:close]
	if params == "" {
		return "()"
	}
	return normalizeTypeList(params)
}

func extractReturnPattern(sig string) string {
	// Find the last ")" that closes params, then everything after is return.
	lastParen := strings.LastIndex(sig, ")")
	if lastParen < 0 || lastParen >= len(sig)-1 {
		return ""
	}
	ret := strings.TrimSpace(sig[lastParen+1:])
	ret = strings.TrimPrefix(ret, "(")
	ret = strings.TrimSuffix(ret, ")")
	if ret == "" {
		return ""
	}
	return normalizeTypeList(ret)
}

// normalizeTypeList converts "ctx context.Context, name string" → "Context,string"
func normalizeTypeList(raw string) string {
	parts := strings.Split(raw, ",")
	var types []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		typ := fields[len(fields)-1]
		// Strip package prefix: "context.Context" → "Context"
		if idx := strings.LastIndex(typ, "."); idx >= 0 {
			typ = typ[idx+1:]
		}
		// Strip pointer/slice
		typ = strings.TrimLeft(typ, "*[]")
		types = append(types, typ)
	}
	return strings.Join(types, ",")
}
