package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactLogPath(t *testing.T) {
	token := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if got := redactLogPath("/api/v1/s/" + token); got != "/api/v1/s/[redacted]" {
		t.Fatalf("share path not redacted: %q", got)
	}
	if got := redactLogPath("/api/v1/diagram"); got != "/api/v1/diagram" {
		t.Fatalf("non-share path changed: %q", got)
	}
	if got := redactLogPath("/api/v1/s/"); got != "/api/v1/s/" {
		t.Fatalf("empty token segment changed: %q", got)
	}
}

func TestShareReadRequestLogRedactsToken(t *testing.T) {
	token := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	var buf bytes.Buffer
	handler := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(handler)
	defer slog.SetDefault(prev)

	s := &Server{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.withLogging(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/s/"+token, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := buf.String()
	if strings.Contains(out, token) {
		t.Fatalf("request log contains raw share token: %q", out)
	}
	if !strings.Contains(out, "/api/v1/s/[redacted]") {
		t.Fatalf("request log missing redacted share path: %q", out)
	}
}
