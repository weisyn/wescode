package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/codeintel"
)

// MetricsPersister periodically flushes in-memory metrics to wescode-app.db.
// Covers: RetrievalMetrics (Tier 3), TCR stats (Tier 1), verification stats.
// Flush interval: every 5 minutes or on Close.
type MetricsPersister struct {
	db        *sql.DB
	retrieval *codeintel.RetrievalMetrics
	tcr       *TCRTracker
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
}

const metricsFlushInterval = 5 * time.Minute

func NewMetricsPersister(db *sql.DB, retrieval *codeintel.RetrievalMetrics, tcr *TCRTracker) *MetricsPersister {
	return &MetricsPersister{
		db:        db,
		retrieval: retrieval,
		tcr:       tcr,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start creates the metrics table and begins periodic flushing.
func (mp *MetricsPersister) Start(ctx context.Context) error {
	if err := mp.migrate(ctx); err != nil {
		return err
	}
	go mp.loop()
	return nil
}

func (mp *MetricsPersister) migrate(ctx context.Context) error {
	_, err := mp.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS wc_metrics (
			id         INTEGER PRIMARY KEY,
			kind       TEXT NOT NULL,
			snapshot   TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
		CREATE INDEX IF NOT EXISTS idx_wc_metrics_kind_created ON wc_metrics(kind, created_at);
	`)
	return err
}

func (mp *MetricsPersister) loop() {
	defer close(mp.done)
	ticker := time.NewTicker(metricsFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			mp.flush()
		case <-mp.stop:
			mp.flush()
			return
		}
	}
}

func (mp *MetricsPersister) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if mp.retrieval != nil {
		snap := mp.retrieval.Snapshot()
		mp.writeSnapshot(ctx, "retrieval", snap)
	}
	if mp.tcr != nil {
		snap := mp.tcr.Snapshot()
		mp.writeSnapshot(ctx, "tcr", snap)
	}
}

func (mp *MetricsPersister) writeSnapshot(ctx context.Context, kind string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		slog.Warn("[metrics_persist] marshal failed", "kind", kind, "error", err)
		return
	}
	_, err = mp.db.ExecContext(ctx,
		`INSERT INTO wc_metrics (kind, snapshot) VALUES (?, ?)`,
		kind, string(raw),
	)
	if err != nil {
		slog.Warn("[metrics_persist] write failed", "kind", kind, "error", err)
	}
}

// Close stops the flush loop and performs a final flush.
func (mp *MetricsPersister) Close() {
	mp.once.Do(func() {
		close(mp.stop)
		<-mp.done
	})
}

// LatestSnapshot reads the most recent snapshot of a given kind from the DB.
func (mp *MetricsPersister) LatestSnapshot(ctx context.Context, kind string) (json.RawMessage, error) {
	var raw string
	err := mp.db.QueryRowContext(ctx,
		`SELECT snapshot FROM wc_metrics WHERE kind = ? ORDER BY id DESC LIMIT 1`,
		kind,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
