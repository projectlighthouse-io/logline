package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"logline/internal/auth"
	"logline/internal/database"
	"logline/internal/domain"
	"logline/internal/store"
)

var (
	levels   = []string{"debug", "info", "info", "info", "warn", "error", "error", "fatal"}
	services = []string{"payment-api", "auth-api", "gateway", "order-api", "user-api"}
	messages = map[string][]string{
		"debug": {
			"cache hit for session lookup",
			"skipping rate limit check in dev mode",
			"request body size: 1247 bytes",
			"dns resolution took 2ms",
		},
		"info": {
			"request processed successfully",
			"user logged in",
			"order created",
			"payment captured",
			"webhook delivered",
			"cache refreshed",
			"health check passed",
			"deployment completed",
		},
		"warn": {
			"slow query detected: 450ms",
			"connection pool at 80% capacity",
			"retry attempt 2 of 3",
			"certificate expiring in 7 days",
			"disk space below 20%",
			"rate limit threshold approaching",
		},
		"error": {
			"connection refused to downstream service",
			"timeout waiting for database response",
			"failed to process payment: gateway timeout",
			"authentication failed for user",
			"webhook delivery failed after 3 retries",
			"circuit breaker open for billing-api",
			"TLS handshake timeout to vault.internal:8200",
			"redis BRPOP timeout on queue:payments",
		},
		"fatal": {
			"out of memory: cannot allocate 512MB",
			"database connection pool exhausted",
			"panic: nil pointer dereference in handler",
		},
	}
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://logline:password@localhost:5433/logline?sslmode=disable"
	}

	count := 5000
	batchSize := 500

	db, err := database.Open(dsn, 25, 5)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	if err := database.Migrate(ctx, db); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	s := store.New(db)
	us := auth.NewUserStore(db)
	aks := auth.NewApiKeyStore(db)

	// create admin user
	_, err = us.Create(ctx, "platform-engineer@logline.test", "secret12", "Platform Engineer")
	if err != nil {
		if auth.IsUniqueViolation(err) {
			fmt.Println("admin user already exists, skipping")
		} else {
			fmt.Fprintf(os.Stderr, "create user: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("created user: platform-engineer@logline.test / secret12")
	}

	// create API keys for each service
	for _, svc := range services {
		key, err := aks.Create(ctx, svc+"-prod", svc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create key for %s: %v\n", svc, err)
			continue
		}
		fmt.Printf("created API key for %s: %s\n", svc, key)
	}

	fmt.Printf("\nseeding %d log entries...\n", count)
	start := time.Now()

	for i := 0; i < count; i += batchSize {
		n := batchSize
		if i+n > count {
			n = count - i
		}

		entries := make([]domain.LogEntry, n)
		for j := range entries {
			level := levels[rand.Intn(len(levels))]
			msgs := messages[level]

			entries[j] = domain.LogEntry{
				Level:     level,
				Message:   msgs[rand.Intn(len(msgs))],
				Service:   services[rand.Intn(len(services))],
				Timestamp: time.Now().Add(-time.Duration(rand.Intn(86400)) * time.Second),
				Data: map[string]any{
					"request_id": fmt.Sprintf("req_%06d", i+j),
					"host":       fmt.Sprintf("node-%d", rand.Intn(5)+1),
				},
			}
		}

		if _, err := s.CopyFrom(ctx, entries); err != nil {
			fmt.Fprintf(os.Stderr, "copy: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("  %d/%d\n", i+n, count)
	}

	fmt.Printf("done in %s\n", time.Since(start).Round(time.Millisecond))
}
