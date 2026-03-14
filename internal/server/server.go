package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"

	"logline/internal/config"
	"logline/internal/store"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	store  *store.Store
	mux    *http.ServeMux
}

func New(cfg config.Config, logger *slog.Logger, s *store.Store) *Server {
	srv := &Server{
		cfg:    cfg,
		logger: logger,
		store:  s,
		mux:    http.NewServeMux(),
	}

	srv.registerRoutes()

	return srv
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /ingest", s.handleIngest)
}

func NewLogger(env, level string) *slog.Logger {
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
