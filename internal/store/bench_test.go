package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/database"
	"logline/internal/domain"
	"logline/internal/store"
)

func setupBenchDB(b *testing.B) *sql.DB {
	b.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		b.Skip("DATABASE_URL not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		b.Fatalf("opening database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		b.Fatalf("connecting to database: %v", err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		db.Close()
		b.Fatalf("running migrations: %v", err)
	}

	if _, err := db.ExecContext(ctx, "TRUNCATE logs"); err != nil {
		db.Close()
		b.Fatalf("truncating logs: %v", err)
	}

	b.Cleanup(func() { db.Close() })

	return db
}

func makeEntries(n int) []domain.LogEntry {
	entries := make([]domain.LogEntry, n)
	for i := range entries {
		entries[i] = domain.LogEntry{
			Level:     "info",
			Message:   fmt.Sprintf("benchmark entry %d", i),
			Service:   "bench",
			Timestamp: time.Now(),
			Data:      map[string]any{"iteration": i},
		}
	}
	return entries
}

func BenchmarkInsertSingle(b *testing.B) {
	db := setupBenchDB(b)
	s := store.New(db)
	entries := makeEntries(b.N)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.InsertLog(ctx, entries[i]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBatch100(b *testing.B) {
	db := setupBenchDB(b)
	s := store.New(db)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries := makeEntries(100)
		if err := s.InsertBatch(ctx, entries); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyFrom100(b *testing.B) {
	db := setupBenchDB(b)
	s := store.New(db)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries := makeEntries(100)
		if _, err := s.CopyFrom(ctx, entries); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBatch(b *testing.B) {
	for _, size := range []int{10, 50, 100, 500, 1000} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			db := setupBenchDB(b)
			s := store.New(db)
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				entries := makeEntries(size)
				if err := s.InsertBatch(ctx, entries); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
