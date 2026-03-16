package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ApiKey struct {
	ID        int64
	Name      string
	KeyPrefix string
	Service   string
	CreatedAt time.Time
	RevokedAt *time.Time
}

type ApiKeyStore struct {
	db *sql.DB
}

func NewApiKeyStore(db *sql.DB) *ApiKeyStore {
	return &ApiKeyStore{db: db}
}

func (s *ApiKeyStore) Create(ctx context.Context, name, service string) (string, error) {
	rawKey, err := GenerateKey()
	if err != nil {
		return "", fmt.Errorf("generating api key: %w", err)
	}

	keyHash := HashKey(rawKey)
	prefix := ExtractPrefix(rawKey)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO api_keys (name, key_prefix, key_hash, service)
		 VALUES ($1, $2, $3, $4)`,
		name, prefix, keyHash, service,
	)
	if err != nil {
		return "", fmt.Errorf("inserting api key: %w", err)
	}

	return rawKey, nil
}

func (s *ApiKeyStore) Validate(ctx context.Context, rawKey string) (*ApiKey, error) {
	keyHash := HashKey(rawKey)

	var key ApiKey
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, key_prefix, service, created_at, revoked_at
		 FROM api_keys
		 WHERE key_hash = $1`,
		keyHash,
	).Scan(&key.ID, &key.Name, &key.KeyPrefix, &key.Service, &key.CreatedAt, &key.RevokedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid api key")
	}
	if err != nil {
		return nil, fmt.Errorf("querying api key: %w", err)
	}

	return &key, nil
}

func (s *ApiKeyStore) Revoke(ctx context.Context, keyID int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`,
		keyID,
	)
	if err != nil {
		return fmt.Errorf("revoking api key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("api key not found or already revoked")
	}

	return nil
}
