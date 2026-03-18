package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// CachingFileServer wraps an fs.FS in a file server that sets ETag and
// Cache-Control headers. if the client sends a matching If-None-Match,
// it returns 304 Not Modified.
func CachingFileServer(fsys fs.FS, etags map[string]string) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// strip leading slash to match etag map keys
		path := strings.TrimPrefix(r.URL.Path, "/")

		etag, ok := etags[path]
		if ok {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "public, max-age=86400")

			if match := r.Header.Get("If-None-Match"); match == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		fileServer.ServeHTTP(w, r)
	})
}
