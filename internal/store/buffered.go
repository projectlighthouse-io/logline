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
	mutex     sync.Mutex
	buffer    []domain.LogEntry
	batchSize int
	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
}

func NewBufferedStore(store *Store, batchSize int, interval time.Duration) *BufferedStore {
	bufferedStore := &BufferedStore{
		store:     store,
		buffer:    make([]domain.LogEntry, 0, batchSize),
		batchSize: batchSize,
		interval:  interval,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go bufferedStore.flushLoop()
	return bufferedStore
}

func (bs *BufferedStore) Add(entry domain.LogEntry) {
	bs.mutex.Lock()
	bs.buffer = append(bs.buffer, entry)
	full := len(bs.buffer) >= bs.batchSize
	bs.mutex.Unlock()

	if full {
		bs.flush()
	}
}

func (bs *BufferedStore) flush() {
	bs.mutex.Lock()
	if len(bs.buffer) == 0 {
		bs.mutex.Unlock()
		return
	}

	entries := bs.buffer
	bs.buffer = make([]domain.LogEntry, 0, bs.batchSize)
	bs.mutex.Unlock()

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
	defer close(bs.done)

	ticker := time.NewTicker(bs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bs.flush()
		case <-bs.stop:
			bs.flush()
			return
		}
	}
}

func (bs *BufferedStore) Close() {
	close(bs.stop)
	<-bs.done
}
