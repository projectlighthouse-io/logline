package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/domain"
	"logline/internal/store"
)

var (
	levels   = []string{"debug", "info", "warn", "error", "fatal"}
	services = []string{"auth-api", "payment-api", "order-api", "user-api", "gateway"}
	messages = []string{
		"request processed successfully",
		"connection refused to downstream service",
		"timeout waiting for database response",
		"authentication failed for user",
		"rate limit exceeded for client",
		"cache miss for session lookup",
		"disk space running low on volume",
		"certificate expiring in 7 days",
		"deployment rollback initiated",
		"health check failed on port 8080",
	}
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://logline:password@localhost:5433/logline?sslmode=disable"
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping: %v\n", err)
		os.Exit(1)
	}

	s := store.New(db)

	// seed 500,000 rows
	total := 500_000
	batchSize := 1000

	var count int
	db.QueryRowContext(ctx, "SELECT count(*) FROM logs").Scan(&count)
	if count >= total {
		fmt.Printf("already have %d rows, skipping seed\n", count)
	} else {
		fmt.Printf("seeding %d rows (batch size %d)...\n", total, batchSize)
		start := time.Now()

		for i := 0; i < total; i += batchSize {
			n := batchSize
			if i+n > total {
				n = total - i
			}

			entries := make([]domain.LogEntry, n)
			for j := range entries {
				entries[j] = domain.LogEntry{
					Level:     levels[rand.Intn(len(levels))],
					Message:   messages[rand.Intn(len(messages))],
					Service:   services[rand.Intn(len(services))],
					Timestamp: time.Now().Add(-time.Duration(rand.Intn(86400)) * time.Second),
					Data:      map[string]any{"request_id": fmt.Sprintf("req_%d", i+j)},
				}
			}

			if _, err := s.CopyFrom(ctx, entries); err != nil {
				fmt.Fprintf(os.Stderr, "copy: %v\n", err)
				os.Exit(1)
			}

			if (i/batchSize)%50 == 0 {
				fmt.Printf("  %d/%d\n", i, total)
			}
		}

		fmt.Printf("seeded %d rows in %s\n\n", total, time.Since(start).Round(time.Millisecond))
	}

	// benchmark: full-text search WITHOUT GIN index
	fmt.Println("=== search WITHOUT GIN index ===")
	db.ExecContext(ctx, "DROP INDEX IF EXISTS idx_logs_message_fts")

	searchQuery := "connection refused"
	runSearch(s, ctx, searchQuery, 5)

	// create GIN index
	fmt.Println("\ncreating GIN index on to_tsvector('english', message)...")
	idxStart := time.Now()
	_, err = db.ExecContext(ctx, "CREATE INDEX idx_logs_message_fts ON logs USING gin(to_tsvector('english', message))")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create index: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("index created in %s\n\n", time.Since(idxStart).Round(time.Millisecond))

	// benchmark: full-text search WITH GIN index
	fmt.Println("=== search WITH GIN index ===")
	runSearch(s, ctx, searchQuery, 5)

	// cleanup
	db.ExecContext(ctx, "DROP INDEX IF EXISTS idx_logs_message_fts")
}

func runSearch(s *store.Store, ctx context.Context, query string, runs int) {
	var total time.Duration

	for i := 0; i < runs; i++ {
		start := time.Now()
		results, err := s.QueryLogs(ctx, store.LogFilter{
			Query: query,
			Limit: 50,
		})
		d := time.Since(start)
		total += d

		if err != nil {
			fmt.Fprintf(os.Stderr, "  run %d: error: %v\n", i+1, err)
			continue
		}

		fmt.Printf("  run %d: %d results in %s\n", i+1, len(results), d.Round(time.Microsecond))
	}

	avg := total / time.Duration(runs)
	fmt.Printf("  avg: %s\n", avg.Round(time.Microsecond))
}
