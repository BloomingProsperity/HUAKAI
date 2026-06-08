package accesslog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// MUTATION: log RequestURI or URL.String() instead of URL.Path -> query secret appears -> RED.
func TestAccessLogNoQuery(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(Middleware(logger))
	r.Get("/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/x?secret=abc", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
	}
	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if got := fields["path"]; got != "/x" {
		t.Fatalf("logged path = %v, want /x", got)
	}
	for _, forbidden := range []string{"secret=abc", "query", "request_uri", "remote_ip", "authorization"} {
		if containsFieldValue(fields, forbidden) {
			t.Fatalf("access log leaked forbidden value/key %q in fields %#v", forbidden, fields)
		}
	}
}

func containsFieldValue(fields map[string]any, needle string) bool {
	for key, value := range fields {
		if strings.Contains(key, needle) {
			return true
		}
		if s, ok := value.(string); ok && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
