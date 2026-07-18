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

// 变异:记录 RequestURI 或 URL.String() 而非 URL.Path -> query 里的密钥泄出 -> 变红。
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
	for key, want := range map[string]any{
		"log_category": "access", "event_type": "http.request_completed",
		"result": "success", "error_class": "none", "error_code": "none",
	} {
		if fields[key] != want {
			t.Fatalf("统一字段 %s = %#v, want %#v", key, fields[key], want)
		}
	}
	for _, forbidden := range []string{"secret=abc", "query", "request_uri", "remote_ip", "authorization"} {
		if containsFieldValue(fields, forbidden) {
			t.Fatalf("access log leaked forbidden value/key %q in fields %#v", forbidden, fields)
		}
	}
}

func TestClassifyHTTPResult(t *testing.T) {
	tests := []struct {
		status     int
		result     string
		errorClass string
		retryable  bool
	}{
		{http.StatusOK, "success", "none", false},
		{http.StatusUnauthorized, "denied", "authentication", false},
		{http.StatusForbidden, "denied", "authorization", false},
		{http.StatusPaymentRequired, "denied", "insufficient_balance", false},
		{http.StatusConflict, "client_failure", "conflict", false},
		{http.StatusTooManyRequests, "denied", "rate_limit", true},
		{http.StatusGatewayTimeout, "timeout", "timeout", true},
		{http.StatusBadGateway, "server_failure", "dependency", true},
		{http.StatusInternalServerError, "server_failure", "unknown", true},
		{http.StatusNotImplemented, "server_failure", "unknown", false},
	}
	for _, test := range tests {
		result, errorClass, errorCode, retryable := classifyHTTPResult(test.status)
		if result != test.result || errorClass != test.errorClass || retryable != test.retryable ||
			errorCode == "" {
			t.Fatalf("status=%d got=(%s,%s,%s,%v)", test.status, result, errorClass, errorCode, retryable)
		}
	}
}

func TestAccessLogLevelSeparatesClientAndServerFailures(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		handler := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}

	entries := recorded.All()
	if len(entries) != 4 {
		t.Fatalf("log entries = %d, want 4", len(entries))
	}
	for i, entry := range entries[:3] {
		if entry.Level != zap.InfoLevel {
			t.Fatalf("第 %d 条客户端失败级别 = %s, want info", i, entry.Level)
		}
		if got := entry.ContextMap()["result"]; got == "success" {
			t.Fatalf("第 %d 条客户端失败不能标成 success", i)
		}
	}
	if entries[3].Level != zap.ErrorLevel {
		t.Fatalf("服务端失败级别 = %s, want error", entries[3].Level)
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
