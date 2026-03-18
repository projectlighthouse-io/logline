//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/database"
	"logline/internal/domain"
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
	s := store.New(tx)
	ctx := context.Background()

	entry := domain.LogEntry{
		Level:     "error",
		Message:   "connection refused",
		Service:   "gateway",
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"host": "10.0.0.1", "port": 5432},
	}

	if err := s.InsertLog(ctx, entry); err != nil {
		t.Fatalf("inserting log: %v", err)
	}

	results, err := s.QueryLogs(ctx, store.LogFilter{
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
	aks := auth.NewApiKeyStore(tx)
	ctx := context.Background()

	rawKey, err := aks.Create(ctx, "test-key", "integration-svc")
	if err != nil {
		t.Fatalf("creating api key: %v", err)
	}

	if rawKey == "" {
		t.Fatal("expected non-empty raw key")
	}

	// validate the key
	key, err := aks.Validate(ctx, rawKey)
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
	if err := aks.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoking api key: %v", err)
	}

	// validate again — should still return the key but with RevokedAt set
	revoked, err := aks.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validating revoked api key: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Error("expected non-nil RevokedAt after revocation")
	}
}

func TestIntegration_ListServicesDistinct(t *testing.T) {
	tx := newTestTx(t)
	s := store.New(tx)
	ctx := context.Background()

	services := []string{"gateway", "api", "worker", "api", "gateway", "auth"}
	for _, svc := range services {
		if err := s.InsertLog(ctx, domain.LogEntry{
			Level:     "info",
			Message:   fmt.Sprintf("log from %s", svc),
			Service:   svc,
			Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("inserting log for service %q: %v", svc, err)
		}
	}

	result, err := s.ListServices(ctx)
	if err != nil {
		t.Fatalf("listing services: %v", err)
	}

	expected := []string{"api", "auth", "gateway", "worker"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d services, got %d: %v", len(expected), len(result), result)
	}

	for i, svc := range expected {
		if result[i] != svc {
			t.Errorf("services[%d]: got %q, want %q", i, result[i], svc)
		}
	}
}
