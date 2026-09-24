package constraints

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Lifecycle tests cover the four transitions that only exist because inference
// is repeated: every re-index re-derives the same constraints from the same
// unchanged code. Without these, learning signals would be silently erased by
// the next index run and every dismissal would last one process lifetime.

// TestAdd_ReinferenceKeepsEarnedState is the core upsert contract. The inferrer
// always emits its own prior (0.6 here); if that overwrote the learned value,
// each re-index would undo every promotion the feedback loop recorded.
func TestAdd_ReinferenceKeepsEarnedState(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()

	first := r.AddInferred(inferred(root, "ctx-propagate", 0.6, CheckNoPanic))
	if first == nil {
		t.Fatal("Add rejected a valid constraint")
	}
	// The loop learns: user rejections and passing tests push it to certainty.
	r.PromoteByID(first.ID, 0.3)
	learned := r.Get(first.ID)
	if learned.Status != StatusActive {
		t.Fatalf("precondition: status = %s, want active", learned.Status)
	}

	// Same code, next index pass: the inferrer re-emits its unchanged prior with
	// refreshed derivable fields.
	redrawn := inferred(root, "ctx-propagate", 0.6, CheckNoPanic, func(c *Constraint) {
		c.Rule = "rewritten by a newer inferrer"
	})
	stored := r.AddInferred(redrawn)
	if stored == nil {
		t.Fatal("re-inference was rejected")
	}

	if stored.Confidence != learned.Confidence {
		t.Errorf("confidence = %.2f, want %.2f: re-inference erased the learned value",
			stored.Confidence, learned.Confidence)
	}
	if stored.UsageCount != learned.UsageCount {
		t.Errorf("usage count = %d, want %d", stored.UsageCount, learned.UsageCount)
	}
	if !stored.CreatedAt.Equal(learned.CreatedAt) {
		t.Error("CreatedAt moved: the constraint is not new, it was re-derived")
	}
	if stored.Status != StatusActive {
		t.Errorf("status = %s, want active", stored.Status)
	}
	// Derivable fields do refresh — the point is to keep lifecycle state, not to
	// freeze the rule text and checker at whatever the first pass produced.
	if stored.Rule != "rewritten by a newer inferrer" {
		t.Errorf("rule = %q, want the refreshed text", stored.Rule)
	}
}

// TestAdd_HumanConfirmationOutranksMachinePrior is the one direction where the
// incoming value must win: prev >= incoming keeps prev, so a 1.0 confirmation
// arriving over a machine estimate has to land, and its TTL=0 must stick
// through the following re-inference passes.
func TestAdd_HumanConfirmationOutranksMachinePrior(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := r.AddInferred(inferred(root, "sec-password", 0.6, CheckPlaintextSecret))

	if !r.ConfirmByID(c.ID) {
		t.Fatal("ConfirmByID missed the constraint")
	}
	confirmed := r.Get(c.ID)
	if confirmed.Confidence != 1.0 || confirmed.TTL != 0 {
		t.Fatalf("precondition: confidence=%.2f ttl=%d", confirmed.Confidence, confirmed.TTL)
	}

	// The inferrer knows nothing about the confirmation and re-emits its prior
	// with the default 90-day TTL.
	stored := r.AddInferred(inferred(root, "sec-password", 0.6, CheckPlaintextSecret))
	if stored.Confidence != 1.0 {
		t.Errorf("confidence = %.2f, want 1.0: re-inference demoted a confirmed constraint",
			stored.Confidence)
	}
	if stored.TTL != 0 {
		t.Errorf("TTL = %d, want 0: a confirmed constraint must not expire", stored.TTL)
	}
	if stored.ShouldRetire() {
		t.Error("a human-confirmed constraint must never be a decay candidate")
	}
}

// TestAdd_DismissedConstraintStaysRetired: the code that produced the
// constraint has not changed, so the next pass re-derives it identically. A
// dismissal that is not durable is a dismissal that lasts until the next index.
func TestAdd_DismissedConstraintStaysRetired(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := r.AddInferred(inferred(root, "no-panic", 0.9, CheckNoPanic))
	if !r.DismissByID(c.ID) {
		t.Fatal("DismissByID missed the constraint")
	}

	stored := r.AddInferred(inferred(root, "no-panic", 0.9, CheckNoPanic))
	if stored == nil {
		t.Fatal("re-inference was rejected outright; expected a tombstoned row back")
	}
	if stored.Status != StatusRetired || stored.RetiredBy != RetiredByUser {
		t.Errorf("status=%s retired_by=%q: re-inference resurrected a dismissed constraint",
			stored.Status, stored.RetiredBy)
	}
	// A tombstone is not enforceable: it must not reach any advisory or overlay.
	if got := r.MatchingFile(filepath.Join(root, "main.go")); len(got) != 0 {
		t.Errorf("MatchingFile returned %d dismissed constraints; want 0", len(got))
	}
	if n := r.CountForRoot(root); n != 0 {
		t.Errorf("CountForRoot = %d, want 0: a tombstone is not coverage", n)
	}
}

// TestAdd_MechanicalRetirementIsReversible is the counterpart: decay and
// reconcile mean "the evidence is gone", not "the human said no". If the
// pattern comes back, the constraint has to come back with it.
func TestAdd_MechanicalRetirementIsReversible(t *testing.T) {
	root := t.TempDir()
	for _, by := range []RetiredBy{RetiredByDecay, RetiredByReconcile} {
		t.Run(string(by), func(t *testing.T) {
			r := NewRegistry()
			c := r.AddInferred(inferred(root, "sec-password", 0.9, CheckPlaintextSecret))
			r.mu.Lock()
			r.constraints[c.ID].Status = StatusRetired
			r.constraints[c.ID].RetiredBy = by
			r.mu.Unlock()

			stored := r.AddInferred(inferred(root, "sec-password", 0.9, CheckPlaintextSecret))
			if stored.Status != StatusActive {
				t.Errorf("status = %s, want active: %s retirement must not outlive the evidence",
					stored.Status, by)
			}
			if stored.RetiredBy != "" {
				t.Errorf("retired_by = %q, want empty after revival", stored.RetiredBy)
			}
		})
	}
}

// TestAdd_HysteresisBandDoesNotFlipOnReindex: confidence in [0.5, 0.8) is the
// undecided band. Resolving it by threshold alone would make a constraint
// oscillate active/candidate across index runs with no new evidence, and every
// flip is a visible change in what the model gets told.
func TestAdd_HysteresisBandDoesNotFlipOnReindex(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		prev Status
		want Status
	}{
		{"active stays active", StatusActive, StatusActive},
		{"candidate stays candidate", StatusCandidate, StatusCandidate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			c := inferred(root, "band", 0.6, CheckNoPanic, func(c *Constraint) {
				c.Status = tt.prev
			})
			if r.Add(c) == nil {
				t.Fatal("Add rejected a valid constraint")
			}

			stored := r.Add(inferred(root, "band", 0.6, CheckNoPanic))
			if stored.Status != tt.want {
				t.Errorf("status = %s, want %s (confidence 0.6 is inside the band)",
					stored.Status, tt.want)
			}
		})
	}
}

// TestDemoteByID_TakesConstraintOffline closes the loop the other way: an
// inferred constraint the AI keeps ignoring while tests stay green is wrong
// about the codebase, and must stop producing advisories without being deleted
// — the evidence may come back.
func TestDemoteByID_TakesConstraintOffline(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := r.AddInferred(inferred(root, "no-panic", 0.9, CheckNoPanic))
	file := filepath.Join(root, "main.go")
	if got := r.MatchingFile(file); len(got) != 1 {
		t.Fatalf("precondition: MatchingFile = %d, want 1", len(got))
	}

	r.DemoteByID(c.ID, 0.5) // 0.9 -> 0.4, below the deactivate threshold
	demoted := r.Get(c.ID)
	if demoted.Status != StatusCandidate {
		t.Errorf("status = %s, want candidate after falling below the threshold", demoted.Status)
	}
	if got := r.MatchingFile(file); len(got) != 0 {
		t.Errorf("MatchingFile returned %d demoted constraints; want 0", len(got))
	}
	// Offline, not gone: it is still addressable and can earn its way back.
	if r.Get(c.ID) == nil {
		t.Fatal("demotion deleted the constraint")
	}
	r.PromoteByID(c.ID, 0.5)
	if got := r.MatchingFile(file); len(got) != 1 {
		t.Errorf("MatchingFile = %d after re-promotion, want 1", len(got))
	}
}

// TestPersistence_UserTombstoneSurvivesRestart is why the two retirement
// reasons persist differently. A dismissal has to outlive the process; a
// decayed row must not, or a pattern that returns would stay suppressed
// forever by a tombstone nobody wrote.
func TestPersistence_UserTombstoneSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	db := openConstraintDB(t)

	r := NewRegistry()
	dismissed := r.AddInferred(inferred(root, "dismissed", 0.9, CheckNoPanic))
	decayed := r.AddInferred(inferred(root, "decayed", 0.9, CheckPlaintextSecret))
	kept := r.AddInferred(inferred(root, "kept", 0.9, CheckErrDiscard))
	r.DismissByID(dismissed.ID)
	r.mu.Lock()
	r.constraints[decayed.ID].Status = StatusRetired
	r.constraints[decayed.ID].RetiredBy = RetiredByDecay
	r.mu.Unlock()

	if err := r.SaveToSQLite(db); err != nil {
		t.Fatalf("SaveToSQLite: %v", err)
	}

	// Restart: a fresh registry reading the same table.
	reloaded := NewRegistry()
	if err := reloaded.LoadFromSQLite(db); err != nil {
		t.Fatalf("LoadFromSQLite: %v", err)
	}

	got := reloaded.Get(dismissed.ID)
	if got == nil {
		t.Fatal("user tombstone did not survive the restart: the dismissal is undone")
	}
	if got.Status != StatusRetired || got.RetiredBy != RetiredByUser {
		t.Errorf("reloaded tombstone: status=%s retired_by=%q", got.Status, got.RetiredBy)
	}
	if reloaded.Get(decayed.ID) != nil {
		t.Error("a decayed row persisted: the constraint can never come back if the pattern does")
	}
	if c := reloaded.Get(kept.ID); c == nil || c.Status != StatusActive {
		t.Error("an active constraint was lost across the restart")
	}

	// And the tombstone still refuses resurrection after the round trip — the
	// state that matters is the one loaded from disk, not the one in memory.
	after := reloaded.AddInferred(inferred(root, "dismissed", 0.9, CheckNoPanic))
	if after.Status != StatusRetired {
		t.Errorf("status = %s: a reloaded tombstone must still block re-inference", after.Status)
	}
}

// TestAdd_EvidenceRefreshesButSurvivesPathsThatCollectNone covers the asymmetry
// in the upsert. Two kinds of writer land on the same ID: an inferrer, which
// looked at the code and can say which packages form the cycle, and the feedback
// paths (teach, L2.5), which carry a predicate and a confidence delta but no
// finding. Newest inference wins because the cycle may have changed shape; a
// feedback write must not blank the trail, or the human panel loses the only
// answer to "why does this constraint exist" the moment anyone rejects an edit.
func TestAdd_EvidenceRefreshesButSurvivesPathsThatCollectNone(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()

	first := r.AddInferred(inferred(root, "import-cycle", 0.6, CheckNoPanic, func(c *Constraint) {
		c.Evidence = []string{"internal/a -> internal/b -> internal/a"}
	}))
	if len(first.Evidence) != 1 {
		t.Fatalf("evidence = %v, want the inferrer's finding", first.Evidence)
	}

	// Next pass, the cycle grew a hop. The stale two-package trail would send a
	// reviewer looking at the wrong edge.
	refreshed := r.AddInferred(inferred(root, "import-cycle", 0.6, CheckNoPanic, func(c *Constraint) {
		c.Evidence = []string{"internal/a -> internal/b -> internal/c -> internal/a"}
	}))
	if len(refreshed.Evidence) != 1 || refreshed.Evidence[0] != "internal/a -> internal/b -> internal/c -> internal/a" {
		t.Errorf("evidence = %v, want the current pass's finding", refreshed.Evidence)
	}

	// A feedback write on the same ID: same rule, no finding attached.
	taught := r.Add(inferred(root, "import-cycle", 0.55, CheckNoPanic, func(c *Constraint) {
		c.Source = SourceLearned
		c.Evidence = nil
	}))
	if len(taught.Evidence) != 1 {
		t.Errorf("evidence = %v: a feedback write erased the inferrer's trail", taught.Evidence)
	}
}

// TestPersistence_EvidenceSurvivesRestart: evidence rides the data_json blob
// rather than its own column, so a dropped struct tag would lose it silently —
// the row still loads, the panel just stops explaining itself.
func TestPersistence_EvidenceSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	db := openConstraintDB(t)

	r := NewRegistry()
	stored := r.AddInferred(inferred(root, "missing-guard", 0.9, CheckNoPanic, func(c *Constraint) {
		c.Evidence = []string{"handler.go:42 calls Save without a tx", "handler.go:88 same"}
	}))
	if err := r.SaveToSQLite(db); err != nil {
		t.Fatalf("SaveToSQLite: %v", err)
	}

	reloaded := NewRegistry()
	if err := reloaded.LoadFromSQLite(db); err != nil {
		t.Fatalf("LoadFromSQLite: %v", err)
	}

	got := reloaded.Get(stored.ID)
	if got == nil {
		t.Fatal("the constraint did not survive the restart")
	}
	if len(got.Evidence) != 2 {
		t.Fatalf("evidence = %v, want both call sites", got.Evidence)
	}
	for i, want := range stored.Evidence {
		if got.Evidence[i] != want {
			t.Errorf("evidence[%d] = %q, want %q", i, got.Evidence[i], want)
		}
	}
}

// TestLoadFromSQLite_DropsUncheckableRows is the DEV-1 gate at the storage
// boundary. Rows from the natural-language era have no Checker and can never
// produce a verdict; loading them back would reintroduce exactly the prose the
// refactor removed.
func TestLoadFromSQLite_DropsUncheckableRows(t *testing.T) {
	root := t.TempDir()
	db := openConstraintDB(t)

	// A legacy row, written the way the old inferrer wrote it: rule prose, a
	// source that no longer exists, and no checker.
	if _, err := db.Exec(`INSERT INTO constraints
		(id, rule, kind, priority, status, confidence, source, scope, ttl_days,
		 usage_count, last_used, created_at, data_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"agents-md-1", "禁止在 handler 中直连数据库", "architecture", int(PriorityArchitecture),
		string(StatusActive), 0.9, "agents_md", "", 90, 0, time.Now().Unix(), time.Now().Unix(),
		`{"id":"agents-md-1","rule":"禁止在 handler 中直连数据库","source":"agents_md","status":"active"}`,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	// A row with a live source but no checker: same problem, different origin.
	if _, err := db.Exec(`INSERT INTO constraints
		(id, rule, kind, priority, status, confidence, source, scope, ttl_days,
		 usage_count, last_used, created_at, data_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"checkerless", "keep it clean", "quality", int(PriorityQuality),
		string(StatusActive), 0.9, SourceInferred, "", 90, 0, time.Now().Unix(), time.Now().Unix(),
		`{"id":"checkerless","rule":"keep it clean","source":"inferred","status":"active"}`,
	); err != nil {
		t.Fatalf("seed checkerless row: %v", err)
	}

	r := NewRegistry()
	valid := r.AddInferred(inferred(root, "valid", 0.9, CheckNoPanic))
	if err := r.SaveToSQLite(db); err != nil {
		t.Fatalf("SaveToSQLite: %v", err)
	}

	reloaded := NewRegistry()
	if err := reloaded.LoadFromSQLite(db); err != nil {
		t.Fatalf("LoadFromSQLite: %v", err)
	}
	if reloaded.Get("agents-md-1") != nil {
		t.Error("an agents_md row survived the load: rule prose is back in the registry")
	}
	if reloaded.Get("checkerless") != nil {
		t.Error("a checkerless row survived the load: it can never produce a verdict")
	}
	if reloaded.Get(valid.ID) == nil {
		t.Error("the checkable constraint was dropped along with the legacy rows")
	}

	// The legacy row is deleted, not just skipped, so it stops being re-read on
	// every startup.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM constraints WHERE source = 'agents_md'`).Scan(&n); err != nil {
		t.Fatalf("count agents_md rows: %v", err)
	}
	if n != 0 {
		t.Errorf("%d agents_md rows left in the table; want 0 (DEV-1)", n)
	}
}

// openConstraintDB creates the constraints table exactly as codeintel/schema.go
// declares it. The schema lives in the parent package, which imports this one,
// so the DDL cannot be shared without an import cycle; a drift between the two
// shows up here as a failing insert.
func openConstraintDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "code.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE constraints (
		id          TEXT PRIMARY KEY,
		rule        TEXT NOT NULL,
		kind        TEXT NOT NULL,
		priority    INTEGER NOT NULL,
		status      TEXT NOT NULL DEFAULT 'candidate',
		confidence  REAL NOT NULL DEFAULT 0.5,
		source      TEXT NOT NULL,
		scope       TEXT NOT NULL DEFAULT '',
		ttl_days    INTEGER NOT NULL DEFAULT 30,
		usage_count INTEGER NOT NULL DEFAULT 0,
		last_used   INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL DEFAULT 0,
		data_json   TEXT NOT NULL DEFAULT '{}'
	)`); err != nil {
		t.Fatalf("create constraints table: %v", err)
	}
	return db
}
