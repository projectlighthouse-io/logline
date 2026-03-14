package store

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"logline/internal/domain"
)

type BufferedStore struct {
	store     *Store
	mu        sync.Mutex
	buf       []domain.LogEntry
	batchSize int
	interval  time.Duration
	done      chan struct{}
}

func NewBufferedStore(store *Store, batchSize int, interval time.Duration) *BufferedStore {
	bs := &BufferedStore{
		store:     store,
		buf:       make([]domain.LogEntry, 0, batchSize),
		batchSize: batchSize,
		interval:  interval,
		done:      make(chan struct{}),
	}
	go bs.flushLoop()
	return bs
}

func (bs *BufferedStore) Add(entry domain.LogEntry) {
	bs.mu.Lock()
	bs.buf = append(bs.buf, entry)
	full := len(bs.buf) >= bs.batchSize
	bs.mu.Unlock()

	if full {
		bs.flush()
	}
}

func (bs *BufferedStore) flush() {
	bs.mu.Lock()
	if len(bs.buf) == 0 {
		bs.mu.Unlock()
		return
	}

	entries := bs.buf
	bs.buf = make([]domain.LogEntry, 0, bs.batchSize)
	bs.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := bs.store.InsertBatch(ctx, entries); err != nil {
		// todo: retry with backoff, or write to a local WAL file
		slog.Error("batch insert failed",
			slog.Int("entries", len(entries)),
			slog.String("error", err.Error()),
		)
	}
}

func (bs *BufferedStore) flushLoop() {
	ticker := time.NewTicker(bs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bs.flush()
		case <-bs.done:
			bs.flush()
			return
		}
	}
}

func (bs *BufferedStore) Close() {
	close(bs.done)
}
