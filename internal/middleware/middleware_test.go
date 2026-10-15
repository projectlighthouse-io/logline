package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"logline/internal/middleware"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRequestID_SetsHeader(t *testing.T) {
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("expected X-Request-ID header, got empty")
	}

	if len(id) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(id), id)
	}
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec1 := httptest.NewRecorder()
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))

	firstID := rec1.Header().Get("X-Request-ID")
	secondID := rec2.Header().Get("X-Request-ID")

	if firstID == secondID {
		t.Errorf("expected unique IDs, both were %q", firstID)
	}
}

func TestRequestID_AvailableInContext(t *testing.T) {
	var captured string

	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = middleware.GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if captured == "" {
		t.Error("expected request ID in context, got empty")
	}

	if captured != rec.Header().Get("X-Request-ID") {
		t.Errorf("context ID %q != header ID %q", captured, rec.Header().Get("X-Request-ID"))
	}
}

func TestLogging_CapturesStatusCode(t *testing.T) {
	logger := testLogger()

	handler := middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

func TestLogging_DefaultsTo200(t *testing.T) {
	logger := testLogger()

	handler := middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRecovery_CatchesPanic(t *testing.T) {
	logger := testLogger()

	handler := middleware.Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecovery_PassesThroughNormally(t *testing.T) {
	logger := testLogger()

	handler := middleware.Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestChain_ExecutesInOrder(t *testing.T) {
	var order []string

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "outer-before")
			next.ServeHTTP(w, r)
			order = append(order, "outer-after")
		})
	}

	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "inner-before")
			next.ServeHTTP(w, r)
			order = append(order, "inner-after")
		})
	}

	handler := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
		}),
		outer, inner,
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	expected := []string{"outer-before", "inner-before", "handler", "inner-after", "outer-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(order), order)
	}

	for i, step := range expected {
		if order[i] != step {
			t.Errorf("step %d: expected %q, got %q", i, step, order[i])
		}
	}
}
