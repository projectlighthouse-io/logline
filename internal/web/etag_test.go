package web

import (
	ioFS "io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestComputeETags(t *testing.T) {
	testFS := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("hello")},
		"b.txt": &fstest.MapFile{Data: []byte("world")},
	}

	etags, err := ComputeETags(testFS)
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
	etags2, err := ComputeETags(testFS)
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
	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	etags, err := ComputeETags(testFS)
	if err != nil {
		t.Fatalf("ComputeETags: %v", err)
	}

	handler := CachingFileServer(testFS, etags)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("ETag"); got != etags["style.css"] {
		t.Errorf("ETag header = %q, want %q", got, etags["style.css"])
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestCachingFileServer_304OnMatch(t *testing.T) {
	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	etags, err := ComputeETags(testFS)
	if err != nil {
		t.Fatalf("ComputeETags: %v", err)
	}

	handler := CachingFileServer(testFS, etags)

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

func TestCachingFileServer_200OnMismatchedETag(t *testing.T) {
	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{color:red}")},
	}

	etags, err := ComputeETags(testFS)
	if err != nil {
		t.Fatalf("ComputeETags: %v", err)
	}

	handler := CachingFileServer(testFS, etags)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	req.Header.Set("If-None-Match", `"wrong-etag"`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on mismatched etag, got %d", rec.Code)
	}

	if rec.Body.Len() == 0 {
		t.Error("200 response should have body")
	}
}

func TestCachingFileServer_ContentType(t *testing.T) {
	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
		"app.js":    &fstest.MapFile{Data: []byte("console.log(1)")},
	}

	etags, _ := ComputeETags(testFS)
	handler := CachingFileServer(testFS, etags)

	tests := []struct {
		path     string
		wantType string
	}{
		{"/style.css", "text/css"},
		{"/app.js", "text/javascript"},
	}

	for _, testCase := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, testCase.path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", testCase.path, rec.Code)
			continue
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType == "" {
			t.Errorf("%s: missing Content-Type header", testCase.path)
		}
	}
}

func TestCachingFileServer_404OnMissing(t *testing.T) {
	testFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	etags, _ := ComputeETags(testFS)
	handler := CachingFileServer(testFS, etags)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing file, got %d", rec.Code)
	}
}

func TestFullPathResolution(t *testing.T) {
	// simulates the fs.Sub + StripPrefix chain
	inner := fstest.MapFS{
		"static/css/style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	subFS, err := ioFS.Sub(inner, "static")
	if err != nil {
		t.Fatal(err)
	}

	etags, err := ComputeETags(subFS)
	if err != nil {
		t.Fatal(err)
	}

	handler := http.StripPrefix("/static/", CachingFileServer(subFS, etags))

	// request through the full chain: /static/css/style.css -> css/style.css
	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Body.String() != "body{}" {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}

	// verify ETag is set
	if rec.Header().Get("ETag") == "" {
		t.Error("expected ETag header")
	}
}
