package codeintel

import (
	"log/slog"
	"strings"
)

// Taint source patterns by language/framework.
// Each entry maps a function/method/symbol name fragment to its TaintKind.
var taintSources = map[string]TaintKind{
	// user_input: HTTP request parameters
	"http.Request":    TaintUserInput,
	"gin.Context":     TaintUserInput,
	"echo.Context":    TaintUserInput,
	"fiber.Ctx":       TaintUserInput,
	"req.Body":        TaintUserInput,
	"req.Query":       TaintUserInput,
	"req.Params":      TaintUserInput,
	"r.FormValue":     TaintUserInput,
	"r.URL.Query":     TaintUserInput,
	"request.json":    TaintUserInput,
	"request.form":    TaintUserInput,
	"request.args":    TaintUserInput,
	"req.body":        TaintUserInput,
	"req.query":       TaintUserInput,
	"req.params":      TaintUserInput,
	"ctx.Query":       TaintUserInput,
	"ctx.Param":       TaintUserInput,
	"ctx.PostForm":    TaintUserInput,
	"ctx.GetRawData":  TaintUserInput,
	"ctx.ShouldBind":  TaintUserInput,
	"ReadAll(r.Body)": TaintUserInput,
	"io.ReadAll":      TaintUserInput,
	"bufio.NewReader": TaintUserInput,
	"os.Stdin":        TaintUserInput,
	"stdin":           TaintUserInput,

	// config: environment and config reads
	"os.Getenv":       TaintConfig,
	"os.LookupEnv":    TaintConfig,
	"viper.Get":       TaintConfig,
	"viper.GetString": TaintConfig,
	"config.Get":      TaintConfig,
	"process.env":     TaintConfig,
	"dotenv":          TaintConfig,
	"os.environ":      TaintConfig,

	// db_result: database query results
	"sql.Rows":        TaintDBResult,
	"sql.Row":         TaintDBResult,
	"db.Query":        TaintDBResult,
	"db.QueryRow":     TaintDBResult,
	"db.QueryContext": TaintDBResult,
	"tx.Query":        TaintDBResult,
	"gorm.DB":         TaintDBResult,
	"Find(":           TaintDBResult,
	"cursor.fetchone": TaintDBResult,
	"cursor.fetchall": TaintDBResult,
	"cursor.execute":  TaintDBResult,
	"prisma":          TaintDBResult,

	// external_api: HTTP response bodies
	"http.Get":      TaintExternalAPI,
	"http.Post":     TaintExternalAPI,
	"http.Response": TaintExternalAPI,
	"resp.Body":     TaintExternalAPI,
	"fetch(":        TaintExternalAPI,
	"axios.get":     TaintExternalAPI,
	"axios.post":    TaintExternalAPI,
	"requests.get":  TaintExternalAPI,
	"requests.post": TaintExternalAPI,
	"httpx.get":     TaintExternalAPI,
	"httpx.post":    TaintExternalAPI,
}

// PropagateTaint marks nodes with taint based on their data sources.
// Uses DATA_FLOWS_TO edges for propagation (forward from taint sources).
//
// Taint sources:
//   - http.Request parameters → user_input
//   - os.Getenv / config reads → config
//   - sql.Rows / db.Query results → db_result
//   - http.Response body → external_api
//
// INV-P5-04: Does not create new edges, only marks node properties.
func PropagateTaint(buf *GraphBuffer) int {
	buf.mu.RLock()

	// Phase 1: Identify taint source nodes by signature/name patterns.
	taintedNodes := make(map[string][]TaintKind)
	for qname, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		taints := identifyTaints(n)
		if len(taints) > 0 {
			taintedNodes[qname] = taints
		}
	}

	// Build forward adjacency from DATA_FLOWS_TO edges.
	forwardAdj := make(map[string][]string)
	for _, e := range buf.Edges {
		if e.Kind == EdgeDataFlowsTo && e.SourceQName != "" && e.TargetQName != "" {
			forwardAdj[e.SourceQName] = append(forwardAdj[e.SourceQName], e.TargetQName)
		}
	}
	buf.mu.RUnlock()

	if len(taintedNodes) == 0 {
		return 0
	}

	// Phase 2: BFS propagation along DATA_FLOWS_TO edges (max 5 hops).
	const maxDepth = 5
	allTaints := make(map[string][]TaintKind)
	for qname, taints := range taintedNodes {
		allTaints[qname] = taints
	}

	visited := make(map[string]bool)
	queue := make([]string, 0, len(taintedNodes))
	depths := make(map[string]int)
	for qname := range taintedNodes {
		queue = append(queue, qname)
		visited[qname] = true
		depths[qname] = 0
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curDepth := depths[cur]
		if curDepth >= maxDepth {
			continue
		}

		for _, next := range forwardAdj[cur] {
			if visited[next] {
				continue
			}
			visited[next] = true
			depths[next] = curDepth + 1
			allTaints[next] = mergeTaints(allTaints[next], allTaints[cur])
			queue = append(queue, next)
		}
	}

	// Phase 3: Apply taints to nodes (INV-P5-04: only marks, no new edges).
	count := 0
	buf.mu.Lock()
	for qname, taints := range allTaints {
		if n, ok := buf.Nodes[qname]; ok {
			n.Taints = dedup(taints)
			count++
		}
	}
	buf.mu.Unlock()

	if count > 0 {
		slog.Info("PropagateTaint: nodes marked", "tainted_sources", len(taintedNodes), "total_propagated", count)
	}
	return count
}

// identifyTaints checks a node's signature and name against known taint source patterns.
func identifyTaints(n *Node) []TaintKind {
	var taints []TaintKind
	seen := make(map[TaintKind]bool)

	combined := n.Signature + " " + n.Name
	for pattern, kind := range taintSources {
		if seen[kind] {
			continue
		}
		if strings.Contains(combined, pattern) {
			taints = append(taints, kind)
			seen[kind] = true
		}
	}
	return taints
}

func mergeTaints(existing, incoming []TaintKind) []TaintKind {
	if len(existing) == 0 {
		return incoming
	}
	seen := make(map[TaintKind]bool, len(existing))
	for _, t := range existing {
		seen[t] = true
	}
	result := append([]TaintKind{}, existing...)
	for _, t := range incoming {
		if !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
	}
	return result
}

func dedup(taints []TaintKind) []TaintKind {
	if len(taints) <= 1 {
		return taints
	}
	seen := make(map[TaintKind]bool, len(taints))
	result := make([]TaintKind, 0, len(taints))
	for _, t := range taints {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}
