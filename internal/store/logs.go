package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"logline/internal/domain"
)

type TimeBucket struct {
	Time  time.Time `json:"time"`
	Count int       `json:"count"`
}

type ServiceCount struct {
	Service string `json:"service"`
	Count   int    `json:"count"`
}

type MessageCount struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

type LogRow struct {
	ID        int64
	Level     string
	Message   string
	Service   string
	Timestamp time.Time
	Data      map[string]any
	CreatedAt time.Time
}

func (s *Store) InsertLog(ctx context.Context, entry domain.LogEntry) error {
	data, err := json.Marshal(entry.Data)
	if err != nil {
		return fmt.Errorf("marshaling data: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO logs (level, message, service, timestamp, data) VALUES ($1, $2, $3, $4, $5)",
		entry.Level, entry.Message, entry.Service, entry.Timestamp, data,
	)
	return err
}

func (s *Store) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")

	args := make([]any, 0, len(entries)*5)

	for i, entry := range entries {
		if i > 0 {
			b.WriteString(", ")
		}

		data, err := json.Marshal(entry.Data)
		if err != nil {
			return fmt.Errorf("marshaling entry %d data: %w", i, err)
		}

		offset := i * 5
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d,$%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5)

		args = append(args, entry.Level, entry.Message, entry.Service, entry.Timestamp, data)
	}

	_, err := s.db.ExecContext(ctx, b.String(), args...)
	return err
}

type LogFilter struct {
	Level   string
	Service string
	From    time.Time
	To      time.Time
	Query   string
	Cursor  int64
	Limit   int
}

func (f LogFilter) FromDate() string {
	if f.From.IsZero() {
		return ""
	}
	return f.From.Format("2006-01-02")
}

func (f LogFilter) ToDate() string {
	if f.To.IsZero() {
		return ""
	}
	return f.To.Format("2006-01-02")
}

func (s *Store) ListServices(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT service FROM logs ORDER BY service")
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var services []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		services = append(services, svc)
	}

	return services, rows.Err()
}

func (s *Store) QueryLogs(ctx context.Context, f LogFilter) ([]LogRow, error) {
	var b strings.Builder
	b.WriteString("SELECT id, level, message, service, timestamp, data, created_at FROM logs WHERE 1=1")
	args := []any{}
	argPos := 1

	if f.Level != "" {
		fmt.Fprintf(&b, " AND level = $%d", argPos)
		args = append(args, f.Level)
		argPos++
	}

	if f.Service != "" {
		fmt.Fprintf(&b, " AND service = $%d", argPos)
		args = append(args, f.Service)
		argPos++
	}

	if !f.From.IsZero() {
		fmt.Fprintf(&b, " AND timestamp >= $%d", argPos)
		args = append(args, f.From)
		argPos++
	}

	if !f.To.IsZero() {
		fmt.Fprintf(&b, " AND timestamp <= $%d", argPos)
		args = append(args, f.To)
		argPos++
	}

	if f.Query != "" {
		fmt.Fprintf(&b, " AND to_tsvector('english', message) @@ websearch_to_tsquery('english', $%d)", argPos)
		args = append(args, f.Query)
		argPos++
	}

	if f.Cursor > 0 {
		fmt.Fprintf(&b, " AND id < $%d", argPos)
		args = append(args, f.Cursor)
		argPos++
	}

	b.WriteString(" ORDER BY id DESC")
	fmt.Fprintf(&b, " LIMIT $%d", argPos)
	args = append(args, f.Limit)

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var results []LogRow
	for rows.Next() {
		var r LogRow
		var data []byte

		if err := rows.Scan(&r.ID, &r.Level, &r.Message, &r.Service, &r.Timestamp, &data, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan log row: %w", err)
		}

		if data != nil {
			if err := json.Unmarshal(data, &r.Data); err != nil {
				return nil, fmt.Errorf("unmarshal data column: %w", err)
			}
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate log rows: %w", err)
	}

	return results, nil
}

func (s *Store) ErrorsPerHour(ctx context.Context) ([]TimeBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT date_trunc('hour', timestamp) AS hour, COUNT(*) AS count
		FROM logs
		WHERE level IN ('error', 'fatal')
		  AND timestamp >= NOW() - INTERVAL '24 hours'
		GROUP BY hour
		ORDER BY hour
	`)
	if err != nil {
		return nil, fmt.Errorf("errors per hour: %w", err)
	}
	defer rows.Close()

	var buckets []TimeBucket
	for rows.Next() {
		var b TimeBucket
		if err := rows.Scan(&b.Time, &b.Count); err != nil {
			return nil, fmt.Errorf("scan time bucket: %w", err)
		}
		buckets = append(buckets, b)
	}

	return buckets, rows.Err()
}

func (s *Store) VolumeByService(ctx context.Context) ([]ServiceCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT service, COUNT(*) AS count
		FROM logs
		WHERE timestamp >= NOW() - INTERVAL '24 hours'
		GROUP BY service
		ORDER BY count DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, fmt.Errorf("volume by service: %w", err)
	}
	defer rows.Close()

	var results []ServiceCount
	for rows.Next() {
		var sc ServiceCount
		if err := rows.Scan(&sc.Service, &sc.Count); err != nil {
			return nil, fmt.Errorf("scan service count: %w", err)
		}
		results = append(results, sc)
	}

	return results, rows.Err()
}

func (s *Store) TopErrors(ctx context.Context) ([]MessageCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message, COUNT(*) AS count
		FROM logs
		WHERE level IN ('error', 'fatal')
		  AND timestamp >= NOW() - INTERVAL '24 hours'
		GROUP BY message
		ORDER BY count DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, fmt.Errorf("top errors: %w", err)
	}
	defer rows.Close()

	var results []MessageCount
	for rows.Next() {
		var mc MessageCount
		if err := rows.Scan(&mc.Message, &mc.Count); err != nil {
			return nil, fmt.Errorf("scan message count: %w", err)
		}
		results = append(results, mc)
	}

	return results, rows.Err()
}

// FillHourGaps ensures there are exactly 24 contiguous hourly buckets,
// inserting zero-count entries for any missing hours.
func FillHourGaps(buckets []TimeBucket) []TimeBucket {
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-23 * time.Hour)

	lookup := make(map[int64]int, len(buckets))
	for _, b := range buckets {
		key := b.Time.UTC().Truncate(time.Hour).Unix()
		lookup[key] = b.Count
	}

	filled := make([]TimeBucket, 24)
	for i := 0; i < 24; i++ {
		t := start.Add(time.Duration(i) * time.Hour)
		count := lookup[t.Unix()]
		filled[i] = TimeBucket{Time: t, Count: count}
	}

	return filled
}

func (s *Store) CopyFrom(ctx context.Context, entries []domain.LogEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Close()

	var count int64

	err = conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()

		rows := make([][]any, len(entries))
		for i, entry := range entries {
			data, err := json.Marshal(entry.Data)
			if err != nil {
				return fmt.Errorf("marshaling entry %d: %w", i, err)
			}
			rows[i] = []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}
		}

		count, err = pgxConn.CopyFrom(
			ctx,
			pgx.Identifier{"logs"},
			[]string{"level", "message", "service", "timestamp", "data"},
			pgx.CopyFromRows(rows),
		)
		return err
	})

	return count, err
}
