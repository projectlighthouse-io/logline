package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLogFilter_DateOnlyToCoversWholeDay(t *testing.T) {
	r := httptest.NewRequest("GET", "/logs?from=2026-02-27&to=2026-02-27", nil)

	filter, err := parseLogFilter(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC); !filter.From.Equal(want) {
		t.Errorf("From = %v, want %v", filter.From, want)
	}

	lateThatDay := time.Date(2026, 2, 27, 23, 59, 59, 0, time.UTC)
	if filter.To.Before(lateThatDay) {
		t.Errorf("To = %v, want it to include %v", filter.To, lateThatDay)
	}
	if filter.ToDate() != "2026-02-27" {
		t.Errorf("ToDate() = %q, want the picked date back for the form", filter.ToDate())
	}
}

func TestParseLogFilter_RejectsBadInput(t *testing.T) {
	for _, rawQuery := range []string{"from=yesterday", "cursor=abc", "limit=x", "data=notjson"} {
		r := httptest.NewRequest("GET", "/logs?"+rawQuery, nil)
		if _, err := parseLogFilter(r); err == nil {
			t.Errorf("%s: expected an error", rawQuery)
		}
	}
}
