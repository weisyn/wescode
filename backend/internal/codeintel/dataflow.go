package codeintel

import (
	"log/slog"
	"strings"
)

// ResolveDataFlow generates DATA_FLOWS_TO edges by mapping function call
// arguments to callee parameters. This enables value-level impact analysis.
//
// For each resolved CALLS edge (caller → callee):
//  1. Locate the call expression in the caller's AST
//  2. Extract argument list
//  3. Map each argument to the corresponding callee parameter
//  4. Create DATA_FLOWS_TO edge: caller_arg → callee_param
//
// Every edge is `inferred`: the endpoints are the two ends of an already-resolved
// call, but the argument→parameter pairing is positional, read off a signature
// string. Extraction quality does differ by language (Go's signatures parse
// cleanly, Python's are largely untyped), and that used to be written into each
// row as a per-language float. It is a property of this extractor, not of the
// edge — one number per language cannot be compared against another edge's
// number, and every reader that tried was ranking a constant. It is documented
// in the data_flow tool contract instead.
func ResolveDataFlow(buf *GraphBuffer) int {
	buf.mu.RLock()
	callEdges := make([]Edge, 0, len(buf.Edges)/4)
	for _, e := range buf.Edges {
		if e.Kind == EdgeCall && e.TargetQName != "" {
			callEdges = append(callEdges, e)
		}
	}
	buf.mu.RUnlock()

	count := 0
	for _, call := range callEdges {
		buf.mu.RLock()
		callerNode := buf.Nodes[call.SourceQName]
		calleeNode := buf.Nodes[call.TargetQName]
		buf.mu.RUnlock()

		if callerNode == nil || calleeNode == nil {
			continue
		}
		if calleeNode.Kind != NodeFunction && calleeNode.Kind != NodeMethod {
			continue
		}

		calleeParams := parseSignatureParams(calleeNode.Signature)
		if len(calleeParams) == 0 {
			continue
		}

		for i, param := range calleeParams {
			if param.name == "" {
				continue
			}

			buf.AddEdge(Edge{
				SourceQName: call.SourceQName,
				TargetQName: call.TargetQName,
				TargetName:  param.name,
				Kind:        EdgeDataFlowsTo,
				Resolution:  ResolutionInferred,
				Source:      "dataflow-resolve",
				FlowType:    "param_pass",
				ParamIdx:    i,
			})
			count++
		}
	}

	if count > 0 {
		slog.Info("ResolveDataFlow: edges created", "count", count)
	}
	return count
}

type sigParam struct {
	name string
	typ  string
}

// parseSignatureParams extracts parameter names and types from a signature string.
// Supports Go-style "(name type, name type)" and TS/Python-style "(name: type, name: type)".
func parseSignatureParams(sig string) []sigParam {
	if sig == "" {
		return nil
	}

	// Strip outer parens: find the first (...) group.
	start := strings.IndexByte(sig, '(')
	if start < 0 {
		return nil
	}
	end := matchingParen(sig, start)
	if end < 0 {
		return nil
	}

	inner := sig[start+1 : end]
	if strings.TrimSpace(inner) == "" {
		return nil
	}

	parts := splitParams(inner)
	params := make([]sigParam, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p := parseSingleParam(part)
		if p.name != "" {
			params = append(params, p)
		}
	}
	return params
}

// parseSingleParam handles "name type", "name: type", "name string", etc.
func parseSingleParam(s string) sigParam {
	// TS/Python style: "name: type" or "name?: type"
	if idx := strings.IndexByte(s, ':'); idx >= 0 {
		name := strings.TrimSpace(s[:idx])
		name = strings.TrimSuffix(name, "?")
		typ := strings.TrimSpace(s[idx+1:])
		return sigParam{name: name, typ: typ}
	}

	// Go style: "name type" or just "type" (unnamed)
	fields := strings.Fields(s)
	switch len(fields) {
	case 0:
		return sigParam{}
	case 1:
		// Could be unnamed param (just type) in Go: func(int, string)
		// Or named param without type in some contexts.
		// Heuristic: if starts with lowercase and not a known type keyword, treat as name.
		if len(fields[0]) > 0 && fields[0][0] >= 'a' && fields[0][0] <= 'z' && !isCommonGoType(fields[0]) {
			return sigParam{name: fields[0]}
		}
		return sigParam{typ: fields[0]}
	default:
		// "name type" or "name ...type"
		name := fields[0]
		// Skip variadic prefix for the type
		typ := strings.Join(fields[1:], " ")
		return sigParam{name: name, typ: typ}
	}
}

func isCommonGoType(s string) bool {
	switch s {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "string", "bool", "byte", "rune",
		"error", "any", "interface", "struct":
		return true
	}
	return false
}

// matchingParen finds the closing paren matching the open paren at pos.
func matchingParen(s string, pos int) int {
	depth := 0
	for i := pos; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitParams splits a parameter list string by commas, respecting nested parens/brackets.
func splitParams(s string) []string {
	var result []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}
