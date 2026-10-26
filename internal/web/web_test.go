package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/service"
)

func TestTemplatesParse(t *testing.T) {
	svc := &service.Service{Loc: time.UTC, Cfg: config.Config{Timezone: "UTC"}}
	if _, err := New(svc, config.Config{Timezone: "UTC"}, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

func TestSkipAccessLog(t *testing.T) {
	tests := []struct {
		path   string
		status int
		skip   bool
	}{
		{"/healthz", http.StatusOK, true},
		{"/readyz", http.StatusOK, true},
		{"/healthz", http.StatusServiceUnavailable, false},
		{"/static/app.css", http.StatusOK, true},
		{"/static/app.js", http.StatusOK, true},
		{"/static/app.js", http.StatusNotFound, false},
		{"/favicon.ico", http.StatusNotFound, true},
		{"/_static/out/browser/serviceWorker.js", http.StatusNotFound, true},
		{"/transactions", http.StatusOK, false},
		{"/login", http.StatusSeeOther, false},
		{"/transactions/bad", http.StatusNotFound, false},
		{"/mcp", http.StatusOK, false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, tt.path, nil)
		if got := skipAccessLog(r, tt.status); got != tt.skip {
			t.Errorf("skipAccessLog(%q, %d) = %v, want %v", tt.path, tt.status, got, tt.skip)
		}
	}
}
