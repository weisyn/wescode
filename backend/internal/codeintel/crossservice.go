package codeintel

import "strings"

// InferCrossServiceEdges matches HTTP client calls to route handlers
// across different packages/services within the same workspace.
//
// Every edge is `inferred`: both endpoints are real buffer nodes, but the
// relation is a conclusion about two URL strings that never meet in the source.
// The match tier (exact / param / prefix) is an argmax key for picking one route
// among several candidates — it lives only inside this function. It used to be
// written to each row as the edge's confidence, where no reader consumed it and
// none could: 0.6 here meant "the path had a :param segment", while 0.6 on an
// implements edge meant something unrelated. The tier is recorded in metadata
// for a human reading the row, not as a number to compare across edge kinds.
func InferCrossServiceEdges(buf *GraphBuffer) int {
	buf.mu.RLock()

	// Collect route handlers: QName → (method, path, packagePath).
	type routeInfo struct {
		qname   string
		method  string
		path    string
		pkgPath string
	}
	var routes []routeInfo

	for qname, n := range buf.Nodes {
		if n.Kind != NodeRoute {
			continue
		}
		method, path := parseRouteQName(qname)
		if path == "" {
			continue
		}
		routes = append(routes, routeInfo{
			qname:   qname,
			method:  method,
			path:    path,
			pkgPath: n.PackagePath,
		})
	}

	// Collect HTTP_CALLS edges with their source node's package.
	type callInfo struct {
		sourceQName string
		targetQName string
		sourcePkg   string
		method      string
		path        string
	}
	var calls []callInfo

	for _, e := range buf.Edges {
		if e.Kind != EdgeHTTPCalls {
			continue
		}
		srcNode := buf.Nodes[e.SourceQName]
		if srcNode == nil {
			continue
		}
		method, path := parseRouteQName(e.TargetQName)
		if path == "" {
			path = e.TargetQName
		}
		calls = append(calls, callInfo{
			sourceQName: e.SourceQName,
			targetQName: e.TargetQName,
			sourcePkg:   srcNode.PackagePath,
			method:      method,
			path:        path,
		})
	}
	buf.mu.RUnlock()

	if len(routes) == 0 || len(calls) == 0 {
		return 0
	}

	var newEdges []Edge

	for _, c := range calls {
		bestTier := matchTierNone
		bestRoute := ""

		for _, r := range routes {
			if r.pkgPath == c.sourcePkg {
				continue // same package, not cross-service
			}

			if c.method != "" && r.method != "" && !strings.EqualFold(c.method, r.method) {
				continue
			}

			if tier := crossServiceMatchTier(r.path, c.path); tier > bestTier {
				bestTier = tier
				bestRoute = r.qname
			}
		}

		if bestRoute != "" {
			newEdges = append(newEdges, Edge{
				SourceQName: c.sourceQName,
				TargetQName: bestRoute,
				TargetName:  bestRoute,
				Kind:        EdgeCrossHTTPCalls,
				Resolution:  ResolutionInferred,
				Source:      "cross-service-inference",
				Metadata:    `{"match":"` + bestTier.String() + `"}`,
			})
		}
	}

	buf.mu.Lock()
	buf.Edges = append(buf.Edges, newEdges...)
	buf.mu.Unlock()

	return len(newEdges)
}

// matchTier orders how a request URL met a route pattern. Ordered so the caller
// can argmax over candidates; `none` means the two paths do not describe the
// same endpoint and no edge is emitted.
type matchTier int

const (
	matchTierNone matchTier = iota
	matchTierPrefix
	matchTierParam
	matchTierExact
)

func (t matchTier) String() string {
	switch t {
	case matchTierExact:
		return "exact"
	case matchTierParam:
		return "param"
	case matchTierPrefix:
		return "prefix"
	default:
		return "none"
	}
}

// crossServiceMatchTier reports how a route pattern met a call path:
//   - exact:  the normalized paths are equal
//   - param:  they agree once :param / {param} segments are treated as wildcards
//   - prefix: the call path extends the route's literal segments (≥2 of them)
func crossServiceMatchTier(routePath, callPath string) matchTier {
	rNorm := normalizePath(routePath)
	cNorm := normalizePath(callPath)

	if strings.EqualFold(rNorm, cNorm) {
		return matchTierExact
	}

	if pathMatches(rNorm, cNorm) {
		return matchTierParam
	}

	rParts := splitPath(rNorm)
	cParts := splitPath(cNorm)
	if len(rParts) > 0 && len(cParts) >= len(rParts) {
		prefixMatch := true
		for i, rp := range rParts {
			if isParam(rp) {
				continue
			}
			if !strings.EqualFold(rp, cParts[i]) {
				prefixMatch = false
				break
			}
		}
		// A single shared segment ("/api") is not evidence of the same endpoint.
		if prefixMatch && len(rParts) >= 2 {
			return matchTierPrefix
		}
	}

	return matchTierNone
}

// parseRouteQName extracts method and path from a route QualifiedName.
// Format: "route:METHOD /path" or "route:/path".
func parseRouteQName(qname string) (method, path string) {
	s := strings.TrimPrefix(qname, "route:")
	if s == qname {
		return "", ""
	}

	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return "", s
}
