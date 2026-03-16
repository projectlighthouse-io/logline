package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"logline/internal/auth"
	"logline/internal/middleware"
)

func makeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func requestWithKey(prefix string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	ctx := context.WithValue(req.Context(), auth.ApiKeyContextKey, &auth.ApiKey{
		KeyPrefix: prefix,
	})
	return req.WithContext(ctx)
}

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	store := middleware.NewRateLimiterStore(100, 5, 10*time.Minute)
	handler := middleware.RateLimit(store)(makeHandler())

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithKey("test1234"))

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
}

func TestRateLimit_RejectsOverBurst(t *testing.T) {
	store := middleware.NewRateLimiterStore(1, 3, 10*time.Minute)
	handler := middleware.RateLimit(store)(makeHandler())

	got429 := false
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithKey("test1234"))

		if rec.Code == http.StatusTooManyRequests {
			got429 = true
		}
	}

	if !got429 {
		t.Error("expected at least one 429 response after exceeding burst")
	}
}

func TestRateLimit_RetryAfterHeader(t *testing.T) {
	store := middleware.NewRateLimiterStore(1, 1, 10*time.Minute)
	handler := middleware.RateLimit(store)(makeHandler())

	// exhaust the single token
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey("test1234"))

	// this one should be rate limited
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey("test1234"))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestRateLimit_PerKeyIsolation(t *testing.T) {
	store := middleware.NewRateLimiterStore(1, 2, 10*time.Minute)
	handler := middleware.RateLimit(store)(makeHandler())

	// exhaust key A
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithKey("keyAAAA"))
	}

	// key B should still work
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey("keyBBBBB"))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for isolated key, got %d", rec.Code)
	}
}

func TestRateLimit_NoApiKey_Returns401(t *testing.T) {
	store := middleware.NewRateLimiterStore(100, 200, 10*time.Minute)
	handler := middleware.RateLimit(store)(makeHandler())

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
