package server

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"logline/internal/store"
)

var templateFuncs = template.FuncMap{
	"formatTime": func(t time.Time) string {
		return t.Format("2006-01-02 15:04:05")
	},
	"levelClass": func(level string) string {
		switch level {
		case "error", "fatal":
			return "bg-red-100 text-red-700"
		case "warn":
			return "bg-yellow-100 text-yellow-700"
		case "info":
			return "bg-blue-100 text-blue-700"
		case "debug":
			return "bg-gray-100 text-gray-700"
		default:
			return "bg-gray-100 text-gray-700"
		}
	},
	"add": func(a, b int) int {
		return a + b
	},
	"queryString": func(f store.LogFilter, cursor int64) string {
		v := url.Values{}
		if f.Level != "" {
			v.Set("level", f.Level)
		}
		if f.Service != "" {
			v.Set("service", f.Service)
		}
		if f.Query != "" {
			v.Set("q", f.Query)
		}
		if !f.From.IsZero() {
			v.Set("from", f.FromDate())
		}
		if !f.To.IsZero() {
			v.Set("to", f.ToDate())
		}
		if cursor > 0 {
			v.Set("cursor", strconv.FormatInt(cursor, 10))
		}
		if encoded := v.Encode(); encoded != "" {
			return "?" + encoded
		}
		return ""
	},
}

func newTemplateCache(dir string) map[string]*template.Template {
	cache := map[string]*template.Template{}

	// skip if templates directory doesn't exist (e.g. during tests)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return cache
	}

	pages := []string{"logs.html", "login.html"}

	for _, page := range pages {
		name := filepath.Base(page)

		t, err := template.New("base.html").Funcs(templateFuncs).ParseFiles(
			filepath.Join(dir, "base.html"),
			filepath.Join(dir, page),
		)
		if err != nil {
			log.Fatalf("parsing template %s: %v", name, err)
		}

		cache[name] = t
	}

	return cache
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	t, ok := s.templates[page]
	if !ok {
		s.logger.Error("template not found", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		s.logger.Error("rendering template", "page", page, "error", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, buf.String())
}
