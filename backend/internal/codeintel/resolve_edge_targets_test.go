package codeintel

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/weisyn/wescode/internal/treesitter"
)

// ResolveEdgeTargets is the single point where an edge acquires a target_id
// after the buffer flush, and all three write paths (full pipeline, single-file
// save, watcher batch) call it unconditionally. That only works if two things
// hold, and neither was covered before this file existed: each of the five
// layers binds the symbol it claims to, and a second run over an already
// resolved graph changes nothing.
//
// The tests below drive the resolver against hand-built symbol tables rather
// than parsed source, because the interesting inputs are corpus *shapes* —
// "this name is unique among exported symbols but not globally" — which are
// tedious to arrange in Go source and trivial to state as rows.

type testSym struct {
	name     string
	kind     string
	parent   string
	pkg      string
	exported bool
	file     string
}

func insertSyms(t *testing.T, ci *CodeIndex, syms ...testSym) map[string]int64 {
	t.Helper()
	ids := make(map[string]int64, len(syms))
	for _, s := range syms {
		if s.kind == "" {
			s.kind = "function"
		}
		if s.file == "" {
			s.file = "/x/" + s.pkg + "/f.go"
		}
		exported := 0
		if s.exported {
			exported = 1
		}
		res, err := ci.writerDB.Exec(
			`INSERT INTO symbols (file_path, kind, name, parent, package_path, exported, line_start, line_end)
			 VALUES (?, ?, ?, ?, ?, ?, 1, 1)`,
			s.file, s.kind, s.name, s.parent, s.pkg, exported)
		if err != nil {
			t.Fatalf("insert symbol %q: %v", s.name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		// Keyed by parent-qualified name so a fixture can hold two same-named
		// methods on different types and still address each one.
		key := s.name
		if s.parent != "" {
			key = s.parent + "." + s.name
		} else if s.pkg != "" {
			key = s.pkg + ":" + s.name
		}
		if _, dup := ids[key]; dup {
			t.Fatalf("fixture key %q is not unique; the test cannot address its own symbols", key)
		}
		ids[key] = id
	}
	return ids
}

// insertUnboundEdge adds the row the resolver is supposed to act on: a name and
// no target. The schema refuses to store a bound resolution without a target,
// so `unresolved` is the only legal starting state.
func insertUnboundEdge(t *testing.T, ci *CodeIndex, sourceID int64, targetName string) int64 {
	t.Helper()
	res, err := ci.writerDB.Exec(
		`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
		 VALUES (?, NULL, ?, 'call', ?, 'test')`,
		sourceID, targetName, ResolutionUnresolved)
	if err != nil {
		t.Fatalf("insert unbound edge -> %q: %v", targetName, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func readEdge(t *testing.T, ci *CodeIndex, edgeID int64) (targetID sql.NullInt64, res Resolution) {
	t.Helper()
	if err := ci.writerDB.QueryRow(
		`SELECT target_id, resolution FROM edges WHERE id = ?`, edgeID,
	).Scan(&targetID, &res); err != nil {
		t.Fatalf("read edge %d: %v", edgeID, err)
	}
	return targetID, res
}

func newResolverTestIndex(t *testing.T) (*CodeIndex, context.Context) {
	t.Helper()
	pool := treesitter.NewParserPoolN(1)
	t.Cleanup(pool.Close)
	ci, err := NewCodeIndex(filepath.Join(t.TempDir(), "resolve.db"), pool)
	if err != nil {
		t.Fatalf("NewCodeIndex: %v", err)
	}
	t.Cleanup(func() { ci.Close() })
	return ci, context.Background()
}

// TestResolveEdgeTargets_Layers walks the five layers, one case each, with a
// decoy that would be picked by a wrong layer.
//
// The resolution label is asserted as strictly as the target: `exact` vs
// `inferred` is the difference between "the source named one symbol" and
// "today's corpus happens to hold one candidate", and the second one can stop
// being true when an unrelated file adds a method. Callers that treat them the
// same are the reason the old single float was useless.
func TestResolveEdgeTargets_Layers(t *testing.T) {
	t.Run("layer1_qualified_by_declaring_type_is_exact", func(t *testing.T) {
		ci, ctx := newResolverTestIndex(t)
		ids := insertSyms(t,
			ci,
			testSym{name: "Caller", pkg: "p/a"},
			testSym{name: "Save", parent: "Store", pkg: "p/b"},
			// Decoy: same method name on another type. Layer 1 must not see it.
			testSym{name: "Save", parent: "Cache", pkg: "p/c"},
		)
		e := insertUnboundEdge(t, ci, ids["p/a:Caller"], "Store.Save")

		if err := ci.ResolveEdgeTargets(ctx); err != nil {
			t.Fatal(err)
		}
		tid, res := readEdge(t, ci, e)
		if !tid.Valid || tid.Int64 != ids["Store.Save"] {
			t.Errorf("target_id = %v, want %d (Store.Save)", tid, ids["Store.Save"])
		}
		if res != ResolutionExact {
			t.Errorf("resolution = %q, want %q: the declaring type came from the source", res, ResolutionExact)
		}
	})

	t.Run("layer2_untyped_receiver_is_inferred", func(t *testing.T) {
		ci, ctx := newResolverTestIndex(t)
		ids := insertSyms(t,
			ci,
			testSym{name: "Caller", pkg: "p/a"},
			// No type named `buf`, so layer 1 cannot answer. `Flush` is unique
			// globally, so layer 2 binds it — on uniqueness, not on type.
			testSym{name: "Flush", pkg: "p/b"},
		)
		e := insertUnboundEdge(t, ci, ids["p/a:Caller"], "buf.Flush")

		if err := ci.ResolveEdgeTargets(ctx); err != nil {
			t.Fatal(err)
		}
		tid, res := readEdge(t, ci, e)
		if !tid.Valid || tid.Int64 != ids["p/b:Flush"] {
			t.Errorf("target_id = %v, want %d (Flush)", tid, ids["p/b:Flush"])
		}
		if res != ResolutionInferred {
			t.Errorf("resolution = %q, want %q: the receiver type is unknown, only the name is unique",
				res, ResolutionInferred)
		}
	})

	t.Run("layer3_callers_own_package_is_exact", func(t *testing.T) {
		ci, ctx := newResolverTestIndex(t)
		ids := insertSyms(t,
			ci,
			testSym{name: "Caller", pkg: "p/a"},
			testSym{name: "Helper", pkg: "p/a", exported: true},
			// Decoy in another package. Two `Helper`s means neither the
			// exported nor the global map holds the name, so only layer 3 —
			// which knows the caller's package — can answer.
			testSym{name: "Helper", pkg: "p/b", exported: true},
		)
		e := insertUnboundEdge(t, ci, ids["p/a:Caller"], "Helper")

		if err := ci.ResolveEdgeTargets(ctx); err != nil {
			t.Fatal(err)
		}
		tid, res := readEdge(t, ci, e)
		if !tid.Valid || tid.Int64 != ids["p/a:Helper"] {
			t.Errorf("target_id = %v, want %d (p/a Helper), got the wrong package's symbol",
				tid, ids["p/a:Helper"])
		}
		if res != ResolutionExact {
			t.Errorf("resolution = %q, want %q: the caller's package disambiguates", res, ResolutionExact)
		}
	})

	t.Run("layer4_sole_exported_name_is_inferred", func(t *testing.T) {
		ci, ctx := newResolverTestIndex(t)
		ids := insertSyms(t,
			ci,
			testSym{name: "Caller", pkg: "p/main"},
			testSym{name: "Once", pkg: "p/x", exported: true},
			// Unexported same-name keeps the global map empty, so layer 5
			// cannot answer and layer 4 has to.
			testSym{name: "Once", pkg: "p/y"},
		)
		e := insertUnboundEdge(t, ci, ids["p/main:Caller"], "Once")

		if err := ci.ResolveEdgeTargets(ctx); err != nil {
			t.Fatal(err)
		}
		tid, res := readEdge(t, ci, e)
		if !tid.Valid || tid.Int64 != ids["p/x:Once"] {
			t.Errorf("target_id = %v, want %d (exported Once)", tid, ids["p/x:Once"])
		}
		if res != ResolutionInferred {
			t.Errorf("resolution = %q, want %q", res, ResolutionInferred)
		}
	})

	t.Run("layer5_sole_name_anywhere_is_inferred", func(t *testing.T) {
		ci, ctx := newResolverTestIndex(t)
		ids := insertSyms(t,
			ci,
			testSym{name: "Caller", pkg: "p/main"},
			// Unexported and in another package: layers 3 and 4 both decline.
			testSym{name: "privateHelper", pkg: "p/z"},
		)
		e := insertUnboundEdge(t, ci, ids["p/main:Caller"], "privateHelper")

		if err := ci.ResolveEdgeTargets(ctx); err != nil {
			t.Fatal(err)
		}
		tid, res := readEdge(t, ci, e)
		if !tid.Valid || tid.Int64 != ids["p/z:privateHelper"] {
			t.Errorf("target_id = %v, want %d", tid, ids["p/z:privateHelper"])
		}
		if res != ResolutionInferred {
			t.Errorf("resolution = %q, want %q", res, ResolutionInferred)
		}
	})
}

// TestResolveEdgeTargets_AmbiguousVsUnresolved pins the distinction the old
// `certainty` column could not express: both of these rows are unbound, and
// under the single float both read 1.0. They call for opposite handling —
// `ambiguous` means the read side should show the candidate group
// (queryCallersForAmbiguousTarget), `unresolved` means there is nothing to show
// and the name may belong to another language or a dependency.
func TestResolveEdgeTargets_AmbiguousVsUnresolved(t *testing.T) {
	ci, ctx := newResolverTestIndex(t)
	ids := insertSyms(t,
		ci,
		testSym{name: "Caller", pkg: "p/main"},
		testSym{name: "Dup", pkg: "p/x", exported: true},
		testSym{name: "Dup", pkg: "p/y", exported: true},
	)

	ambiguous := insertUnboundEdge(t, ci, ids["p/main:Caller"], "Dup")
	missing := insertUnboundEdge(t, ci, ids["p/main:Caller"], "NotInThisCorpus")

	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}

	if tid, res := readEdge(t, ci, ambiguous); tid.Valid {
		t.Errorf("ambiguous edge got bound to %d; two candidates must not be silently narrowed to one", tid.Int64)
	} else if res != ResolutionAmbiguous {
		t.Errorf("resolution = %q, want %q: candidates exist, just not one", res, ResolutionAmbiguous)
	}

	if tid, res := readEdge(t, ci, missing); tid.Valid {
		t.Errorf("edge with no candidate got bound to %d", tid.Int64)
	} else if res != ResolutionUnresolved {
		t.Errorf("resolution = %q, want %q: no candidate at all is not the same as too many",
			res, ResolutionUnresolved)
	}
}

// TestResolveEdgeTargets_Idempotent is the property that lets one function serve
// the full pipeline, every file save, and the background pass. If a second run
// could move a row, running it after each save would make the graph a function
// of how many times files were touched.
//
// The vacuity guard matters more than usual here: an all-unresolved graph is
// trivially stable, so "nothing changed" would pass with the resolver gutted.
// The fixture is asserted to contain all three outcomes first.
func TestResolveEdgeTargets_Idempotent(t *testing.T) {
	ci, ctx := newResolverTestIndex(t)
	ids := insertSyms(t,
		ci,
		testSym{name: "Caller", pkg: "p/a"},
		testSym{name: "Save", parent: "Store", pkg: "p/b"},
		testSym{name: "Helper", pkg: "p/a", exported: true},
		testSym{name: "Helper", pkg: "p/b", exported: true},
		testSym{name: "Dup", pkg: "p/x", exported: true},
		testSym{name: "Dup", pkg: "p/y", exported: true},
	)
	caller := ids["p/a:Caller"]
	insertUnboundEdge(t, ci, caller, "Store.Save")      // exact
	insertUnboundEdge(t, ci, caller, "Helper")          // exact via package
	insertUnboundEdge(t, ci, caller, "Dup")             // ambiguous
	insertUnboundEdge(t, ci, caller, "NotInThisCorpus") // unresolved

	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := snapshotEdges(t, ci)

	var bound, ambiguous, unresolved int
	for _, s := range first {
		switch {
		case s.targetID.Valid:
			bound++
		case s.res == ResolutionAmbiguous:
			ambiguous++
		case s.res == ResolutionUnresolved:
			unresolved++
		}
	}
	if bound == 0 || ambiguous == 0 || unresolved == 0 {
		t.Fatalf("fixture is vacuous (bound=%d ambiguous=%d unresolved=%d): "+
			"a graph missing any outcome is stable for the wrong reason",
			bound, ambiguous, unresolved)
	}

	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := snapshotEdges(t, ci)

	if len(first) != len(second) {
		t.Fatalf("edge count changed %d -> %d across runs", len(first), len(second))
	}
	for id, a := range first {
		b := second[id]
		if a.targetID != b.targetID || a.res != b.res {
			t.Errorf("edge %d moved on the second run: (target=%v res=%s) -> (target=%v res=%s)",
				id, a.targetID, a.res, b.targetID, b.res)
		}
	}
}

// TestResolveEdgeTargets_AmbiguousIsRecomputedNotSticky covers the state no
// other mechanism revisits.
//
// The FK and the unbind trigger only touch rows whose target_id matches the
// deleted symbol, so an *unbound* edge's label survives every delete untouched.
// An edge marked `ambiguous` because two candidates existed therefore keeps
// claiming a candidate group after both candidates are gone — and the read side
// then offers the user a group query that returns nothing. The resolver has to
// recompute the label from the current corpus each run, not accrete it.
func TestResolveEdgeTargets_AmbiguousIsRecomputedNotSticky(t *testing.T) {
	ci, ctx := newResolverTestIndex(t)
	ids := insertSyms(t,
		ci,
		testSym{name: "Caller", pkg: "p/main"},
		testSym{name: "Dup", pkg: "p/x", exported: true},
		testSym{name: "Dup", pkg: "p/y", exported: true},
	)
	e := insertUnboundEdge(t, ci, ids["p/main:Caller"], "Dup")

	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if _, res := readEdge(t, ci, e); res != ResolutionAmbiguous {
		t.Fatalf("setup: resolution = %q, want %q", res, ResolutionAmbiguous)
	}

	// One candidate leaves: the name is now unique, the edge must bind.
	if _, err := ci.writerDB.ExecContext(ctx, `DELETE FROM symbols WHERE id = ?`, ids["p/y:Dup"]); err != nil {
		t.Fatal(err)
	}
	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if tid, res := readEdge(t, ci, e); !tid.Valid || tid.Int64 != ids["p/x:Dup"] {
		t.Errorf("after one candidate was deleted: target_id = %v, want %d; "+
			"an ambiguous edge must be reconsidered once the ambiguity is gone", tid, ids["p/x:Dup"])
	} else if res != ResolutionInferred {
		t.Errorf("resolution = %q, want %q", res, ResolutionInferred)
	}

	// The last candidate leaves: the trigger unbinds to `unresolved`, and the
	// resolver must leave it there rather than restoring `ambiguous`.
	if _, err := ci.writerDB.ExecContext(ctx, `DELETE FROM symbols WHERE id = ?`, ids["p/x:Dup"]); err != nil {
		t.Fatal(err)
	}
	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if tid, res := readEdge(t, ci, e); tid.Valid {
		t.Errorf("target_id = %v after every candidate was deleted", tid.Int64)
	} else if res != ResolutionUnresolved {
		t.Errorf("resolution = %q, want %q: no candidate remains, so there is no group to show",
			res, ResolutionUnresolved)
	}
}

// TestResolveEdgeTargets_AmbiguousClearedWithoutIntermediateBinding is the same
// invariant on the path nothing else covers.
//
// The test above happens to route through a bound state — one candidate leaves,
// the edge binds, then the trigger demotes it on the second delete. The trigger
// is what makes that case correct, and it only fires for rows whose target_id
// matches the deleted symbol. An edge that is `ambiguous` and *never bound* is
// invisible to it: delete every candidate at once and no mechanism revisits the
// label. The resolver must therefore recompute it from the current corpus, not
// treat it as already decided.
//
// Left stale, the read side offers a candidate-group query
// (queryCallersForAmbiguousTarget) for a group with no members — the graph
// claims to know of callees it cannot name.
func TestResolveEdgeTargets_AmbiguousClearedWithoutIntermediateBinding(t *testing.T) {
	ci, ctx := newResolverTestIndex(t)
	ids := insertSyms(t,
		ci,
		testSym{name: "Caller", pkg: "p/main"},
		testSym{name: "Dup", pkg: "p/x", exported: true},
		testSym{name: "Dup", pkg: "p/y", exported: true},
	)
	e := insertUnboundEdge(t, ci, ids["p/main:Caller"], "Dup")

	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if tid, res := readEdge(t, ci, e); tid.Valid || res != ResolutionAmbiguous {
		t.Fatalf("setup: want unbound %q, got target=%v res=%q", ResolutionAmbiguous, tid, res)
	}

	// Both candidates in one statement: the edge is never bound, so the unbind
	// trigger never sees it.
	if _, err := ci.writerDB.ExecContext(ctx, `DELETE FROM symbols WHERE name = 'Dup'`); err != nil {
		t.Fatal(err)
	}
	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if _, res := readEdge(t, ci, e); res != ResolutionUnresolved {
		t.Errorf("resolution = %q, want %q: every candidate is gone, so this is not an ambiguity "+
			"any more — the resolver is carrying a label forward instead of recomputing it", res, ResolutionUnresolved)
	}
}

type edgeState struct {
	targetID sql.NullInt64
	res      Resolution
}

func snapshotEdges(t *testing.T, ci *CodeIndex) map[int64]edgeState {
	t.Helper()
	rows, err := ci.writerDB.Query(`SELECT id, target_id, resolution FROM edges`)
	if err != nil {
		t.Fatalf("snapshot edges: %v", err)
	}
	defer rows.Close()
	out := map[int64]edgeState{}
	for rows.Next() {
		var id int64
		var st edgeState
		if err := rows.Scan(&id, &st.targetID, &st.res); err != nil {
			t.Fatal(err)
		}
		out[id] = st
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
