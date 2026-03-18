package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestComputeETags(t *testing.T) {
	fs := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("hello")},
		"b.txt": &fstest.MapFile{Data: []byte("world")},
	}

	etags, err := ComputeETags(fs)
	if err != nil {
		t.Fatalf("ComputeETags returned error: %v", err)
	}

	if len(etags) != 2 {
		t.Fatalf("expected 2 etags, got %d", len(etags))
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		tag, ok := etags[name]
		if !ok {
			t.Errorf("missing etag for %s", name)
			continue
		}

		// etags must be quoted per HTTP spec
		if tag[0] != '"' || tag[len(tag)-1] != '"' {
			t.Errorf("etag for %s is not quoted: %s", name, tag)
		}
	}

	// deterministic: computing again should produce same values
	etags2, err := ComputeETags(fs)
	if err != nil {
		t.Fatalf("second ComputeETags returned error: %v", err)
	}

	for name, tag := range etags {
		if etags2[name] != tag {
			t.Errorf("etag for %s not deterministic: %s vs %s", name, tag, etags2[name])
		}
	}

	// different content produces different etags
	if etags["a.txt"] == etags["b.txt"] {
		t.Error("different files should have different etags")
	}
}

func TestCachingFileServer_SetsHeaders(t *testing.T) {
	fs := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	etags, err := ComputeETags(fs)
	if err != nil {
		t.Fatalf("ComputeETags: %v", err)
	}

	handler := CachingFileServer(fs, etags)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("ETag"); got != etags["style.css"] {
		t.Errorf("ETag header = %q, want %q", got, etags["style.css"])
	}

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want %q", got, "public, max-age=86400")
	}
}

func TestCachingFileServer_304OnMatch(t *testing.T) {
	fs := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	etags, err := ComputeETags(fs)
	if err != nil {
		t.Fatalf("ComputeETags: %v", err)
	}

	handler := CachingFileServer(fs, etags)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	req.Header.Set("If-None-Match", etags["style.css"])
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec.Code)
	}

	if rec.Body.Len() != 0 {
		t.Error("304 response should have empty body")
	}
}
