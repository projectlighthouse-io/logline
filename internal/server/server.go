package server

import (
	"net/http"

	"logline/internal/config"
)

type Server struct {
	cfg config.Config
	mux *http.ServeMux
}

func New(cfg config.Config) *Server {
	s := &Server{
		cfg: cfg,
		mux: http.NewServeMux(),
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
