package store

import (
	"context"
	"database/sql"
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
	ID        int64          `json:"id"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Service   string         `json:"service"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
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

	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")

	args := make([]any, 0, len(entries)*5)

	for i, entry := range entries {
		if i > 0 {
			sqlBuilder.WriteString(", ")
		}

		data, err := json.Marshal(entry.Data)
		if err != nil {
			return fmt.Errorf("marshaling entry %d data: %w", i, err)
		}

		offset := i * 5
		fmt.Fprintf(&sqlBuilder, "($%d,$%d,$%d,$%d,$%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5)

		args = append(args, entry.Level, entry.Message, entry.Service, entry.Timestamp, data)
	}

	_, err := s.db.ExecContext(ctx, sqlBuilder.String(), args...)
	return err
}

type LogFilter struct {
	Level      string
	Service    string
	From       time.Time
	To         time.Time
	Query      string
	DataFilter map[string]any
	Cursor     int64
	Limit      int
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
		var service string
		if err := rows.Scan(&service); err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		services = append(services, service)
	}

	return services, rows.Err()
}

func (s *Store) QueryLogs(ctx context.Context, filter LogFilter) ([]LogRow, error) {
	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("SELECT id, level, message, service, timestamp, data, created_at FROM logs WHERE 1=1")
	args := []any{}
	argPos := 1

	if filter.Level != "" {
		fmt.Fprintf(&sqlBuilder, " AND level = $%d", argPos)
		args = append(args, filter.Level)
		argPos++
	}

	if filter.Service != "" {
		fmt.Fprintf(&sqlBuilder, " AND service = $%d", argPos)
		args = append(args, filter.Service)
		argPos++
	}

	if !filter.From.IsZero() {
		fmt.Fprintf(&sqlBuilder, " AND timestamp >= $%d", argPos)
		args = append(args, filter.From)
		argPos++
	}

	if !filter.To.IsZero() {
		fmt.Fprintf(&sqlBuilder, " AND timestamp <= $%d", argPos)
		args = append(args, filter.To)
		argPos++
	}

	if filter.Query != "" {
		fmt.Fprintf(&sqlBuilder, " AND to_tsvector('english', message) @@ websearch_to_tsquery('english', $%d)", argPos)
		args = append(args, filter.Query)
		argPos++
	}

	if filter.DataFilter != nil {
		dataJSON, err := json.Marshal(filter.DataFilter)
		if err != nil {
			return nil, fmt.Errorf("marshal data filter: %w", err)
		}
		fmt.Fprintf(&sqlBuilder, " AND data @> $%d::jsonb", argPos)
		args = append(args, string(dataJSON))
		argPos++
	}

	if filter.Cursor > 0 {
		fmt.Fprintf(&sqlBuilder, " AND id < $%d", argPos)
		args = append(args, filter.Cursor)
		argPos++
	}

	sqlBuilder.WriteString(" ORDER BY id DESC")
	fmt.Fprintf(&sqlBuilder, " LIMIT $%d", argPos)
	args = append(args, filter.Limit)

	rows, err := s.db.QueryContext(ctx, sqlBuilder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var results []LogRow
	for rows.Next() {
		var row LogRow
		var data []byte

		if err := rows.Scan(&row.ID, &row.Level, &row.Message, &row.Service, &row.Timestamp, &data, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan log row: %w", err)
		}

		if data != nil {
			if err := json.Unmarshal(data, &row.Data); err != nil {
				return nil, fmt.Errorf("unmarshal data column: %w", err)
			}
		}

		results = append(results, row)
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
		var bucket TimeBucket
		if err := rows.Scan(&bucket.Time, &bucket.Count); err != nil {
			return nil, fmt.Errorf("scan time bucket: %w", err)
		}
		buckets = append(buckets, bucket)
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
		var serviceCount ServiceCount
		if err := rows.Scan(&serviceCount.Service, &serviceCount.Count); err != nil {
			return nil, fmt.Errorf("scan service count: %w", err)
		}
		results = append(results, serviceCount)
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
		var messageCount MessageCount
		if err := rows.Scan(&messageCount.Message, &messageCount.Count); err != nil {
			return nil, fmt.Errorf("scan message count: %w", err)
		}
		results = append(results, messageCount)
	}

	return results, rows.Err()
}

// FillHourGaps ensures there are exactly 24 contiguous hourly buckets,
// inserting zero-count entries for any missing hours.
func FillHourGaps(buckets []TimeBucket) []TimeBucket {
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-23 * time.Hour)

	lookup := make(map[int64]int, len(buckets))
	for _, bucket := range buckets {
		key := bucket.Time.UTC().Truncate(time.Hour).Unix()
		lookup[key] = bucket.Count
	}

	filled := make([]TimeBucket, 24)
	for i := 0; i < 24; i++ {
		hour := start.Add(time.Duration(i) * time.Hour)
		count := lookup[hour.Unix()]
		filled[i] = TimeBucket{Time: hour, Count: count}
	}

	return filled
}

func (s *Store) CopyFrom(ctx context.Context, entries []domain.LogEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	db, ok := s.db.(*sql.DB)
	if !ok {
		return 0, fmt.Errorf("CopyFrom requires *sql.DB, got %T", s.db)
	}

	conn, err := db.Conn(ctx)
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
