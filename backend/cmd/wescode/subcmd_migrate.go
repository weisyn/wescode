package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/weisyn/wescode/internal/engine"
)

// runMigrate implements the one-shot `wescode migrate v0.2-to-v1.0`
// subcommand.
//
// v1.0 layout target under DataDir (~/.wescode/data or $WESCODE_DATA_DIR):
//
//	db/wescode_auth.db                        — auth-only DB (isolated from engine)
//	hypervisor.db                             — wesgine Hypervisor DB (cells / admin)
//	cells/<workspace-cellID>/cell.db          — per-workspace wesgine engine DB
//	cells/<workspace-cellID>/wescode-app.db   — per-workspace application DB (group tables)
//	cells/<workspace-cellID>/skills/          — per-workspace Tier C skills
//	cells/<workspace-cellID>/.plans/          — plan artefacts
//
// Legacy v0.2 layout expected:
//
//	db/wescode.db      — mixed auth + engine (wesgine v0.2)
//	index/code.db      — codeintel index (rebuilt per-workspace in v1.0)
//	skills/            — shared skill bundles (seeded per-workspace in v1.0)
//
// DEV-1 semantics: the migration is destructive and one-shot.  Auth
// session rows survive if the caller uses `--keep-auth` (default), which
// copies the ws_session table into the new location before archiving
// the legacy file.  Everything else (chat history / memory / run
// traces / code index) is archived-and-rebuild — legacy DB moves to
// `legacy-YYYYMMDD.db.bak` in the same directory.
//
// Workspace cells materialise lazily on the next Initialize; no
// per-workspace subdirectories are created here (wescode cannot enumerate
// which workspaces existed in v0.2 without walking the code index).
func runMigrate(extra []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("wescode migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "print planned changes without touching disk")
	force := fs.Bool("force", false, "re-run even if a hypervisor.db already exists (destructive)")
	keepAuth := fs.Bool("keep-auth", true, "copy ws_session rows into the new auth DB before archiving legacy")
	if err := fs.Parse(extra); err != nil {
		return err
	}
	direction := "v0.2-to-v1.0"
	if len(fs.Args()) > 0 {
		direction = fs.Args()[0]
	}
	if direction != "v0.2-to-v1.0" {
		return fmt.Errorf("unsupported migration direction %q (only v0.2-to-v1.0 supported)", direction)
	}

	dataDir := engine.DefaultDataDir()
	if v := os.Getenv("WESCODE_DATA_DIR"); v != "" {
		dataDir = v
	}
	if dataDir == "" {
		return fmt.Errorf("no data dir resolved; set WESCODE_DATA_DIR or fix engine.DefaultDataDir()")
	}

	plan, err := planMigration(dataDir, *force, *keepAuth)
	if err != nil {
		return err
	}

	logger.Info("migrate: plan", "data_dir", dataDir, "actions", len(plan.actions))
	for _, act := range plan.actions {
		logger.Info("migrate:   step", "op", act.op, "target", act.target, "note", act.note)
	}
	if *dryRun {
		logger.Info("migrate: dry-run complete; no filesystem changes made")
		return nil
	}
	if len(plan.actions) == 0 {
		logger.Info("migrate: nothing to do — data directory already v1.0-shaped")
		return nil
	}
	if err := runPlan(dataDir, plan, logger); err != nil {
		return fmt.Errorf("execute plan: %w", err)
	}
	logger.Info("migrate: complete", "data_dir", dataDir)
	logger.Info("migrate: next steps",
		"1", "start wescode normally — auth rows survived if --keep-auth was set",
		"2", "open each workspace once so the per-Cell cell.db / wescode-app.db / skills seed",
		"3", "legacy-*.db.bak retained for rollback; delete once the new layout is verified",
	)
	return nil
}

type migrateOp string

const (
	opRename   migrateOp = "rename"   // rename source → dataDir/legacy-<tag>.db.bak
	opMkdir    migrateOp = "mkdir"    // MkdirAll target
	opMovePath migrateOp = "movepath" // rename source → target
	opCopyAuth migrateOp = "copyauth" // copy ws_session table into new auth DB, then rename source
)

type migrateAction struct {
	op     migrateOp
	source string // absolute source path
	target string // absolute target path (for mkdir / movepath / copyauth)
	note   string
}

type migratePlan struct {
	actions []migrateAction
}

func planMigration(dataDir string, force, keepAuth bool) (*migratePlan, error) {
	plan := &migratePlan{}

	// 1. Legacy mixed auth+engine DB → archive or transplant.
	legacyDB := filepath.Join(dataDir, "db", "wescode.db")
	newAuthDB := filepath.Join(dataDir, "db", "wescode_auth.db")
	if _, err := os.Stat(legacyDB); err == nil {
		hypDB := filepath.Join(dataDir, "hypervisor.db")
		if _, herr := os.Stat(hypDB); herr == nil && !force {
			return nil, fmt.Errorf("refuse to migrate: %s already exists (use --force to override)", hypDB)
		}
		if keepAuth {
			plan.actions = append(plan.actions, migrateAction{
				op:     opCopyAuth,
				source: legacyDB,
				target: newAuthDB,
				note:   "copy ws_session rows into wescode_auth.db then archive legacy DB",
			})
		} else {
			plan.actions = append(plan.actions, migrateAction{
				op:     opRename,
				source: legacyDB,
				note:   "archive legacy wescode.db (auth rows dropped; users re-login)",
			})
		}
	}

	// 2. Archive legacy codeintel index (v1.0 lives at {ws}/.wescode/index/).
	legacyCodeDB := filepath.Join(dataDir, "index", "code.db")
	if _, err := os.Stat(legacyCodeDB); err == nil {
		plan.actions = append(plan.actions, migrateAction{
			op:     opRename,
			source: legacyCodeDB,
			note:   "archive legacy code.db (rebuilt per-workspace on next open)",
		})
	}

	// 3. Archive legacy shared skills tree (v1.0 skills are per-cell).
	//    Move to legacy-skills-<tag>/ rather than delete so operators can
	//    diff against the freshly-seeded per-workspace skill packages.
	legacySkills := filepath.Join(dataDir, "skills")
	if statSrc, err := os.Stat(legacySkills); err == nil && statSrc.IsDir() {
		archiveTag := time.Now().UTC().Format("20060102")
		plan.actions = append(plan.actions, migrateAction{
			op:     opMovePath,
			source: legacySkills,
			target: filepath.Join(dataDir, fmt.Sprintf("legacy-skills-%s", archiveTag)),
			note:   "archive legacy shared skills/ (per-workspace re-seed on open)",
		})
	}

	// 4. Ensure cells/ root exists for lazy per-workspace materialisation.
	cellsRoot := filepath.Join(dataDir, "cells")
	if _, err := os.Stat(cellsRoot); os.IsNotExist(err) {
		plan.actions = append(plan.actions, migrateAction{
			op:     opMkdir,
			target: cellsRoot,
			note:   "per-workspace cell root; each workspace materialises on Initialize",
		})
	}

	return plan, nil
}

func runPlan(_ string, plan *migratePlan, logger *slog.Logger) error {
	backupTag := time.Now().UTC().Format("20060102")
	for _, act := range plan.actions {
		switch act.op {
		case opRename:
			bak := filepath.Join(filepath.Dir(act.source), fmt.Sprintf("legacy-%s.db.bak", backupTag))
			// Disambiguate multiple legacy DBs archived in the same run.
			if _, err := os.Stat(bak); err == nil {
				bak = filepath.Join(filepath.Dir(act.source),
					fmt.Sprintf("legacy-%s-%s.db.bak", backupTag, filepath.Base(act.source)))
			}
			if err := os.Rename(act.source, bak); err != nil {
				return fmt.Errorf("rename %s → %s: %w", act.source, bak, err)
			}
			logger.Info("migrate: renamed", "from", act.source, "to", bak)
		case opMkdir:
			if err := os.MkdirAll(act.target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", act.target, err)
			}
			logger.Info("migrate: mkdir", "path", act.target)
		case opMovePath:
			if err := os.MkdirAll(filepath.Dir(act.target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", act.target, err)
			}
			if err := os.Rename(act.source, act.target); err != nil {
				return fmt.Errorf("move %s → %s: %w", act.source, act.target, err)
			}
			logger.Info("migrate: moved", "from", act.source, "to", act.target)
		case opCopyAuth:
			if err := copyAuthTable(act.source, act.target); err != nil {
				return fmt.Errorf("copy auth table %s → %s: %w", act.source, act.target, err)
			}
			logger.Info("migrate: copied ws_session rows", "from", act.source, "to", act.target)
			// Then archive the source (same as opRename).
			bak := filepath.Join(filepath.Dir(act.source), fmt.Sprintf("legacy-%s.db.bak", backupTag))
			if err := os.Rename(act.source, bak); err != nil {
				return fmt.Errorf("archive %s → %s: %w", act.source, bak, err)
			}
			logger.Info("migrate: renamed", "from", act.source, "to", bak)
		default:
			return fmt.Errorf("unknown plan op %q", act.op)
		}
	}
	return nil
}
