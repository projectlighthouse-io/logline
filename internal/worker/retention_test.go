package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/database"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping DB test")
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

	t.Cleanup(func() { db.Close() })

	return db
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRetentionWorker_DeletesExpired(t *testing.T) {
	db := setupTestDB(t)

	// insert a log entry with created_at far in the past
	expired := time.Now().AddDate(0, 0, -60)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO logs (level, message, service, timestamp, created_at) VALUES ($1, $2, $3, $4, $5)`,
		"info", "old log", "test-retention", expired, expired,
	)
	if err != nil {
		t.Fatalf("inserting expired log: %v", err)
	}

	// insert a recent log that should NOT be deleted
	recent := time.Now()
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO logs (level, message, service, timestamp, created_at) VALUES ($1, $2, $3, $4, $5)`,
		"info", "recent log", "test-retention", recent, recent,
	)
	if err != nil {
		t.Fatalf("inserting recent log: %v", err)
	}

	w := NewRetentionWorker(db, 30, 1*time.Hour, testLogger())

	// run one pass directly
	w.deleteExpired(context.Background())

	// verify expired log was deleted
	var count int
	err = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM logs WHERE service = $1 AND message = $2`,
		"test-retention", "old log",
	).Scan(&count)
	if err != nil {
		t.Fatalf("counting expired logs: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 expired logs, got %d", count)
	}

	// verify recent log was kept
	err = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM logs WHERE service = $1 AND message = $2`,
		"test-retention", "recent log",
	).Scan(&count)
	if err != nil {
		t.Fatalf("counting recent logs: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 recent log, got %d", count)
	}

	// cleanup
	db.ExecContext(context.Background(),
		`DELETE FROM logs WHERE service = $1`, "test-retention",
	)
}

func TestRetentionWorker_StopsOnCancel(t *testing.T) {
	db := setupTestDB(t)

	w := NewRetentionWorker(db, 30, 100*time.Millisecond, testLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Run returned as expected
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
