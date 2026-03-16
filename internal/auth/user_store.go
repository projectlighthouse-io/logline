package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	Name         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

func (s *UserStore) Create(ctx context.Context, email, password, name string) (*User, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	user := &User{}
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, email, password_hash, name, created_at, updated_at`,
		email, hash, name,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name,
		&user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return user, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*User, error) {
	user := &User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name,
		&user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("getting user by email: %w", err)
	}

	return user, nil
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
