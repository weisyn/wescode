package codeintel

import (
	"fmt"
	"log/slog"
	"sort"
)

// Convention represents an automatically discovered project convention.
type Convention struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CommunityID int      `json:"community_id"`
	Dimension   string   `json:"dimension"` // "error" | "param" | "return" | "entry" | "exit"
	Pattern     string   `json:"pattern"`   // the dominant pattern value
	Coverage    float64  `json:"coverage"`  // fraction of community functions matching
	Exemplars   []string `json:"exemplars"` // representative function names
	Outliers    []string `json:"outliers"`  // functions NOT matching (potential violations)
}

const (
	conventionMinCommunitySize = 5
	conventionMinCoverage      = 0.80 // INV-P6-01
)

// MineConventions analyzes Leiden communities to discover shared patterns.
// INV-P6-01: Only registers conventions with coverage ≥ 80%.
// INV-P6-07: Outliers are suggestions, not violations.
func MineConventions(buf *GraphBuffer) []Convention {
	buf.mu.RLock()
	defer buf.mu.RUnlock()

	// Group function/method nodes by community.
	communities := make(map[int][]*Node)
	for _, n := range buf.Nodes {
		if n.CommunityID == 0 {
			continue
		}
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		communities[n.CommunityID] = append(communities[n.CommunityID], n)
	}

	var conventions []Convention

	for cid, nodes := range communities {
		if len(nodes) < conventionMinCommunitySize {
			continue
		}

		// Extract fingerprints for each node.
		type nodeFingerprint struct {
			node *Node
			fp   PatternFingerprint
		}
		fps := make([]nodeFingerprint, len(nodes))
		for i, n := range nodes {
			fps[i] = nodeFingerprint{node: n, fp: ExtractFingerprint(n.Signature)}
		}

		// Check each dimension for dominant patterns.
		dimensions := []struct {
			name    string
			extract func(PatternFingerprint) string
		}{
			{"error", func(fp PatternFingerprint) string { return fp.ErrorPattern }},
			{"param", func(fp PatternFingerprint) string { return fp.ParamPattern }},
			{"return", func(fp PatternFingerprint) string { return fp.ReturnPattern }},
			{"entry", func(fp PatternFingerprint) string { return fp.EntryPattern }},
			{"exit", func(fp PatternFingerprint) string { return fp.ExitPattern }},
		}

		for _, dim := range dimensions {
			clusters := make(map[string][]string) // pattern → []funcName
			for _, nf := range fps {
				val := dim.extract(nf.fp)
				if val == "" || val == "unknown" {
					continue
				}
				clusters[val] = append(clusters[val], nf.node.Name)
			}

			// Find the dominant cluster.
			var bestPattern string
			var bestCount int
			for pat, names := range clusters {
				if len(names) > bestCount {
					bestPattern = pat
					bestCount = len(names)
				}
			}

			totalRelevant := 0
			for _, names := range clusters {
				totalRelevant += len(names)
			}
			if totalRelevant == 0 {
				continue
			}

			coverage := float64(bestCount) / float64(totalRelevant)
			if coverage < conventionMinCoverage {
				continue
			}

			// Collect exemplars (≤3) and outliers.
			exemplars := clusters[bestPattern]
			if len(exemplars) > 3 {
				exemplars = exemplars[:3]
			}

			var outliers []string
			for pat, names := range clusters {
				if pat == bestPattern {
					continue
				}
				outliers = append(outliers, names...)
			}
			sort.Strings(outliers)
			if len(outliers) > 5 {
				outliers = outliers[:5]
			}

			conv := Convention{
				Name:        fmt.Sprintf("community_%d_%s", cid, dim.name),
				Description: fmt.Sprintf("Community %d: %s pattern is '%s' (%.0f%% coverage)", cid, dim.name, bestPattern, coverage*100),
				CommunityID: cid,
				Dimension:   dim.name,
				Pattern:     bestPattern,
				Coverage:    coverage,
				Exemplars:   exemplars,
				Outliers:    outliers,
			}
			conventions = append(conventions, conv)
		}
	}

	if len(conventions) > 0 {
		slog.Info("pass6: conventions mined", "count", len(conventions))
	}
	return conventions
}
