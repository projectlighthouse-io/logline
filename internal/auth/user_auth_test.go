package auth_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/database"
)

func setupUserTestDB(t *testing.T) *sql.DB {
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

	// clean between tests (sessions first due to FK)
	db.ExecContext(ctx, "TRUNCATE sessions, users CASCADE")

	t.Cleanup(func() { db.Close() })

	return db
}

// replicates sha256 hex hashing for test data manipulation
func hashTokenForTest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func TestCreateUser_Success(t *testing.T) {
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)

	user, err := store.Create(context.Background(), "test@example.com", "securepassword", "Test User")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if user.ID == 0 {
		t.Error("expected non-zero user ID")
	}
	if user.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %q", user.Email)
	}
	if user.Name != "Test User" {
		t.Errorf("expected name Test User, got %q", user.Name)
	}
	if user.PasswordHash == "" {
		t.Error("expected password hash to be set")
	}
	if user.PasswordHash == "securepassword" {
		t.Error("password hash must not be plaintext")
	}
	if user.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)
	ctx := context.Background()

	_, err := store.Create(ctx, "dupe@example.com", "password123", "First")
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Create(ctx, "dupe@example.com", "password456", "Second")
	if err == nil {
		t.Fatal("expected error for duplicate email")
	}

	if !auth.IsUniqueViolation(err) {
		t.Errorf("expected unique violation, got %v", err)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	// password length validation is in the handler, not the store.
	// the store itself does not reject short passwords.
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)

	user, err := store.Create(context.Background(), "short@example.com", "short", "Short Pass")
	if err != nil {
		t.Fatalf("store should not reject short passwords (handler's job), got %v", err)
	}
	if user.ID == 0 {
		t.Error("expected non-zero user ID")
	}
}

func TestGetByEmail_NotFound(t *testing.T) {
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)

	_, err := store.GetByEmail(context.Background(), "nonexistent@example.com")
	if err == nil {
		t.Fatal("expected error for nonexistent email")
	}
}

func TestVerifyPassword_Correct(t *testing.T) {
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)

	user, err := store.Create(context.Background(), "verify@example.com", "correctpassword", "Verify")
	if err != nil {
		t.Fatal(err)
	}

	if err := auth.VerifyPassword(user.PasswordHash, "correctpassword"); err != nil {
		t.Errorf("expected password to match, got %v", err)
	}
}

func TestVerifyPassword_Wrong(t *testing.T) {
	db := setupUserTestDB(t)
	store := auth.NewUserStore(db)

	user, err := store.Create(context.Background(), "wrong@example.com", "correctpassword", "Wrong")
	if err != nil {
		t.Fatal(err)
	}

	if err := auth.VerifyPassword(user.PasswordHash, "wrongpassword"); err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestSessionCreate_And_Validate(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "session@example.com", "password123", "Session User")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}

	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars", len(token))
	}

	session, validatedUser, err := sessionStore.Validate(ctx, token)
	if err != nil {
		t.Fatalf("validating session: %v", err)
	}

	if session.UserID != user.ID {
		t.Errorf("expected user_id %d, got %d", user.ID, session.UserID)
	}
	if validatedUser.Email != "session@example.com" {
		t.Errorf("expected email session@example.com, got %q", validatedUser.Email)
	}
}

func TestSessionValidate_Expired(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "expired@example.com", "password123", "Expired")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// manually set the session to be expired
	tokenHash := hashTokenForTest(token)
	_, err = db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = $1 WHERE token_hash = $2`,
		time.Now().Add(-1*time.Hour), tokenHash,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = sessionStore.Validate(ctx, token)
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestSessionValidate_InvalidToken(t *testing.T) {
	db := setupUserTestDB(t)
	sessionStore := auth.NewSessionStore(db)

	_, _, err := sessionStore.Validate(context.Background(), "not_a_real_token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestSessionDelete(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "logout@example.com", "password123", "Logout")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := sessionStore.Delete(ctx, token); err != nil {
		t.Fatalf("deleting session: %v", err)
	}

	_, _, err = sessionStore.Validate(ctx, token)
	if err == nil {
		t.Error("expected error after session deletion")
	}
}

func TestSessionDeleteExpired(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "cleanup@example.com", "password123", "Cleanup")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// manually expire the session
	tokenHash := hashTokenForTest(token)
	_, err = db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = $1 WHERE token_hash = $2`,
		time.Now().Add(-1*time.Hour), tokenHash,
	)
	if err != nil {
		t.Fatal(err)
	}

	count, err := sessionStore.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("deleting expired sessions: %v", err)
	}

	if count < 1 {
		t.Errorf("expected at least 1 deleted session, got %d", count)
	}
}

func TestMiddleware_NoSession_Returns401(t *testing.T) {
	db := setupUserTestDB(t)
	sessionStore := auth.NewSessionStore(db)

	handler := requireSessionTestMiddleware(sessionStore)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddleware_ValidSession_PassesThrough(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "middleware@example.com", "password123", "MW User")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	var gotUser bool
	handler := requireSessionTestMiddleware(sessionStore)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser = true
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !gotUser {
		t.Error("handler was not called")
	}
}

func TestMiddleware_ExpiredSession_ClearsCookie(t *testing.T) {
	db := setupUserTestDB(t)
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	ctx := context.Background()

	user, err := userStore.Create(ctx, "expired-mw@example.com", "password123", "Expired MW")
	if err != nil {
		t.Fatal(err)
	}

	token, err := sessionStore.Create(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// manually expire
	tokenHash := hashTokenForTest(token)
	_, err = db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = $1 WHERE token_hash = $2`,
		time.Now().Add(-1*time.Hour), tokenHash,
	)
	if err != nil {
		t.Fatal(err)
	}

	handler := requireSessionTestMiddleware(sessionStore)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "session_token" && c.MaxAge < 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected session_token cookie to be cleared (MaxAge < 0)")
	}
}

// simplified version of the server's requireSession for testing the auth layer
func requireSessionTestMiddleware(ss *auth.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session_token")
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			_, _, err = ss.Validate(r.Context(), cookie.Value)
			if err != nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "session_token",
					Value:    "",
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   -1,
				})
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
