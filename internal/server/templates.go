package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"logline/internal/store"
)

var templateFuncs = template.FuncMap{
	"formatTime": func(timestamp time.Time) string {
		return timestamp.Format("2006-01-02 15:04:05")
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
	"add": func(left, right int) int {
		return left + right
	},
	"queryString": func(filter store.LogFilter, cursor int64) string {
		values := url.Values{}
		if filter.Level != "" {
			values.Set("level", filter.Level)
		}
		if filter.Service != "" {
			values.Set("service", filter.Service)
		}
		if filter.Query != "" {
			values.Set("q", filter.Query)
		}
		if !filter.From.IsZero() {
			values.Set("from", filter.FromDate())
		}
		if !filter.To.IsZero() {
			values.Set("to", filter.ToDate())
		}
		if cursor > 0 {
			values.Set("cursor", strconv.FormatInt(cursor, 10))
		}
		if encoded := values.Encode(); encoded != "" {
			return "?" + encoded
		}
		return ""
	},
}

func newTemplateCache(fsys fs.FS) map[string]*template.Template {
	cache := map[string]*template.Template{}

	pages := []string{"logs.html", "login.html", "dashboard.html"}

	for _, page := range pages {
		pageTemplate, err := template.New("base.html").Funcs(templateFuncs).ParseFS(fsys,
			"templates/base.html",
			"templates/"+page,
		)
		if err != nil {
			log.Fatalf("parsing template %s: %v", page, err)
		}

		cache[page] = pageTemplate
	}

	return cache
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	pageTemplate, ok := s.templates[page]
	if !ok {
		s.logger.Error("template not found", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := pageTemplate.ExecuteTemplate(&buf, "base.html", data); err != nil {
		s.logger.Error("rendering template", "page", page, "error", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, buf.String())
}
