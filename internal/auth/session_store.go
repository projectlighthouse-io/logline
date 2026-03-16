package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type Session struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type SessionStore struct {
	db *sql.DB
}

func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db}
}

func (s *SessionStore) Create(ctx context.Context, userID int64) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	tokenHash := hashToken(token)
	expiresAt := time.Now().Add(24 * time.Hour)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	return token, nil
}

func (s *SessionStore) Validate(ctx context.Context, rawToken string) (*Session, *User, error) {
	tokenHash := hashToken(rawToken)

	session := &Session{}
	user := &User{}

	err := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.user_id, s.token_hash, s.expires_at, s.created_at,
		        u.id, u.email, u.password_hash, u.name, u.created_at, u.updated_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1`,
		tokenHash,
	).Scan(
		&session.ID, &session.UserID, &session.TokenHash, &session.ExpiresAt,
		&session.CreatedAt,
		&user.ID, &user.Email, &user.PasswordHash, &user.Name,
		&user.CreatedAt, &user.UpdatedAt,
	)

	if err != nil {
		return nil, nil, fmt.Errorf("validating session: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		// expired -- clean up and reject
		_, _ = s.db.ExecContext(ctx,
			`DELETE FROM sessions WHERE id = $1`, session.ID)
		return nil, nil, fmt.Errorf("session expired")
	}

	return session, user, nil
}

func (s *SessionStore) Delete(ctx context.Context, rawToken string) error {
	tokenHash := hashToken(rawToken)

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE token_hash = $1`,
		tokenHash,
	)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}

	return nil
}

func (s *SessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < now()`,
	)
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
