package store

import "time"

// LogRow is the database representation of a log entry.
// separate from domain.LogEntry because the row has fields
// the API payload does not (id, created_at) and may diverge
// further as the schema evolves.
type LogRow struct {
	ID        int64
	Level     string
	Message   string
	Service   string
	Timestamp time.Time
	Data      map[string]any
	CreatedAt time.Time
}
