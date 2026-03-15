package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/config"
	"logline/internal/database"
	"logline/internal/server"
	"logline/internal/store"
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
	srv := server.New(cfg, logger, s)

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Info("logline starting",
		slog.String("addr", addr),
		slog.String("env", cfg.Env),
	)

	return http.ListenAndServe(addr, srv)
}
