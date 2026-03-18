package store

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX abstracts *sql.DB and *sql.Tx so the store can run inside a transaction.
// *sql.DB satisfies this interface, so existing callers are unaffected.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db DBTX
}

func New(db DBTX) *Store {
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error {
	db, ok := s.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("Ping requires *sql.DB, got %T", s.db)
	}
	return db.PingContext(ctx)
}
