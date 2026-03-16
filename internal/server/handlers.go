package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"logline/internal/auth"
	"logline/internal/domain"
	"logline/internal/middleware"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check failed: database unreachable",
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusServiceUnavailable, domain.ErrorResponse{
			Error: "database unreachable",
		})
		return
	}

	writeJSON(w, http.StatusOK, domain.StatusResponse{Status: "ok"})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())
	logger := s.logger.With(slog.String("request_id", requestID))

	if r.Header.Get("Content-Type") != "application/json" {
		logger.Warn("unsupported content type",
			slog.String("content_type", r.Header.Get("Content-Type")),
		)
		writeJSON(w, http.StatusUnsupportedMediaType, domain.ErrorResponse{
			Error: "content-type must be application/json",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var entry domain.LogEntry
	if err := decoder.Decode(&entry); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			logger.Warn("request body too large",
				slog.String("error", err.Error()),
			)
			writeJSON(w, http.StatusRequestEntityTooLarge, domain.ErrorResponse{
				Error: "request body too large",
			})
			return
		}

		logger.Warn("invalid JSON in request",
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "invalid JSON: " + err.Error(),
		})
		return
	}

	if msg := domain.ValidateLogEntry(entry); msg != "" {
		logger.Warn("validation failed",
			slog.String("reason", msg),
		)
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: msg,
		})
		return
	}

	// service scoping: key can only ingest for its own service
	apiKey := auth.KeyFromContext(r.Context())
	if apiKey != nil && apiKey.Service != entry.Service {
		logger.Warn("service mismatch",
			slog.String("key_service", apiKey.Service),
			slog.String("entry_service", entry.Service),
		)
		writeJSON(w, http.StatusForbidden, domain.ErrorResponse{
			Error: fmt.Sprintf("api key is scoped to service %q, not %q", apiKey.Service, entry.Service),
		})
		return
	}

	if err := s.store.InsertLog(r.Context(), entry); err != nil {
		logger.Error("failed to store log entry",
			slog.String("error", err.Error()),
			slog.String("service", entry.Service),
		)
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "failed to store log entry",
		})
		return
	}

	logger.Info("log entry stored",
		slog.String("service", entry.Service),
		slog.String("entry_level", entry.Level),
	)

	writeJSON(w, http.StatusCreated, domain.StatusResponse{Status: "accepted"})
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Service string `json:"service"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{Error: "invalid JSON"})
		return
	}

	if req.Name == "" || req.Service == "" {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{Error: "name and service are required"})
		return
	}

	rawKey, err := s.apiKeyStore.Create(r.Context(), req.Name, req.Service)
	if err != nil {
		s.logger.Error("failed to create api key", slog.String("error", err.Error()))
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{Error: "failed to create api key"})
		return
	}

	s.logger.Info("api key created",
		slog.String("name", req.Name),
		slog.String("service", req.Service),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"key":     rawKey,
		"message": "Store this key securely. It will not be shown again.",
	})
}

const maxBodySize = 1_048_576 // 1 MB

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
