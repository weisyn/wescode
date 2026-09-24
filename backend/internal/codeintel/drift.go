package codeintel

import (
	"database/sql"
	"fmt"
)

// DriftAlert represents a detected architectural drift.
type DriftAlert struct {
	Kind        string  `json:"kind"` // "dependency_cycle_forming" | "complexity_spike" | "interface_instability"
	Description string  `json:"description"`
	Severity    string  `json:"severity"` // "warning" | "critical"
	Symbol      string  `json:"symbol,omitempty"`
	Metric      float64 `json:"metric"`
}

// DetectDrifts analyzes node_history to find architectural drift patterns.
func DetectDrifts(db *sql.DB) ([]DriftAlert, error) {
	var alerts []DriftAlert

	// 1. Complexity spike: line_count growth > 50% across last 3 snapshots.
	spikes, err := detectComplexitySpikes(db)
	if err != nil {
		return nil, fmt.Errorf("complexity spikes: %w", err)
	}
	alerts = append(alerts, spikes...)

	// 2. Interface instability: high-caller symbols whose effects/line_count changed significantly.
	instabilities, err := detectInterfaceInstability(db)
	if err != nil {
		return nil, fmt.Errorf("interface instability: %w", err)
	}
	alerts = append(alerts, instabilities...)

	// 3. Dependency cycle forming: caller_count growing steadily (import accumulation proxy).
	cycles, err := detectDependencyCycleForming(db)
	if err != nil {
		return nil, fmt.Errorf("dependency cycle forming: %w", err)
	}
	alerts = append(alerts, cycles...)

	return alerts, nil
}

func detectComplexitySpikes(db *sql.DB) ([]DriftAlert, error) {
	rows, err := db.Query(`
		WITH ranked AS (
			SELECT symbol_name, line_count, snapshot_at,
				ROW_NUMBER() OVER (PARTITION BY symbol_name ORDER BY snapshot_at DESC) AS rn
			FROM node_history
		),
		recent AS (
			SELECT symbol_name,
				MAX(CASE WHEN rn = 1 THEN line_count END) AS latest,
				MAX(CASE WHEN rn = 3 THEN line_count END) AS third
			FROM ranked WHERE rn <= 3
			GROUP BY symbol_name
			HAVING COUNT(*) >= 3
		)
		SELECT symbol_name, latest, third FROM recent
		WHERE third > 0 AND CAST(latest - third AS REAL) / third > 0.5
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []DriftAlert
	for rows.Next() {
		var sym string
		var latest, third int
		if err := rows.Scan(&sym, &latest, &third); err != nil {
			continue
		}
		growth := float64(latest-third) / float64(third) * 100
		severity := "warning"
		if growth > 100 {
			severity = "critical"
		}
		alerts = append(alerts, DriftAlert{
			Kind:        "complexity_spike",
			Description: fmt.Sprintf("%s grew from %d to %d lines (+%.0f%%) over 3 snapshots", sym, third, latest, growth),
			Severity:    severity,
			Symbol:      sym,
			Metric:      growth,
		})
	}
	return alerts, rows.Err()
}

func detectInterfaceInstability(db *sql.DB) ([]DriftAlert, error) {
	rows, err := db.Query(`
		WITH ranked AS (
			SELECT symbol_name, caller_count, effects, snapshot_at,
				ROW_NUMBER() OVER (PARTITION BY symbol_name ORDER BY snapshot_at DESC) AS rn
			FROM node_history
		),
		changes AS (
			SELECT symbol_name,
				MAX(CASE WHEN rn = 1 THEN caller_count END) AS latest_callers,
				MAX(CASE WHEN rn = 1 THEN effects END) AS latest_effects,
				MAX(CASE WHEN rn = 2 THEN effects END) AS prev_effects
			FROM ranked WHERE rn <= 2
			GROUP BY symbol_name
			HAVING COUNT(*) >= 2
		)
		SELECT symbol_name, latest_callers, latest_effects, prev_effects FROM changes
		WHERE latest_callers >= 5 AND latest_effects != prev_effects AND prev_effects IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []DriftAlert
	for rows.Next() {
		var sym string
		var callers int
		var latestEff, prevEff string
		if err := rows.Scan(&sym, &callers, &latestEff, &prevEff); err != nil {
			continue
		}
		alerts = append(alerts, DriftAlert{
			Kind:        "interface_instability",
			Description: fmt.Sprintf("%s (used by %d callers) changed effects: '%s' → '%s'", sym, callers, prevEff, latestEff),
			Severity:    "warning",
			Symbol:      sym,
			Metric:      float64(callers),
		})
	}
	return alerts, rows.Err()
}

func detectDependencyCycleForming(db *sql.DB) ([]DriftAlert, error) {
	rows, err := db.Query(`
		WITH ranked AS (
			SELECT symbol_name, caller_count, snapshot_at,
				ROW_NUMBER() OVER (PARTITION BY symbol_name ORDER BY snapshot_at DESC) AS rn
			FROM node_history
		),
		growth AS (
			SELECT symbol_name,
				MAX(CASE WHEN rn = 1 THEN caller_count END) AS c1,
				MAX(CASE WHEN rn = 2 THEN caller_count END) AS c2,
				MAX(CASE WHEN rn = 3 THEN caller_count END) AS c3
			FROM ranked WHERE rn <= 3
			GROUP BY symbol_name
			HAVING COUNT(*) >= 3
		)
		SELECT symbol_name, c1, c2, c3 FROM growth
		WHERE c1 > c2 AND c2 > c3 AND c1 >= 10
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []DriftAlert
	for rows.Next() {
		var sym string
		var c1, c2, c3 int
		if err := rows.Scan(&sym, &c1, &c2, &c3); err != nil {
			continue
		}
		alerts = append(alerts, DriftAlert{
			Kind:        "dependency_cycle_forming",
			Description: fmt.Sprintf("%s caller count growing steadily: %d → %d → %d (possible coupling accumulation)", sym, c3, c2, c1),
			Severity:    "warning",
			Symbol:      sym,
			Metric:      float64(c1),
		})
	}
	return alerts, rows.Err()
}
