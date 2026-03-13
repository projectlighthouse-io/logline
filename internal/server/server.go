package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"

	"logline/internal/config"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	mux    *http.ServeMux
}

func New(cfg config.Config) *Server {
	logger := newLogger(cfg.Env, cfg.LogLevel)

	s := &Server{
		cfg:    cfg,
		logger: logger,
		mux:    http.NewServeMux(),
	}

	s.registerRoutes()

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /ingest", s.handleIngest)
}

func newLogger(env, level string) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: parseLogLevel(level),
	}

	switch env {
	case "production":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func generateTraceID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
