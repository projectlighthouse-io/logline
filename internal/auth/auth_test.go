package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/database"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connecting: %v", err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatalf("migrations: %v", err)
	}

	// clean api_keys between tests
	db.ExecContext(ctx, "TRUNCATE api_keys")

	t.Cleanup(func() { db.Close() })

	return db
}

func TestGenerateKey_Format(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(key, "ll_") {
		t.Errorf("expected ll_ prefix, got %q", key[:3])
	}

	// ll_ + 43 chars of base64url (32 bytes encoded)
	if len(key) != 46 {
		t.Errorf("expected 46 chars, got %d", len(key))
	}
}

func TestHashKey_Deterministic(t *testing.T) {
	h1 := auth.HashKey("ll_test123")
	h2 := auth.HashKey("ll_test123")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}

	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(h1))
	}
}

func TestHashKey_DifferentInputs(t *testing.T) {
	h1 := auth.HashKey("ll_key1")
	h2 := auth.HashKey("ll_key2")

	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestCreateAndValidate(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, err := store.Create(ctx, "test-key", "payments")
	if err != nil {
		t.Fatal(err)
	}

	key, err := store.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if key.Name != "test-key" {
		t.Errorf("expected name test-key, got %q", key.Name)
	}

	if key.Service != "payments" {
		t.Errorf("expected service payments, got %q", key.Service)
	}

	if key.RevokedAt != nil {
		t.Error("new key should not be revoked")
	}
}

func TestValidate_InvalidKey(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)

	_, err := store.Validate(context.Background(), "ll_doesnotexist")
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestRevoke_ThenValidate(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, err := store.Create(ctx, "revoke-test", "auth")
	if err != nil {
		t.Fatal(err)
	}

	// validate before revoke — should work
	key, err := store.Validate(ctx, rawKey)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}

	// validate after revoke — should still return the key (with RevokedAt set)
	key, err = store.Validate(ctx, rawKey)
	if err != nil {
		t.Fatalf("validate after revoke should still return key: %v", err)
	}

	if key.RevokedAt == nil {
		t.Error("revoked key should have RevokedAt set")
	}
}

func TestRevoke_AlreadyRevoked(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, _ := store.Create(ctx, "double-revoke", "api")
	key, _ := store.Validate(ctx, rawKey)

	store.Revoke(ctx, key.ID)

	err := store.Revoke(ctx, key.ID)
	if err == nil {
		t.Error("expected error revoking already-revoked key")
	}
}

func TestMiddleware_NoHeader(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)

	handler := auth.Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddleware_ValidKey(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)

	rawKey, _ := store.Create(context.Background(), "mw-test", "api")

	var capturedKey *auth.ApiKey

	handler := auth.Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = auth.KeyFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	if capturedKey == nil {
		t.Fatal("expected api key in context")
	}

	if capturedKey.Service != "api" {
		t.Errorf("expected service api, got %q", capturedKey.Service)
	}
}

func TestMiddleware_RevokedKey(t *testing.T) {
	db := setupTestDB(t)
	store := auth.NewApiKeyStore(db)
	ctx := context.Background()

	rawKey, _ := store.Create(ctx, "revoked-mw", "api")
	key, _ := store.Validate(ctx, rawKey)
	store.Revoke(ctx, key.ID)

	handler := auth.Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for revoked key, got %d", rec.Code)
	}
}
