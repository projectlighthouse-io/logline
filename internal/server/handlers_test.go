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
	"logline/internal/domain"
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
	logStore := store.New(db)
	apiKeyStore := auth.NewApiKeyStore(db)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	return New(testConfig(), testLogger(), logStore, apiKeyStore, userStore, sessionStore), db
}

// creates an API key for the given service and returns the raw key
func createTestKey(t *testing.T, db *sql.DB, service string) string {
	t.Helper()
	apiKeyStore := auth.NewApiKeyStore(db)
	rawKey, err := apiKeyStore.Create(context.Background(), "test-key", service)
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

	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
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

func newIngestRequest(t *testing.T, apiKey, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return req
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("status = %d, want %d", rec.Code, want)
	}
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var decoded T
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	return decoded
}

func TestIngestValidEntry(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "auth")

	body := `{
		"level": "error",
		"message": "connection refused",
		"service": "auth",
		"timestamp": "2026-02-27T14:30:00Z"
	}`

	req := newIngestRequest(t, key, body)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)
	resp := decodeJSON[domain.StatusResponse](t, rec)

	if resp.Status != "accepted" {
		t.Errorf("status = %q, want %q", resp.Status, "accepted")
	}
}

func TestIngest_Errors(t *testing.T) {
	srv, db := newTestServer(t)
	key := createTestKey(t, db, "web")

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantErr     string
	}{
		{
			name:        "wrong content-type",
			contentType: "text/plain",
			body:        `{"level":"info","message":"hi","service":"web","timestamp":"2026-02-27T14:30:00Z"}`,
			wantStatus:  http.StatusUnsupportedMediaType,
			wantErr:     "content-type must be application/json",
		},
		{
			name:        "empty body",
			contentType: "application/json",
			body:        "",
			wantStatus:  http.StatusBadRequest,
			wantErr:     "invalid JSON",
		},
		{
			name:        "invalid json",
			contentType: "application/json",
			body:        `{not json}`,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "invalid JSON",
		},
		{
			name:        "missing level",
			contentType: "application/json",
			body:        `{"message":"hi","service":"web","timestamp":"2026-02-27T14:30:00Z"}`,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "level is required",
		},
		{
			name:        "missing message",
			contentType: "application/json",
			body:        `{"level":"info","service":"web","timestamp":"2026-02-27T14:30:00Z"}`,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "message is required",
		},
		{
			name:        "missing service",
			contentType: "application/json",
			body:        `{"level":"info","message":"hi","timestamp":"2026-02-27T14:30:00Z"}`,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "service is required",
		},
		{
			name:        "invalid level value",
			contentType: "application/json",
			body:        `{"level":"critical","message":"hi","service":"web","timestamp":"2026-02-27T14:30:00Z"}`,
			wantStatus:  http.StatusBadRequest,
			wantErr:     "level must be one of",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(testCase.body))
			req.Header.Set("Content-Type", testCase.contentType)
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != testCase.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, testCase.wantStatus)
			}

			var errResp domain.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
				t.Fatalf("decoding error response: %v", err)
			}

			if !strings.Contains(errResp.Error, testCase.wantErr) {
				t.Errorf("error = %q, want substring %q", errResp.Error, testCase.wantErr)
			}
		})
	}
}

func TestHandleIngest_ServiceScopeFromContext(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{
		"level": "info",
		"message": "test",
		"service": "payments",
		"timestamp": "2026-02-27T14:30:00Z"
	}`

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	key := &auth.ApiKey{Name: "billing-key", Service: "billing"}
	ctx := context.WithValue(req.Context(), auth.ApiKeyContextKey, key)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	srv.handleIngest(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
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
