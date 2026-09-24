package codeintel

import "math/rand/v2"

// ComputeLeidenCommunities runs the Leiden community detection algorithm
// on the CKG graph. Only uses CALLS + IMPORTS edges for clustering.
// Returns community_id for each node QualifiedName.
// INV-P4-02: does not modify original graph edges, only produces community assignments.
func ComputeLeidenCommunities(buf *GraphBuffer) map[string]int {
	buf.mu.RLock()
	adj := buildAdjacency(buf)
	buf.mu.RUnlock()

	if len(adj) == 0 {
		return nil
	}

	communities := make(map[string]int, len(adj))
	id := 0
	for node := range adj {
		communities[node] = id
		id++
	}

	totalEdges := countTotalEdges(adj)
	if totalEdges == 0 {
		return communities
	}

	// Iterate until convergence (max 50 passes to avoid infinite loop).
	for pass := 0; pass < 50; pass++ {
		moved := leidenPhase1(adj, communities, totalEdges)
		leidenRefine(adj, communities)

		if !moved {
			break
		}
	}

	// Normalize community IDs to be contiguous starting from 1.
	return normalizeCommunityIDs(communities)
}

// buildAdjacency constructs an undirected adjacency list from CALLS + IMPORTS edges.
//
// Every retained edge weighs 1.0. It used to weigh its `certainty`, which read as
// "weaker call, weaker coupling" but was not that number: for a call edge the
// float said how sure the *name lookup* was, and its most common value (1.0) was
// also what "no candidate found at all" left behind. Both endpoints being in
// `adj` is already the real precondition — an edge that got here is one whose
// source and target are both known symbols in this buffer, and two such symbols
// either call each other or they don't. Modularity has no third state to weigh.
func buildAdjacency(buf *GraphBuffer) map[string]map[string]float64 {
	adj := make(map[string]map[string]float64, len(buf.Nodes))

	for qname := range buf.Nodes {
		adj[qname] = make(map[string]float64)
	}

	for _, e := range buf.Edges {
		if e.Kind != EdgeCall && e.Kind != EdgeImport {
			continue
		}
		if _, ok := adj[e.SourceQName]; !ok {
			continue
		}
		if _, ok := adj[e.TargetQName]; !ok {
			continue
		}
		if e.SourceQName == e.TargetQName {
			continue
		}
		adj[e.SourceQName][e.TargetQName]++
		adj[e.TargetQName][e.SourceQName]++
	}

	return adj
}

// countTotalEdges returns the sum of all edge weights (each undirected edge counted once).
func countTotalEdges(adj map[string]map[string]float64) float64 {
	total := 0.0
	for _, neighbors := range adj {
		for _, w := range neighbors {
			total += w
		}
	}
	return total / 2.0
}

// leidenPhase1 performs local moves: greedily moves nodes to maximize modularity.
// Returns true if any node was moved.
func leidenPhase1(adj map[string]map[string]float64, communities map[string]int, totalEdges float64) bool {
	moved := false
	m2 := 2.0 * totalEdges

	nodes := make([]string, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	degree := make(map[string]float64, len(adj))
	for n, neighbors := range adj {
		d := 0.0
		for _, w := range neighbors {
			d += w
		}
		degree[n] = d
	}

	// Precompute community degree sums — O(n) init, O(1) lookup per candidate.
	commDegree := make(map[int]float64, len(adj)/4)
	for n, c := range communities {
		commDegree[c] += degree[n]
	}

	for _, node := range nodes {
		currentComm := communities[node]
		ki := degree[node]

		neighborComms := make(map[int]float64)
		for neighbor, w := range adj[node] {
			c := communities[neighbor]
			neighborComms[c] += w
		}

		bestComm := currentComm
		bestDeltaQ := 0.0

		for comm, kiIn := range neighborComms {
			if comm == currentComm {
				continue
			}

			sigmaTotal := commDegree[comm]
			deltaQ := kiIn/m2 - (sigmaTotal*ki)/(m2*m2)

			kiInCurrent := neighborComms[currentComm]
			sigmaCurrent := commDegree[currentComm] - ki
			deltaQLoss := kiInCurrent/m2 - (sigmaCurrent*ki)/(m2*m2)

			netDelta := deltaQ - deltaQLoss
			if netDelta > bestDeltaQ {
				bestDeltaQ = netDelta
				bestComm = comm
			}
		}

		if bestComm != currentComm {
			commDegree[currentComm] -= ki
			commDegree[bestComm] += ki
			communities[node] = bestComm
			moved = true
		}
	}

	return moved
}

// leidenRefine ensures each community is internally connected (Leiden-specific guarantee).
// Splits disconnected components within a community into separate communities.
func leidenRefine(adj map[string]map[string]float64, communities map[string]int) {
	// Group nodes by community.
	commNodes := make(map[int][]string)
	for n, c := range communities {
		commNodes[c] = append(commNodes[c], n)
	}

	nextID := 0
	for _, c := range communities {
		if c >= nextID {
			nextID = c + 1
		}
	}

	for _, members := range commNodes {
		if len(members) <= 1 {
			continue
		}

		components := connectedComponents(members, adj)
		if len(components) <= 1 {
			continue
		}

		// Keep the largest component with the original ID; assign new IDs to others.
		largestIdx := 0
		for i, comp := range components {
			if len(comp) > len(components[largestIdx]) {
				largestIdx = i
			}
		}

		for i, comp := range components {
			if i == largestIdx {
				continue
			}
			for _, n := range comp {
				communities[n] = nextID
			}
			nextID++
		}
	}
}

// connectedComponents finds connected components among a subset of nodes
// using only edges within the subset.
func connectedComponents(nodes []string, adj map[string]map[string]float64) [][]string {
	nodeSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n] = true
	}

	visited := make(map[string]bool, len(nodes))
	var components [][]string

	for _, start := range nodes {
		if visited[start] {
			continue
		}

		var component []string
		queue := []string{start}
		visited[start] = true

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			component = append(component, curr)

			for neighbor := range adj[curr] {
				if nodeSet[neighbor] && !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		components = append(components, component)
	}

	return components
}

// normalizeCommunityIDs remaps community IDs to contiguous integers starting from 1.
func normalizeCommunityIDs(communities map[string]int) map[string]int {
	if len(communities) == 0 {
		return communities
	}

	seen := make(map[int]int) // old ID → new ID
	nextID := 1

	result := make(map[string]int, len(communities))
	for node, oldID := range communities {
		newID, ok := seen[oldID]
		if !ok {
			newID = nextID
			seen[oldID] = newID
			nextID++
		}
		result[node] = newID
	}

	return result
}
