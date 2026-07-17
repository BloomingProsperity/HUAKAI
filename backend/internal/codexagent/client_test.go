package codexagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistrationClientRefusesRedirectAndDoesNotReplaySignature(t *testing.T) {
	var redirected atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer sink.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := newRegistrationClient(origin.Client(), nil)
	client.baseURL = origin.URL
	client.now = func() time.Time { return time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC) }
	if _, err := client.register(t.Context(), 7, identityMaterial{RuntimeID: "runtime", privateKey: privateKey}); err == nil {
		t.Fatal("307 被当作成功")
	}
	if redirected.Load() != 0 {
		t.Fatalf("签名请求被重放到重定向目标 %d 次", redirected.Load())
	}
}

func TestRegistrationClientAcceptsClearTaskResponse(t *testing.T) {
	requestShape := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/runtime/task/register" || r.Method != http.MethodPost {
			requestShape <- r.Method + " " + r.URL.Path
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task-1"}`))
	}))
	defer server.Close()
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	client := newRegistrationClient(server.Client(), nil)
	client.baseURL = server.URL
	got, err := client.register(t.Context(), 7, identityMaterial{RuntimeID: "runtime", privateKey: privateKey})
	if err != nil || got != "task-1" {
		t.Fatalf("task=%q err=%v", got, err)
	}
	select {
	case got := <-requestShape:
		t.Fatalf("request=%s", got)
	default:
	}
}
