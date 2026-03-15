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
		t.Fatalf("connecting to database: %v", err)
	}

	if err := database.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}

	if _, err := db.ExecContext(ctx, "TRUNCATE logs"); err != nil {
		db.Close()
		t.Fatalf("truncating logs: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

func TestQueryLogs_FilterByLevel(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for _, lvl := range []string{"error", "error", "error", "info", "info"} {
		if err := s.InsertLog(ctx, domain.LogEntry{
			Level: lvl, Message: "test", Service: "api", Timestamp: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.QueryLogs(ctx, store.LogFilter{Level: "error", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 errors, got %d", len(results))
	}

	for _, r := range results {
		if r.Level != "error" {
			t.Errorf("expected level error, got %q", r.Level)
		}
	}
}

func TestQueryLogs_CursorPagination(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		if err := s.InsertLog(ctx, domain.LogEntry{
			Level: "info", Message: fmt.Sprintf("entry %d", i),
			Service: "api", Timestamp: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	page1, err := s.QueryLogs(ctx, store.LogFilter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 5 {
		t.Fatalf("expected 5 results, got %d", len(page1))
	}

	cursor := page1[len(page1)-1].ID
	page2, err := s.QueryLogs(ctx, store.LogFilter{Cursor: cursor, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 5 {
		t.Fatalf("expected 5 results, got %d", len(page2))
	}

	if page2[0].ID >= page1[len(page1)-1].ID {
		t.Errorf("page2 first ID (%d) should be less than page1 last ID (%d)",
			page2[0].ID, page1[len(page1)-1].ID)
	}
}

func TestQueryLogs_FullTextSearch(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	if err := s.InsertLog(ctx, domain.LogEntry{
		Level: "error", Message: "connection refused to downstream",
		Service: "gateway", Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertLog(ctx, domain.LogEntry{
		Level: "info", Message: "request processed successfully",
		Service: "gateway", Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	results, err := s.QueryLogs(ctx, store.LogFilter{Query: "connection refused", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Message != "connection refused to downstream" {
		t.Errorf("unexpected message: %q", results[0].Message)
	}
}

func TestQueryLogs_NoFilters(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if err := s.InsertLog(ctx, domain.LogEntry{
			Level: "info", Message: "test", Service: "api", Timestamp: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.QueryLogs(ctx, store.LogFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 10 {
		t.Errorf("expected 10 results, got %d", len(results))
	}

	for i := 1; i < len(results); i++ {
		if results[i].ID >= results[i-1].ID {
			t.Errorf("results not in descending order at index %d", i)
		}
	}
}
