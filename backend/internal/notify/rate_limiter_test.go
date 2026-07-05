package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestLowBalanceDeliveryFailureReleasesRateLimitSlot(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	store := fakeStore{settings: Settings{
		TenantID:         7,
		UserID:           42,
		NotifyType:       TypeWebhook,
		WebhookURL:       "https://hooks.example.test/low-balance",
		WebhookSecret:    "signing-secret",
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	}}
	httpCalls := &sequenceRoundTripper{statuses: []int{http.StatusInternalServerError, http.StatusNoContent}}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("3.00000000"), 9001)
	if !errors.Is(err, ErrDeliveryFailed) {
		t.Fatalf("first delivery err=%v want ErrDeliveryFailed", err)
	}
	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("2.00000000"), 9002); err != nil {
		t.Fatalf("second delivery after released slot: %v", err)
	}
	if got := len(httpCalls.Requests()); got != 2 {
		t.Fatalf("webhook calls=%d want 2; MUTATION: not releasing failed limiter slot suppresses retry", got)
	}
}

func TestRateLimiterEvictsExpiredSlots(t *testing.T) {
	limiter := NewRateLimiter(time.Minute)
	base := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	if !limiter.Allow(7, 42, EventLowBalance, base) {
		t.Fatal("first slot should be allowed")
	}
	if limiter.Allow(7, 42, EventLowBalance, base.Add(30*time.Second)) {
		t.Fatal("same slot inside window should be blocked")
	}

	later := base.Add(2 * time.Minute)
	if !limiter.Allow(7, 43, EventLowBalance, later) {
		t.Fatal("different user after window should be allowed")
	}
	if _, ok := limiter.last[rateLimitKey(7, 42, EventLowBalance)]; ok {
		t.Fatalf("expired rate-limit slot was not evicted; MUTATION: removing eviction keeps stale map entries")
	}
}

type sequenceRoundTripper struct {
	mu       sync.Mutex
	statuses []int
	reqs     []recordedRequest
}

func (rt *sequenceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.reqs = append(rt.reqs, recordedRequest{
		Method: req.Method,
		URL:    req.URL,
		Header: req.Header.Clone(),
		Body:   body,
	})
	status := http.StatusNoContent
	if len(rt.statuses) > 0 {
		status = rt.statuses[0]
		rt.statuses = rt.statuses[1:]
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (rt *sequenceRoundTripper) Requests() []recordedRequest {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]recordedRequest, len(rt.reqs))
	copy(out, rt.reqs)
	return out
}
