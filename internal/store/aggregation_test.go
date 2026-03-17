package store_test

import (
	"context"
	"testing"
	"time"

	"logline/internal/domain"
	"logline/internal/store"
)

func TestErrorsPerHour(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	now := time.Now().UTC()
	oneHourAgo := now.Add(-1 * time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)

	entries := []domain.LogEntry{
		{Level: "error", Message: "fail 1", Service: "api", Timestamp: now},
		{Level: "error", Message: "fail 2", Service: "api", Timestamp: now},
		{Level: "error", Message: "fail 3", Service: "api", Timestamp: oneHourAgo},
		{Level: "fatal", Message: "crash", Service: "api", Timestamp: twoHoursAgo},
		{Level: "info", Message: "ok", Service: "api", Timestamp: now},
	}

	for _, e := range entries {
		if err := s.InsertLog(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	buckets, err := s.ErrorsPerHour(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(buckets) == 0 {
		t.Fatal("expected at least one bucket")
	}

	// sum all counts -- should be 4 (3 errors + 1 fatal, info excluded)
	total := 0
	for _, b := range buckets {
		total += b.Count
	}

	if total != 4 {
		t.Errorf("expected total count 4, got %d", total)
	}
}

func TestVolumeByService(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	now := time.Now().UTC()

	entries := []domain.LogEntry{
		{Level: "info", Message: "a", Service: "gateway", Timestamp: now},
		{Level: "info", Message: "b", Service: "gateway", Timestamp: now},
		{Level: "info", Message: "c", Service: "gateway", Timestamp: now},
		{Level: "error", Message: "d", Service: "auth", Timestamp: now},
		{Level: "error", Message: "e", Service: "auth", Timestamp: now},
		{Level: "info", Message: "f", Service: "worker", Timestamp: now},
	}

	for _, e := range entries {
		if err := s.InsertLog(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.VolumeByService(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 services, got %d", len(results))
	}

	// ordered by count DESC
	if results[0].Service != "gateway" || results[0].Count != 3 {
		t.Errorf("expected gateway with count 3, got %s with %d", results[0].Service, results[0].Count)
	}

	if results[1].Service != "auth" || results[1].Count != 2 {
		t.Errorf("expected auth with count 2, got %s with %d", results[1].Service, results[1].Count)
	}

	if results[2].Service != "worker" || results[2].Count != 1 {
		t.Errorf("expected worker with count 1, got %s with %d", results[2].Service, results[2].Count)
	}
}

func TestTopErrors(t *testing.T) {
	db := setupTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	now := time.Now().UTC()

	entries := []domain.LogEntry{
		{Level: "error", Message: "connection timeout", Service: "api", Timestamp: now},
		{Level: "error", Message: "connection timeout", Service: "api", Timestamp: now},
		{Level: "error", Message: "connection timeout", Service: "api", Timestamp: now},
		{Level: "error", Message: "null pointer", Service: "api", Timestamp: now},
		{Level: "error", Message: "null pointer", Service: "api", Timestamp: now},
		{Level: "error", Message: "disk full", Service: "api", Timestamp: now},
		{Level: "info", Message: "all good", Service: "api", Timestamp: now},
	}

	for _, e := range entries {
		if err := s.InsertLog(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.TopErrors(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 error messages, got %d", len(results))
	}

	if results[0].Message != "connection timeout" || results[0].Count != 3 {
		t.Errorf("expected 'connection timeout' with count 3, got %q with %d", results[0].Message, results[0].Count)
	}

	if results[1].Message != "null pointer" || results[1].Count != 2 {
		t.Errorf("expected 'null pointer' with count 2, got %q with %d", results[1].Message, results[1].Count)
	}

	if results[2].Message != "disk full" || results[2].Count != 1 {
		t.Errorf("expected 'disk full' with count 1, got %q with %d", results[2].Message, results[2].Count)
	}
}

func TestFillHourGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)

	// sparse input: only 2 out of 24 hours
	buckets := []store.TimeBucket{
		{Time: now.Add(-5 * time.Hour), Count: 10},
		{Time: now.Add(-2 * time.Hour), Count: 7},
	}

	filled := store.FillHourGaps(buckets)

	if len(filled) != 24 {
		t.Fatalf("expected 24 entries, got %d", len(filled))
	}

	// verify the two non-zero entries are present
	nonZero := 0
	for _, b := range filled {
		if b.Count > 0 {
			nonZero++
		}
	}

	if nonZero != 2 {
		t.Errorf("expected 2 non-zero entries, got %d", nonZero)
	}

	// verify contiguous hours (each bucket is 1 hour after the previous)
	for i := 1; i < len(filled); i++ {
		diff := filled[i].Time.Sub(filled[i-1].Time)
		if diff != time.Hour {
			t.Errorf("gap between bucket %d and %d is %v, expected 1h", i-1, i, diff)
		}
	}

	// verify the last bucket is the current hour
	if !filled[23].Time.Equal(now) {
		t.Errorf("last bucket time %v, expected %v", filled[23].Time, now)
	}
}
