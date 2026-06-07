package mediatask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPProviderUsesInjectedClientAndMapsSubmitPoll(t *testing.T) {
	// Mutation: replace the injected server client with http.DefaultClient or
	// hard-code paths; this injected transport provider must fail.
	var submitSeen bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/tasks":
			submitSeen = true
			var req SubmitReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode submit: %v", err)
			}
			if req.TaskID != 55 || req.RequestID != "req-55" || req.TaskType != "image_generation" {
				t.Fatalf("submit req=%+v", req)
			}
			body = map[string]string{"provider_task_id": "up-55"}
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/up-55":
			body = PollResult{
				Status:      StatusSucceeded,
				Progress:    100,
				ActualCents: 77,
				Result:      json.RawMessage(`{"url":"https://cdn.example/55.png"}`),
			}
		default:
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(raw))),
			Request:    r,
		}, nil
	})}

	provider := NewHTTPProvider("http://provider.test", client)
	providerTaskID, err := provider.Submit(context.Background(), SubmitReq{
		TaskID:      55,
		RequestID:   "req-55",
		TaskType:    "image_generation",
		InputParams: json.RawMessage(`{"prompt":"a clean test"}`),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !submitSeen || providerTaskID != "up-55" {
		t.Fatalf("submitSeen=%v providerTaskID=%q", submitSeen, providerTaskID)
	}

	poll, err := provider.Poll(context.Background(), "up-55")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if poll.Status != StatusSucceeded || poll.ActualCents != 77 || !strings.Contains(string(poll.Result), "cdn.example") {
		t.Fatalf("poll=%+v", poll)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNoopProviderNeverReportsTerminalSuccess(t *testing.T) {
	provider := NewNoopProvider()
	id, err := provider.Submit(context.Background(), SubmitReq{TaskID: 1, RequestID: "req-1", TaskType: "image_generation"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if id == "" {
		t.Fatal("noop provider returned empty provider task id")
	}
	poll, err := provider.Poll(context.Background(), id)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if poll.Status != StatusInProgress {
		t.Fatalf("noop poll status=%q want in_progress", poll.Status)
	}
}

func TestHTTPProviderRegistryAcceptsMidjourneyAlias(t *testing.T) {
	// MUTATION: leave the registry hard-coded to provider name "http"; MJ tasks
	// translated with Provider=midjourney cannot reuse the async HTTP relay.
	registry := NewHTTPProviderRegistry(StaticConfigSource{Config: Config{
		Enabled: true, ProviderBaseURL: "http://provider.example",
		DefaultEstimatedCents: map[string]int64{
			"mj_imagine": 123,
		},
	}}, http.DefaultClient)

	if _, ok, err := registry.Provider(context.Background(), "midjourney"); err != nil || !ok {
		t.Fatalf("Provider(midjourney) ok=%v err=%v want available", ok, err)
	}
}

func TestHTTPProviderRegistryAcceptsSunoAlias(t *testing.T) {
	// MUTATION: leave the registry hard-coded to provider names "http" and
	// "midjourney"; Suno tasks translated with Provider=suno cannot reuse the
	// async HTTP relay.
	registry := NewHTTPProviderRegistry(StaticConfigSource{Config: Config{
		Enabled: true, ProviderBaseURL: "http://provider.example",
		DefaultEstimatedCents: map[string]int64{
			"suno_generate": 123,
		},
	}}, http.DefaultClient)

	if _, ok, err := registry.Provider(context.Background(), "suno"); err != nil || !ok {
		t.Fatalf("Provider(suno) ok=%v err=%v want available", ok, err)
	}
}
