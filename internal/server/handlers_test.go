package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"logline/internal/config"
)

func newTestServer() *Server {
	return New(config.Config{
		Port:     4000,
		Env:      "development",
		LogLevel: "info",
	})
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestIngestValidEntry(t *testing.T) {
	srv := newTestServer()

	payload := `{"level":"error","message":"connection refused","service":"auth","timestamp":"2026-02-27T14:30:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestIngestWrongContentType(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", rec.Code)
	}
}

func TestIngestEmptyBody(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestInvalidJSON(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("definitely not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestMissingRequiredFields(t *testing.T) {
	srv := newTestServer()

	payload := `{"level":"error"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestIngestInvalidLevel(t *testing.T) {
	srv := newTestServer()

	payload := `{"level":"critical","message":"test","service":"api","timestamp":"2026-02-27T14:30:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestWrongMethodReturns405(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}
