package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"logline/internal/middleware"
)

func TestCORS_AllowedOrigin(t *testing.T) {
	handler := middleware.CORS([]string{"https://example.com"})(makeHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("expected Access-Control-Allow-Origin to be https://example.com, got %q",
			rec.Header().Get("Access-Control-Allow-Origin"))
	}

	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Access-Control-Allow-Methods header")
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	handler := middleware.CORS([]string{"https://example.com"})(makeHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS headers for disallowed origin, got %q",
			rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_PreflightReturns204(t *testing.T) {
	handler := middleware.CORS([]string{"https://example.com"})(makeHandler())

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
}

func TestCORS_NoOriginHeader(t *testing.T) {
	handler := middleware.CORS([]string{"https://example.com"})(makeHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS headers when Origin is missing")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 passthrough, got %d", rec.Code)
	}
}
