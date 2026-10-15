//go:build integration

package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/config"
	"logline/internal/database"
	"logline/internal/domain"
	"logline/internal/server"
	"logline/internal/store"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set, skipping integration tests")
		return 0
	}

	var err error
	testDB, err = sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening database: %v\n", err)
		return 1
	}
	defer testDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := testDB.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connecting to database: %v\n", err)
		return 1
	}

	if err := database.Migrate(ctx, testDB); err != nil {
		fmt.Fprintf(os.Stderr, "running migrations: %v\n", err)
		return 1
	}

	return m.Run()
}

// newTestTx starts a transaction and registers a cleanup that rolls it back.
// each test gets an isolated view of the database without cross-test pollution.
func newTestTx(t *testing.T) *sql.Tx {
	t.Helper()

	tx, err := testDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("starting transaction: %v", err)
	}

	t.Cleanup(func() {
		_ = tx.Rollback()
	})

	return tx
}

func TestIntegration_IngestAndQuery(t *testing.T) {
	tx := newTestTx(t)
	logStore := store.New(tx)
	ctx := context.Background()

	entry := domain.LogEntry{
		Level:     "error",
		Message:   "connection refused",
		Service:   "gateway",
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"host": "10.0.0.1", "port": 5432},
	}

	if err := logStore.InsertLog(ctx, entry); err != nil {
		t.Fatalf("inserting log: %v", err)
	}

	results, err := logStore.QueryLogs(ctx, store.LogFilter{
		Level:   "error",
		Service: "gateway",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("querying logs: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Level != "error" {
		t.Errorf("level: got %q, want %q", r.Level, "error")
	}
	if r.Message != "connection refused" {
		t.Errorf("message: got %q, want %q", r.Message, "connection refused")
	}
	if r.Service != "gateway" {
		t.Errorf("service: got %q, want %q", r.Service, "gateway")
	}
	if r.Data["host"] != "10.0.0.1" {
		t.Errorf("data.host: got %v, want %q", r.Data["host"], "10.0.0.1")
	}
}

func TestIntegration_APIKeyLifecycle(t *testing.T) {
	tx := newTestTx(t)
	apiKeyStore := auth.NewApiKeyStore(tx)
	ctx := context.Background()

	rawKey, err := apiKeyStore.Create(ctx, "test-key", "integration-svc")
	if err != nil {
		t.Fatalf("creating api key: %v", err)
	}

	if rawKey == "" {
		t.Fatal("expected non-empty raw key")
	}

	// validate the key
	key, err := apiKeyStore.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validating api key: %v", err)
	}
	if key.Name != "test-key" {
		t.Errorf("name: got %q, want %q", key.Name, "test-key")
	}
	if key.Service != "integration-svc" {
		t.Errorf("service: got %q, want %q", key.Service, "integration-svc")
	}
	if key.RevokedAt != nil {
		t.Errorf("expected nil RevokedAt before revocation, got %v", key.RevokedAt)
	}

	// revoke the key
	if err := apiKeyStore.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoking api key: %v", err)
	}

	// validate again — should still return the key but with RevokedAt set
	revoked, err := apiKeyStore.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validating revoked api key: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Error("expected non-nil RevokedAt after revocation")
	}
}

func TestIntegration_ListServicesDistinct(t *testing.T) {
	tx := newTestTx(t)
	logStore := store.New(tx)
	ctx := context.Background()

	services := []string{"gateway", "api", "worker", "api", "gateway", "auth"}
	for _, service := range services {
		if err := logStore.InsertLog(ctx, domain.LogEntry{
			Level:     "info",
			Message:   fmt.Sprintf("log from %s", service),
			Service:   service,
			Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("inserting log for service %q: %v", service, err)
		}
	}

	result, err := logStore.ListServices(ctx)
	if err != nil {
		t.Fatalf("listing services: %v", err)
	}

	expected := []string{"api", "auth", "gateway", "worker"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d services, got %d: %v", len(expected), len(result), result)
	}

	for i, service := range expected {
		if result[i] != service {
			t.Errorf("services[%d]: got %q, want %q", i, result[i], service)
		}
	}
}

// newTestServer wires a full server (routes, middleware, handlers) on top of
// the test transaction and starts it on a random port.
func newTestServer(t *testing.T, tx *sql.Tx) *httptest.Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Config{
		Env:            "test",
		RateLimit:      10000,
		RateLimitBurst: 10000,
	}

	srv := server.New(cfg, logger,
		store.New(tx),
		auth.NewApiKeyStore(tx),
		auth.NewUserStore(tx),
		auth.NewSessionStore(tx),
	)

	testServer := httptest.NewServer(srv)
	t.Cleanup(testServer.Close)

	return testServer
}

func createTestAPIKey(t *testing.T, apiKeyStore *auth.ApiKeyStore, service string) string {
	t.Helper()

	rawKey, err := apiKeyStore.Create(context.Background(), "test-key", service)
	if err != nil {
		t.Fatalf("creating test API key: %v", err)
	}

	return rawKey
}

func ingestLog(t *testing.T, baseURL, apiKey string, entry map[string]any) *http.Response {
	t.Helper()

	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshaling entry: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}

	return resp
}

func TestIngestAndQuery(t *testing.T) {
	tx := newTestTx(t)
	testServer := newTestServer(t, tx)

	apiKey := createTestAPIKey(t, auth.NewApiKeyStore(tx), "payments")

	resp := ingestLog(t, testServer.URL, apiKey, map[string]any{
		"level":     "error",
		"message":   "payment gateway timeout",
		"service":   "payments",
		"timestamp": "2026-02-27T15:30:00Z",
		"data": map[string]any{
			"gateway":    "stripe",
			"timeout_ms": 5000,
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
	}

	logs, err := store.New(tx).QueryLogs(context.Background(), store.LogFilter{
		Service: "payments",
		Level:   "error",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("querying logs: %v", err)
	}

	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}

	if logs[0].Message != "payment gateway timeout" {
		t.Errorf("expected message %q, got %q", "payment gateway timeout", logs[0].Message)
	}

	if logs[0].Data["gateway"] != "stripe" {
		t.Errorf("expected data.gateway %q, got %v", "stripe", logs[0].Data["gateway"])
	}
}

func TestIngestWithoutAPIKey(t *testing.T) {
	tx := newTestTx(t)
	testServer := newTestServer(t, tx)

	resp := ingestLog(t, testServer.URL, "", map[string]any{
		"level":     "info",
		"message":   "test",
		"service":   "test",
		"timestamp": "2026-02-27T15:30:00Z",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestIngestWrongService(t *testing.T) {
	tx := newTestTx(t)
	testServer := newTestServer(t, tx)

	apiKey := createTestAPIKey(t, auth.NewApiKeyStore(tx), "payments")

	resp := ingestLog(t, testServer.URL, apiKey, map[string]any{
		"level":     "info",
		"message":   "test",
		"service":   "billing",
		"timestamp": "2026-02-27T15:30:00Z",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestQueryNoResults(t *testing.T) {
	tx := newTestTx(t)
	logStore := store.New(tx)

	logs, err := logStore.QueryLogs(context.Background(), store.LogFilter{
		Service: "nonexistent",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("expected no error for empty result, got %v", err)
	}

	if len(logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(logs))
	}
}
