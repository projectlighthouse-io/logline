package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"logline/internal/auth"
)

func TestDashboardLogs_ShowsSignedInUser(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/logs", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &auth.User{Email: "ada@example.com"})
	rec := httptest.NewRecorder()

	srv.handleDashboardLogs(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ada@example.com") {
		t.Error("expected the signed-in user's email in the nav")
	}
}
