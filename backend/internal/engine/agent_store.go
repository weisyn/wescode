package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	wesagent "github.com/weisyn/wesgine/agent"
)

// userAgentStore is the durable shadow of the engine's in-memory agent store.
//
// wesgine mints a fresh in-memory agent.Store on every Cell.Start
// (internal/cell/subsystems.go) and CellSpec.OnStarted puts re-seeding on the
// application. Preset agents can be replayed from the embedded YAML because
// their definition lives in this binary; an agent the user created here has no
// second copy, so without this table it survives only until the temperature
// scheduler Cools an idle Cell — fifteen minutes, no restart required — and
// then disappears with no error and no log line.
//
// Rows carry the engine's own AgentConfig verbatim, so restoring is a plain
// Register of what the engine last held: defaults wesapp applied (Role←Name,
// Goal←"帮助用户") and its packed Metadata come back unchanged. A column per
// field would be a second encoder of the same struct, and it would silently
// drop whatever wesgine adds to AgentConfig next.
type userAgentStore struct {
	db *sql.DB
}

func newUserAgentStore(ctx context.Context, db *sql.DB) (*userAgentStore, error) {
	if db == nil {
		return nil, fmt.Errorf("userAgentStore: nil app db")
	}
	const ddl = `CREATE TABLE IF NOT EXISTS wc_agents (
		id         TEXT PRIMARY KEY,
		config     TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return nil, fmt.Errorf("userAgentStore: migrate: %w", err)
	}
	return &userAgentStore{db: db}, nil
}

// Put mirrors one agent definition. Callers pass the config read back from the
// engine, never one they rebuilt from the request.
func (s *userAgentStore) Put(ctx context.Context, cfg wesagent.AgentConfig) error {
	blob, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("userAgentStore: marshal %q: %w", cfg.ID, err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO wc_agents (id, config, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET config = excluded.config, updated_at = excluded.updated_at`,
		cfg.ID, string(blob), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("userAgentStore: put %q: %w", cfg.ID, err)
	}
	return nil
}

func (s *userAgentStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM wc_agents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("userAgentStore: delete %q: %w", id, err)
	}
	return nil
}

// agentRegistrar is the one method a restore needs. Narrowing it here (rather
// than taking *wesgine.Cell, whose construction needs a Hypervisor and a data
// directory) is what lets the round-trip this table exists for be asserted in
// a test instead of only in a running IDE.
type agentRegistrar interface {
	Register(ctx context.Context, cfg wesagent.AgentConfig) error
}

// RestoreInto re-registers every mirrored agent into the freshly built engine
// store and reports how many landed.
//
// A row that cannot be decoded or registered is logged and skipped rather than
// failing the restore: one truncated blob must not cost the user every other
// agent, and returning early would look exactly like the data loss this table
// exists to prevent.
func (s *userAgentStore) RestoreInto(ctx context.Context, handle agentRegistrar) (int, error) {
	if handle == nil {
		return 0, fmt.Errorf("userAgentStore: nil registrar")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, config FROM wc_agents ORDER BY updated_at`)
	if err != nil {
		return 0, fmt.Errorf("userAgentStore: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	restored := 0
	for rows.Next() {
		var id, blob string
		if err := rows.Scan(&id, &blob); err != nil {
			return restored, fmt.Errorf("userAgentStore: scan: %w", err)
		}
		var cfg wesagent.AgentConfig
		if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
			slog.Error("[engine] user agent row undecodable — skipped", "id", id, "error", err)
			continue
		}
		if err := handle.Register(ctx, cfg); err != nil {
			slog.Error("[engine] user agent restore failed", "id", id, "error", err)
			continue
		}
		restored++
	}
	if err := rows.Err(); err != nil {
		return restored, fmt.Errorf("userAgentStore: iterate: %w", err)
	}
	return restored, nil
}
