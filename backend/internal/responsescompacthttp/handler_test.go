package responsescompacthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompactRejectsStreamingBeforeDelegating(t *testing.T) {
	called := false
	handler := NewCompactHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}), "/v1/responses")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact",
		strings.NewReader(`{"model":"m","input":"x","stream":true}`))

	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("delegate was called for stream:true compact request")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	var got struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error: %v body=%s", err, rec.Body.String())
	}
	if got.Error.Type != "invalid_request_error" {
		t.Fatalf("error.type=%q want invalid_request_error body=%s", got.Error.Type, rec.Body.String())
	}
	if got.Error.Message != "Streaming not supported for compact responses" {
		t.Fatalf("error.message=%q want compact streaming message", got.Error.Message)
	}
}

func TestCompactRemovesNonTrueStreamAndDelegatesCanonicalPath(t *testing.T) {
	var seenPath string
	var seenBody map[string]json.RawMessage
	handler := NewCompactHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("delegate decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}), "/backend-api/codex/responses")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses/compact",
		strings.NewReader(`{"model":"m","input":"x","stream":false}`))

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want delegate 202", rec.Code, rec.Body.String())
	}
	if seenPath != "/backend-api/codex/responses" {
		t.Fatalf("delegate path=%q want canonical codex responses path", seenPath)
	}
	if _, ok := seenBody["stream"]; ok {
		t.Fatalf("delegate body still has stream field: %#v", seenBody)
	}
	if string(seenBody["model"]) != `"m"` || string(seenBody["input"]) != `"x"` {
		t.Fatalf("delegate body=%#v lost model/input", seenBody)
	}
}
