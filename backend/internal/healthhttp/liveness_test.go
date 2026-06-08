package healthhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	h := NewLivenessHandler()

	getRec := httptest.NewRecorder()
	h(getRec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status=%d want 200; mutation returning 500 makes this red", getRec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(getRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET /healthz body=%q: %v", getRec.Body.String(), err)
	}
	if body["status"] != "ok" {
		t.Fatalf("GET /healthz status field=%q want ok", body["status"])
	}

	headRec := httptest.NewRecorder()
	h(headRec, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD /healthz status=%d want 200", headRec.Code)
	}
	if headRec.Body.Len() != 0 {
		t.Fatalf("HEAD /healthz body length=%d want 0", headRec.Body.Len())
	}
}
