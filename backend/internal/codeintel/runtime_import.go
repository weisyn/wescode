package codeintel

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RuntimeImporter loads optional runtime data (OTEL traces, coverage, pprof)
// and annotates CKG nodes with frequency/coverage/CPU attributes.
// INV-P6-03: Opt-in only — no config = no execution.
type RuntimeImporter struct {
	DB *sql.DB
}

// ImportOTELTraces parses an OTEL JSON trace file and sets runtime_frequency
// on matching CKG nodes (by function name).
func (ri *RuntimeImporter) ImportOTELTraces(tracePath string) (int, error) {
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return 0, fmt.Errorf("read OTEL trace file: %w", err)
	}

	var root otelTraceRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return 0, fmt.Errorf("parse OTEL trace JSON: %w", err)
	}

	freq := make(map[string]int)
	for _, rs := range root.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				if span.Name != "" {
					freq[span.Name]++
				}
			}
		}
	}

	if len(freq) == 0 {
		return 0, nil
	}

	tx, err := ri.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE symbols SET runtime_frequency = ? WHERE name = ? AND kind IN ('function','method')`)
	if err != nil {
		return 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	updated := 0
	for funcName, count := range freq {
		res, err := stmt.Exec(count, funcName)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated += int(n)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// ImportCoverage parses a Go coverage profile and sets coverage (0.0-1.0)
// on matching CKG nodes.
// Format: "mode: set" header, then "file:start.col,end.col count" lines.
func (ri *RuntimeImporter) ImportCoverage(coverPath string) (int, error) {
	f, err := os.Open(coverPath)
	if err != nil {
		return 0, fmt.Errorf("open coverage file: %w", err)
	}
	defer f.Close()

	type fileCov struct {
		covered int
		total   int
	}
	fileStats := make(map[string]*fileCov)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		// Format: "pkg/file.go:10.5,20.3 1 1"
		// The last two numbers are statement count and hit count.
		colonIdx := strings.LastIndex(line, ":")
		if colonIdx < 0 {
			continue
		}
		filePath := line[:colonIdx]

		parts := strings.Fields(line[colonIdx+1:])
		if len(parts) < 3 {
			continue
		}

		fc, ok := fileStats[filePath]
		if !ok {
			fc = &fileCov{}
			fileStats[filePath] = fc
		}
		fc.total++
		if parts[2] != "0" {
			fc.covered++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan coverage: %w", err)
	}

	if len(fileStats) == 0 {
		return 0, nil
	}

	tx, err := ri.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE symbols SET coverage = ? WHERE file_path LIKE '%' || ? AND kind IN ('function','method')`)
	if err != nil {
		return 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	updated := 0
	for filePath, fc := range fileStats {
		var cov float64
		if fc.total > 0 {
			cov = float64(fc.covered) / float64(fc.total)
		}
		res, err := stmt.Exec(cov, filePath)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated += int(n)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// ImportPprof parses a CPU pprof text profile and sets cpu_pct on matching nodes.
// Accepts `go tool pprof -text` output format:
//
//	flat  flat%   sum%  cum   cum%  function
//
// We extract the cum% and function name.
func (ri *RuntimeImporter) ImportPprof(pprofPath string) (int, error) {
	f, err := os.Open(pprofPath)
	if err != nil {
		return 0, fmt.Errorf("open pprof file: %w", err)
	}
	defer f.Close()

	cpuMap := make(map[string]float64)
	scanner := bufio.NewScanner(f)
	headerSeen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 6 && fields[0] == "flat" {
			headerSeen = true
			continue
		}
		if !headerSeen || len(fields) < 6 {
			continue
		}

		// cum% is field[4], function is field[5]
		cumPctStr := strings.TrimSuffix(fields[4], "%")
		var cumPct float64
		if _, err := fmt.Sscanf(cumPctStr, "%f", &cumPct); err != nil {
			continue
		}

		funcName := fields[5]
		// Extract just the function name from the qualified path.
		if idx := strings.LastIndex(funcName, "."); idx >= 0 {
			funcName = funcName[idx+1:]
		}
		// Remove parenthesized receiver prefix like "(*T)."
		funcName = strings.TrimPrefix(funcName, "(*")
		if idx := strings.Index(funcName, ")."); idx >= 0 {
			funcName = funcName[idx+2:]
		}

		if existing, ok := cpuMap[funcName]; !ok || cumPct > existing {
			cpuMap[funcName] = cumPct
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan pprof: %w", err)
	}

	if len(cpuMap) == 0 {
		return 0, nil
	}

	tx, err := ri.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE symbols SET cpu_pct = ? WHERE name = ? AND kind IN ('function','method')`)
	if err != nil {
		return 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	updated := 0
	for funcName, pct := range cpuMap {
		res, err := stmt.Exec(pct, funcName)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated += int(n)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// OTEL trace JSON structure (simplified).
type otelTraceRoot struct {
	ResourceSpans []otelResourceSpan `json:"resourceSpans"`
}

type otelResourceSpan struct {
	ScopeSpans []otelScopeSpan `json:"scopeSpans"`
}

type otelScopeSpan struct {
	Spans []otelSpan `json:"spans"`
}

type otelSpan struct {
	Name string `json:"name"`
}
