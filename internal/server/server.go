package server

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"time"

	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/middleware"
	"logline/internal/store"
)

type contextKey string

const userContextKey contextKey = "user"

type Server struct {
	cfg              config.Config
	logger           *slog.Logger
	store            *store.Store
	apiKeyStore      *auth.ApiKeyStore
	userStore        *auth.UserStore
	sessionStore     *auth.SessionStore
	rateLimiterStore *middleware.RateLimiterStore
	templates        map[string]*template.Template
	mux              *http.ServeMux
	handler          http.Handler
}

func New(cfg config.Config, logger *slog.Logger, s *store.Store, aks *auth.ApiKeyStore, us *auth.UserStore, ss *auth.SessionStore) *Server {
	srv := &Server{
		cfg:              cfg,
		logger:           logger,
		store:            s,
		apiKeyStore:      aks,
		userStore:        us,
		sessionStore:     ss,
		rateLimiterStore: middleware.NewRateLimiterStore(cfg.RateLimit, cfg.RateLimitBurst, 5*time.Minute),
		templates:        newTemplateCache("templates"),
		mux:              http.NewServeMux(),
	}

	srv.registerRoutes()

	srv.handler = middleware.Chain(
		srv.mux,
		middleware.Recovery(logger),
		middleware.RequestID,
		middleware.Logging(logger),
		middleware.CORS(cfg.CORSOrigins),
	)

	go srv.startSessionCleanup(context.Background())

	return srv
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// public
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /register", s.handleRegister)
	s.mux.HandleFunc("POST /login", s.handleLogin)
	s.mux.HandleFunc("POST /logout", s.handleLogout)

	// api key protected, rate limited
	authMiddleware := auth.Middleware(s.apiKeyStore)
	rateLimitMiddleware := middleware.RateLimit(s.rateLimiterStore)
	s.mux.Handle("POST /ingest", authMiddleware(rateLimitMiddleware(http.HandlerFunc(s.handleIngest))))
	s.mux.HandleFunc("POST /api/keys", s.handleCreateAPIKey)

	// session protected
	s.mux.Handle("GET /logs", s.requireSession(http.HandlerFunc(s.handleListLogs)))

	// dashboard (session protected, HTML)
	s.mux.HandleFunc("GET /dashboard/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /dashboard/login", s.handleLoginSubmit)
	s.mux.Handle("GET /dashboard", s.requireSession(http.HandlerFunc(s.handleDashboardOverview)))
	s.mux.Handle("GET /dashboard/logs", s.requireSession(http.HandlerFunc(s.handleDashboardLogs)))
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_token")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "authentication required",
			})
			return
		}

		_, user, err := s.sessionStore.Validate(r.Context(), cookie.Value)
		if err != nil {
			// clear the invalid cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "session_token",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   s.cfg.Env != "development",
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})

			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "authentication required",
			})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromContext(ctx context.Context) *auth.User {
	user, _ := ctx.Value(userContextKey).(*auth.User)
	return user
}

func (s *Server) startSessionCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			count, err := s.sessionStore.DeleteExpired(ctx)
			if err != nil {
				s.logger.Error("session cleanup failed",
					slog.String("error", err.Error()),
				)
				continue
			}
			if count > 0 {
				s.logger.Info("expired sessions cleaned up",
					slog.Int64("count", count),
				)
			}
		case <-ctx.Done():
			return
		}
	}
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
