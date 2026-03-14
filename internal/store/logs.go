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
