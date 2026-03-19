package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/database"
	"logline/internal/store"
)

func testConfig() config.Config {
	return config.Config{
		Port:           4000,
		Env:            "development",
		LogLevel:       "info",
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		DBMaxConns:     5,
		DBMaxIdle:      2,
		RateLimit:      10000,
		RateLimitBurst: 10000,
		Version:        "test",
		Commit:         "abc123",
		BuildTime:      "2026-01-01T00:00:00Z",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping DB test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connecting to database: %v", err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

func newTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	s := store.New(db)
	aks := auth.NewApiKeyStore(db)
	us := auth.NewUserStore(db)
	ss := auth.NewSessionStore(db)
	return New(testConfig(), testLogger(), s, aks, us, ss), db
}

// creates an API key for the given service and returns the raw key
func createTestKey(t *testing.T, db *sql.DB, service string) string {
	t.Helper()
	aks := auth.NewApiKeyStore(db)
	rawKey, err := aks.Create(context.Background(), "test-key", service)
	if err != nil {
		t.Fatal(err)
	}
	return rawKey
}

func TestHandleHealth_OK(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHealthEndpoint_AlwaysOK(t *testing.T) {
	srv, _ := newTestServer(t)

	// health should return 200 even during shutdown
	srv.SetShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 during shutdown, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for _, field := range []string{"status", "version", "commit", "build_time"} {
		if _, ok := body[field]; !ok {
			t.Errorf("expected %q field in health response", field)
		}
	}

	if body["version"] != "test" {
		t.Errorf("expected version %q, got %q", "test", body["version"])
	}
}

func TestReadyEndpoint_OK(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyEndpoint_ShuttingDown(t *testing.T) {
	srv, _ := newTestServer(t)

	srv.SetShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during shutdown, got %d", rec.Code)
	}
}

func TestIngestValidEntry(t *testing.T) {
	srv, db := newTestServer(t)

	// create an API key for the "auth" service
	aks := auth.NewApiKeyStore(db)
	rawKey, err := aks.Create(context.Background(), "test-key", "auth")
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"level":"error","message":"connection refused","service":"auth","timestamp":"2026-02-27T14:30:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestIngestWrongContentType(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "api")

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", rec.Code)
	}
}

func TestIngestEmptyBody(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "api")

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestInvalidJSON(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "api")

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("definitely not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestMissingRequiredFields(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "api")

	payload := `{"level":"error"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestInvalidLevel(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "api")

	payload := `{"level":"critical","message":"test","service":"api","timestamp":"2026-02-27T14:30:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestWrongMethodReturns405(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}
