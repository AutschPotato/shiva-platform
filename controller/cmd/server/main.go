package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/shiva-load-testing/controller/internal/completion"
	"github.com/shiva-load-testing/controller/internal/config"
	"github.com/shiva-load-testing/controller/internal/handler"
	"github.com/shiva-load-testing/controller/internal/orchestrator"
	"github.com/shiva-load-testing/controller/internal/scheduler"
	"github.com/shiva-load-testing/controller/internal/scriptgen"
	"github.com/shiva-load-testing/controller/internal/server"
	"github.com/shiva-load-testing/controller/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		logger.Warn("config warning", "error", err)
	}

	// Database
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Wait for DB
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer waitCancel()
	logger.Info("waiting for database...")
	if err := store.WaitForDB(waitCtx, db); err != nil {
		return fmt.Errorf("db not ready: %w", err)
	}
	logger.Info("database connected")

	// Store & migrations
	st := store.New(db)
	if err := st.Migrate(context.Background()); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	logger.Info("database migrated")

	// Clean up orphaned tests from previous crash
	if affected, err := st.MarkStaleRunningTests(context.Background()); err != nil {
		logger.Warn("failed to clean up stale tests", "error", err)
	} else if affected > 0 {
		logger.Info("cleaned up stale running tests from previous session", "count", affected)
	}

	// Ensure initial admin
	hashedPw, err := handler.HashPassword(cfg.InitialAdminPassword)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	if err := st.EnsureAdmin(context.Background(), cfg.InitialAdminUsername, cfg.InitialAdminEmail, hashedPw); err != nil {
		return fmt.Errorf("ensure admin: %w", err)
	}

	// Ensure default script exists
	if err := scriptgen.EnsureDefault(cfg.ScriptsDir); err != nil {
		logger.Warn("could not ensure default script", "error", err)
	}
	scriptgen.SetCompletionBufferSeconds(cfg.K6CompletionBufferSec)

	// Orchestrator
	pollInterval := time.Duration(cfg.MetricsPollIntervalMS) * time.Millisecond
	maxTestDuration := time.Duration(cfg.MaxTestDurationMin) * time.Minute
	dashboardRuntime := orchestrator.DashboardRuntimeConfig{
		Enabled: cfg.K6DashboardEnabled,
		Host:    cfg.K6DashboardHost,
		Port:    cfg.K6DashboardPort,
	}
	orch := orchestrator.New(cfg.Workers, pollInterval, maxTestDuration, logger, dashboardRuntime)
	if cfg.K6WorkerReadyTimeoutSec > 0 {
		orch.SetWorkerReadyTimeout(time.Duration(cfg.K6WorkerReadyTimeoutSec) * time.Second)
	}
	logger.Info("worker discovery",
		"mode", string(cfg.WorkerDiscovery),
		"count", len(cfg.Workers),
		"addresses", cfg.Workers,
	)
	logger.Info("k6 dashboard runtime",
		"enabled", cfg.K6DashboardEnabled,
		"host", cfg.K6DashboardHost,
		"port", cfg.K6DashboardPort,
	)

	// Ensure output directory exists and is writable by k6 workers (non-root user)
	if err := os.MkdirAll(cfg.OutputDir, 0777); err != nil {
		logger.Warn("could not create output dir", "error", err)
	}
	if err := os.Chmod(cfg.OutputDir, 0777); err != nil {
		logger.Warn("could not chmod output dir", "error", err)
	}

	// Test handler (needed by both router and scheduler)
	completionRegistry := completion.NewRegistry()
	testH := handler.NewTestHandler(st, orch, logger, cfg.ScriptsDir, cfg.OutputDir, completionRegistry, cfg.InternalControllerURL)

	// Scheduler
	sched := scheduler.New(st, testH, orch, logger, cfg.EncryptionKey)
	testH.SetScheduleCompletionNotifier(sched)
	sched.Start(context.Background())

	router := server.NewRouter(server.Deps{
		Store:                 st,
		Orchestrator:          orch,
		Scheduler:             sched,
		TestHandler:           testH,
		Logger:                logger,
		JWTSecret:             cfg.JWTSecret,
		APIKey:                cfg.APIKey,
		CORSOrigins:           cfg.CORSOrigins,
		ScriptsDir:            cfg.ScriptsDir,
		OutputDir:             cfg.OutputDir,
		PublicAppURL:          cfg.PublicAppURL,
		PasswordResetTokenTTL: time.Duration(cfg.PasswordResetTokenTTLMin) * time.Minute,
		SMTPHost:              cfg.SMTPHost,
		SMTPPort:              cfg.SMTPPort,
		SMTPUser:              cfg.SMTPUser,
		SMTPPassword:          cfg.SMTPPassword,
		SMTPFromEmail:         cfg.SMTPFromEmail,
		SMTPFromName:          cfg.SMTPFromName,
		EncryptionKey:         cfg.EncryptionKey,
	})

	// HTTP Server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 20 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "port", cfg.Port)
		errCh <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("shutdown signal received", "signal", sig)
	case err := <-errCh:
		if err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	sched.Stop()
	orch.StopPolling()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	logger.Info("server stopped")
	return nil
}
