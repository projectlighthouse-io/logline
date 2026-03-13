package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"logline/internal/config"
	"logline/internal/server"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	srv := server.New(cfg)

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("logline starting",
		slog.String("addr", addr),
		slog.String("env", cfg.Env),
	)

	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("server stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
