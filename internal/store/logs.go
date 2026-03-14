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
