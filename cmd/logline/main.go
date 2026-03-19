package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/database"
	"logline/internal/server"
	"logline/internal/store"
	"logline/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	logger := server.NewLogger(cfg.Env, cfg.LogLevel)

	db, err := database.Open(cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMaxIdle)
	if err != nil {
		return err
	}
	defer db.Close()

	logger.Info("database connected",
		slog.Int("max_open_conns", cfg.DBMaxConns),
		slog.Int("max_idle_conns", cfg.DBMaxIdle),
	)

	if err := database.Migrate(context.Background(), db); err != nil {
		return err
	}
	logger.Info("migrations applied")

	s := store.New(db)
	aks := auth.NewApiKeyStore(db)
	us := auth.NewUserStore(db)
	ss := auth.NewSessionStore(db)
	srv := server.New(cfg, logger, s, aks, us, ss)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	rw := worker.NewRetentionWorker(db, cfg.RetentionDays, 1*time.Hour, logger)
	go rw.Run(workerCtx)

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	// channel to capture ListenAndServe errors
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- httpServer.ListenAndServe()
	}()

	logger.Info("logline starting",
		slog.String("addr", addr),
		slog.String("env", cfg.Env),
	)

	// wait for shutdown signal or server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http server error: %w", err)
		}
	}

	// shutdown sequence
	srv.SetShuttingDown()
	workerCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer shutdownCancel()

	logger.Info("shutting down http server")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", slog.String("error", err.Error()))
	}

	logger.Info("shutdown complete")
	return nil
}
