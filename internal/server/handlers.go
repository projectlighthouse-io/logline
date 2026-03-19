package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"logline/internal/auth"
	"logline/internal/domain"
	"logline/internal/middleware"
	"logline/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// liveness probe — always returns 200
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "ok",
		"version":    s.cfg.Version,
		"commit":     s.cfg.Commit,
		"build_time": s.cfg.BuildTime,
	})
}

// readiness probe — checks shutdown flag and database connectivity
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.shuttingDown.Load() {
		writeJSON(w, http.StatusServiceUnavailable, domain.ErrorResponse{
			Error: "server is shutting down",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("readiness check failed: database unreachable",
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

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "invalid request body",
		})
		return
	}

	if req.Email == "" || req.Password == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "email, password, and name are required",
		})
		return
	}

	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "password must be at least 8 characters",
		})
		return
	}

	user, err := s.userStore.Create(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		if auth.IsUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, domain.ErrorResponse{
				Error: "email already registered",
			})
			return
		}

		s.logger.Error("failed to create user",
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "internal error",
		})
		return
	}

	s.logger.Info("user registered",
		slog.Int64("user_id", user.ID),
		slog.String("email", user.Email),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"message": "account created",
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "invalid request body",
		})
		return
	}

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{
			Error: "email and password are required",
		})
		return
	}

	user, err := s.userStore.GetByEmail(r.Context(), req.Email)
	if err != nil {
		// run bcrypt anyway so the response time is the same whether the email exists or not.
		// without this, an attacker can distinguish "email not found" (~1ms) from
		// "wrong password" (~250ms) by timing the response.
		bcrypt.CompareHashAndPassword([]byte("$2a$12$dummy"), []byte(req.Password))

		writeJSON(w, http.StatusUnauthorized, domain.ErrorResponse{
			Error: "invalid email or password",
		})
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		writeJSON(w, http.StatusUnauthorized, domain.ErrorResponse{
			Error: "invalid email or password",
		})
		return
	}

	token, err := s.sessionStore.Create(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("failed to create session",
			slog.String("error", err.Error()),
			slog.Int64("user_id", user.ID),
		)
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "internal error",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Env != "development",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logged in",
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "logged out",
		})
		return
	}

	if err := s.sessionStore.Delete(r.Context(), cookie.Value); err != nil {
		s.logger.Error("failed to delete session",
			slog.String("error", err.Error()),
		)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Env != "development",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logged out",
	})
}

// todo: implement log listing with query params
func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "ok",
	})
}

type logViewerData struct {
	Logs       []store.LogRow
	Filter     store.LogFilter
	Services   []string
	NextCursor int64
	HasMore    bool
	Error      string
}

type dashboardData struct {
	Logs       []store.LogRow
	NextCursor int64
	Error      string
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "login.html", dashboardData{})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login.html", dashboardData{
			Error: "invalid form data",
		})
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	if email == "" || password == "" {
		s.render(w, http.StatusBadRequest, "login.html", dashboardData{
			Error: "email and password are required",
		})
		return
	}

	user, err := s.userStore.GetByEmail(r.Context(), email)
	if err != nil {
		// timing attack defense: run bcrypt even when user not found
		bcrypt.CompareHashAndPassword([]byte("$2a$12$dummy"), []byte(password))

		s.render(w, http.StatusUnauthorized, "login.html", dashboardData{
			Error: "invalid email or password",
		})
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, password); err != nil {
		s.render(w, http.StatusUnauthorized, "login.html", dashboardData{
			Error: "invalid email or password",
		})
		return
	}

	token, err := s.sessionStore.Create(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("failed to create session",
			slog.String("error", err.Error()),
			slog.Int64("user_id", user.ID),
		)
		s.render(w, http.StatusInternalServerError, "login.html", dashboardData{
			Error: "internal error",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Env != "development",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	http.Redirect(w, r, "/dashboard/logs", http.StatusSeeOther)
}

func (s *Server) handleDashboardLogs(w http.ResponseWriter, r *http.Request) {
	f := store.LogFilter{
		Limit: 50,
	}

	q := r.URL.Query()

	if v := q.Get("level"); v != "" {
		f.Level = v
	}
	if v := q.Get("service"); v != "" {
		f.Service = v
	}
	if v := q.Get("q"); v != "" {
		f.Query = v
	}
	if v := q.Get("from"); v != "" {
		if t, err := parseTime(v); err == nil {
			f.From = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := parseTime(v); err == nil {
			f.To = t
		}
	}
	if v := q.Get("cursor"); v != "" {
		if c, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.Cursor = c
		}
	}
	if v := q.Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 200 {
			f.Limit = l
		}
	}

	logs, err := s.store.QueryLogs(r.Context(), f)
	if err != nil {
		s.logger.Error("failed to query logs", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	services, err := s.store.ListServices(r.Context())
	if err != nil {
		s.logger.Error("failed to list services", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var nextCursor int64
	hasMore := len(logs) == f.Limit
	if hasMore {
		nextCursor = logs[len(logs)-1].ID
	}

	s.render(w, http.StatusOK, "logs.html", logViewerData{
		Logs:       logs,
		Filter:     f,
		Services:   services,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

type dashboardOverviewData struct {
	ErrorsPerHour   []store.TimeBucket
	VolumeByService []store.ServiceCount
	TopErrors       []store.MessageCount
	ErrorsJSON      template.JS
	VolumeJSON      template.JS
}

func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	errors, err := s.store.ErrorsPerHour(ctx)
	if err != nil {
		s.logger.Error("failed to query errors per hour", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	volume, err := s.store.VolumeByService(ctx)
	if err != nil {
		s.logger.Error("failed to query volume by service", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	topErrors, err := s.store.TopErrors(ctx)
	if err != nil {
		s.logger.Error("failed to query top errors", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	filled := store.FillHourGaps(errors)

	errorsJSON, err := json.Marshal(filled)
	if err != nil {
		s.logger.Error("failed to marshal errors json", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	volumeJSON, err := json.Marshal(volume)
	if err != nil {
		s.logger.Error("failed to marshal volume json", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// default to empty array for chart.js
	if volume == nil {
		volumeJSON = []byte("[]")
	}

	s.render(w, http.StatusOK, "dashboard.html", dashboardOverviewData{
		ErrorsPerHour:   filled,
		VolumeByService: volume,
		TopErrors:       topErrors,
		ErrorsJSON:      template.JS(errorsJSON),
		VolumeJSON:      template.JS(volumeJSON),
	})
}

// parseTime tries RFC3339 first, then date-only format
func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

const maxBodySize = 1_048_576 // 1 MB

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
