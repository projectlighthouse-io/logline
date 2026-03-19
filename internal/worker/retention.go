package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

type RetentionWorker struct {
	db            *sql.DB
	retentionDays int
	interval      time.Duration
	logger        *slog.Logger
}

func NewRetentionWorker(db *sql.DB, retentionDays int, interval time.Duration, logger *slog.Logger) *RetentionWorker {
	return &RetentionWorker{
		db:            db,
		retentionDays: retentionDays,
		interval:      interval,
		logger:        logger,
	}
}

const retentionBatchSize = 1000

// Run deletes expired logs in batches until the context is cancelled.
func (w *RetentionWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.deleteExpired(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *RetentionWorker) deleteExpired(ctx context.Context) {
	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)

	for {
		if ctx.Err() != nil {
			return
		}

		res, err := w.db.ExecContext(ctx,
			`DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE created_at < $1 LIMIT $2)`,
			cutoff, retentionBatchSize,
		)
		if err != nil {
			w.logger.Error("retention delete failed",
				slog.String("error", err.Error()),
			)
			return
		}

		affected, err := res.RowsAffected()
		if err != nil {
			w.logger.Error("retention rows affected check failed",
				slog.String("error", err.Error()),
			)
			return
		}

		if affected > 0 {
			w.logger.Info("retention worker deleted expired logs",
				slog.Int64("deleted", affected),
			)
		}

		// no more rows to delete
		if affected < retentionBatchSize {
			return
		}
	}
}
