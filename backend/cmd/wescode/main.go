package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	weisyn "github.com/weisyn/weisyn"
	"github.com/weisyn/weisyn/sdk/auth"
	"github.com/weisyn/wesapp/provider"
	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/rpc"
	"github.com/weisyn/wescode/internal/store"
	wesgine "github.com/weisyn/wesgine"

	// IM channel adapters register their factory from init(), so a platform is
	// only connectable if its package is linked here. The set must stay equal to
	// the schemas declared in internal/engine/channels.go: a schema without a
	// blank import renders a working config form whose "save and connect" fails
	// with "no factory registered", which reads as a broken app rather than an
	// unsupported platform.
	_ "github.com/weisyn/wesgine/adapter/channel/dingtalk"
	_ "github.com/weisyn/wesgine/adapter/channel/feishu"
	_ "github.com/weisyn/wesgine/adapter/channel/qqbot"
	_ "github.com/weisyn/wesgine/adapter/channel/wecom"
	_ "github.com/weisyn/wesgine/adapter/channel/wecom_callback"
	_ "github.com/weisyn/wesgine/adapter/channel/weixin"

	_ "modernc.org/sqlite"
)

// logReplaceAttr normalizes every record at the handler boundary (zero
// changes at call sites, applies to wesgine engine logs too):
//   - time: always UTC so Go logs, the chat event log (ISO UTC) and the
//     VS Code session directory share one clock (2026-08-17 log rebuild).
//   - msg: legacy "[module] text" prefixes become a structured `module`
//     field with a clean `msg`, so logs are jq-filterable without parsing.
func logReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.Time(slog.TimeKey, t.UTC())
		}
	case slog.MessageKey:
		if msg, ok := a.Value.Any().(string); ok && len(msg) > 2 && msg[0] == '[' {
			if idx := strings.IndexByte(msg, ']'); idx > 1 {
				module := msg[1:idx]
				rest := strings.TrimSpace(msg[idx+1:])
				// Empty group key inlines: {module, msg} flat in JSON.
				return slog.Group("", slog.String("module", module), slog.String("msg", rest))
			}
		}
	}
	return a
}

func initLogger() {
	dataDir := engine.DefaultDataDir()
	if v := os.Getenv("WESCODE_DATA_DIR"); v != "" {
		dataDir = v
	}
	logDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "wescode.log")

	rw, err := newRotatingWriter(logPath)
	if err != nil {
		slog.SetDefault(wesgine.NewStderrWarnLogger())
		return
	}

	// File: all levels, rotated at 10MB × 5 backups (async, never blocks).
	fileHandler := slog.NewJSONHandler(rw, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: logReplaceAttr})

	// Stderr: only Warn+ (minimal output — pipe buffer never fills).
	stderrHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn, ReplaceAttr: logReplaceAttr})

	// Combine with wesgine AsyncHandler: caller never blocks on I/O.
	combined := wesgine.NewMultiHandler(fileHandler, stderrHandler)
	logger := slog.New(wesgine.NewAsyncHandler(combined))
	slog.SetDefault(logger)
}

func main() {
	initLogger()

	// Boot self-check: one authoritative structured record of where engine
	// data lives (data_dir + data_dir_source). Diagnostic scripts and the
	// About page must read THIS field — never re-derive the path in TS.
	dataDir := engine.DefaultDataDir()
	dataDirSource := "xdg"
	if v := strings.TrimSpace(os.Getenv("WESCODE_DATA_DIR")); v != "" {
		dataDir = v
		dataDirSource = "env"
	}
	slog.Info("[boot] engine data dir", "data_dir", dataDir, "data_dir_source", dataDirSource)

	// One-shot Windows legacy-layout migration (A-8): runs before any
	// subcommand so CLI / packaged / make run share the same entry point,
	// and before initAuth/StartEngine so no SQLite lock exists on either
	// data tree. Safe no-op everywhere else (no leftover hypervisor.db).
	runLegacyLayoutMigration()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Subcommand dispatch.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--index-worker":
			if err := codeintel.RunWorker(); err != nil {
				log.Fatalf("index-worker: %v", err)
			}
			return
		case "migrate":
			if err := runMigrate(os.Args[2:], slog.Default()); err != nil {
				log.Fatalf("migrate: %v", err)
			}
			return
		case "run":
			if err := runHeadless(ctx, os.Args[2:]); err != nil {
				log.Fatalf("run: %v", err)
			}
			return
		case "bench":
			if err := runBench(ctx, os.Args[2:]); err != nil {
				log.Fatalf("bench: %v", err)
			}
			return
		}
	}

	// Default: RPC mode (VSCode drives via stdio JSON-RPC).
	go func() { _ = http.ListenAndServe(":6060", nil) }()

	provider.SetCatalog(weisyn.ProviderCatalog())

	cfg, err := engine.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	authDB, authSvc, err := initAuthService(ctx)
	if err != nil {
		log.Fatalf("init auth: %v", err)
	}
	defer authDB.Close()

	weisynURL := os.Getenv("WESCODE_WEISYN_URL")
	if weisynURL == "" {
		weisynURL = "https://www.weisyn.com"
	}
	engineSvc := engine.NewService(cfg)
	engineSvc.SetTokenSource(authSvc, weisynURL)

	authSvc.OnSessionCleared(func(ctx context.Context) {
		engineSvc.BootstrapWesProvider(ctx)
	})

	server := rpc.NewServer(os.Stdin, os.Stdout)
	handler := rpc.NewHandler(engineSvc, authSvc, server, cancel, rpc.WithAppContext(ctx))
	handler.Register(server)

	if err := server.Run(ctx); err != nil {
		log.Fatalf("run rpc server: %v", err)
	}
	wesgine.FlushLogger(slog.Default())
}

func initAuthService(ctx context.Context) (*sql.DB, *auth.Service, error) {
	dataDir := engine.DefaultDataDir()
	if v := os.Getenv("WESCODE_DATA_DIR"); v != "" {
		dataDir = v
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "db"), 0o755); err != nil {
		return nil, nil, fmt.Errorf("mkdir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "db", "wescode_auth.db")
	// filepath.ToSlash keeps SQLite DSN parsing unambiguous on Windows
	// (drive letters and backslashes are URI-hostile).
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open auth db: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)

	// Run PRAGMA user_version migration chain before auth.Service.Migrate().
	// Failure is non-fatal for auth: archive and recreate (login session lost,
	// user re-authenticates — acceptable trade-off vs crash on boot).
	if err := store.RunMigrations(ctx, db, "wescode_auth.db", store.AuthDBMigrations()); err != nil {
		slog.Warn("[auth] wescode_auth.db migration failed, archiving and recreating",
			"error", err, "path", dbPath)
		db.Close()
		archivePath := dbPath + ".migration-failed"
		_ = os.Rename(dbPath, archivePath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("reopen auth db after archive: %w", err)
		}
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
		if err = store.RunMigrations(ctx, db, "wescode_auth.db", store.AuthDBMigrations()); err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("auth migration on fresh db: %w", err)
		}
	}

	agentHubURL := os.Getenv("WESCODE_WEISYN_URL")
	if agentHubURL == "" {
		agentHubURL = "https://www.weisyn.com"
	}
	svc := auth.New(auth.Config{
		DB:          db,
		AgentHubURL: agentHubURL,
		Source:      "wescode",
		TableName:   "ws_session",
	})
	if err := svc.Migrate(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("auth migrate: %w", err)
	}
	return db, svc, nil
}
