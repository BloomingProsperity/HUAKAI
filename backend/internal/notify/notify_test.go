package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/shopspring/decimal"
)

func TestLowBalanceWebhookSendsSignedOnceWithinLimit(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	store := fakeStore{settings: Settings{
		TenantID:         7,
		UserID:           42,
		NotifyType:       TypeWebhook,
		WebhookURL:       "https://hooks.example.test/low-balance",
		WebhookSecret:    "signing-secret",
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	}}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("3.00000000"), 9001); err != nil {
		t.Fatalf("NotifyLowBalance first call: %v", err)
	}
	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("2.00000000"), 9002); err != nil {
		t.Fatalf("NotifyLowBalance repeated call inside window: %v", err)
	}

	reqs := httpCalls.Requests()
	if len(reqs) != 1 {
		t.Fatalf("webhook calls=%d want 1; MUTATION: removing limiter should make this 2", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.URL.String() != "https://hooks.example.test/low-balance" {
		t.Fatalf("webhook request=%s %s", req.Method, req.URL.String())
	}
	wantSig := webhookSignature("signing-secret", req.Body)
	if got := req.Header.Get("X-HUAKAI-Notification-Signature"); got != wantSig {
		t.Fatalf("signature=%q want %q; MUTATION: signing the wrong bytes or skipping HMAC must fail", got, wantSig)
	}
	if strings.Contains(string(req.Body), "signing-secret") {
		t.Fatalf("payload leaked webhook secret: %s", string(req.Body))
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["event_type"] != EventLowBalance {
		t.Fatalf("event_type=%v want %s", payload["event_type"], EventLowBalance)
	}
}

func TestWebhookSSRFBlockedBeforeHTTPDo(t *testing.T) {
	cases := []string{
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.1/hook",
	}
	for _, rawURL := range cases {
		t.Run(rawURL, func(t *testing.T) {
			store := fakeStore{settings: Settings{
				TenantID:         7,
				UserID:           42,
				NotifyType:       TypeWebhook,
				WebhookURL:       rawURL,
				WebhookSecret:    "secret",
				BalanceThreshold: decimal.RequireFromString("10.00000000"),
			}}
			httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
			notifier := NewNotifier(Config{
				Store:      store,
				HTTPClient: &http.Client{Transport: httpCalls},
				Now:        fixedNow,
			})

			err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), 11)
			if !errors.Is(err, ErrUnsafeEndpoint) {
				t.Fatalf("error=%v want ErrUnsafeEndpoint", err)
			}
			if got := len(httpCalls.Requests()); got != 0 {
				t.Fatalf("http calls=%d want 0; MUTATION: removing SSRF preflight should call the outbound client", got)
			}
		})
	}
}

func TestEmailHeaderInjectionRejectedBeforeSender(t *testing.T) {
	store := fakeStore{settings: Settings{
		TenantID:          7,
		UserID:            42,
		NotifyType:        TypeEmail,
		NotificationEmail: "victim@example.test\r\nBcc: attacker@example.test",
		BalanceThreshold:  decimal.RequireFromString("10.00000000"),
	}}
	sender := &recordingEmailSender{}
	notifier := NewNotifier(Config{
		Store:       store,
		EmailSender: sender,
		Now:         fixedNow,
	})

	err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), 11)
	if !errors.Is(err, ErrHeaderInjection) {
		t.Fatalf("error=%v want ErrHeaderInjection", err)
	}
	if got := sender.Count(); got != 0 {
		t.Fatalf("email sends=%d want 0; MUTATION: stripping CRLF and continuing would call sender", got)
	}
}

func TestSettlerNotificationFailureDoesNotBlockSettle(t *testing.T) {
	next := &fakeSettler{result: &billing.SettleResult{
		TenantID:       7,
		UserID:         42,
		NewUserBalance: decimal.RequireFromString("1.00000000"),
		BillingEventID: 9001,
	}}
	store := fakeStore{settings: Settings{
		TenantID:         7,
		UserID:           42,
		NotifyType:       TypeWebhook,
		WebhookURL:       "https://hooks.example.test/low-balance",
		WebhookSecret:    "secret",
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	}}
	notifier := NewNotifier(Config{
		Store:      store,
		HTTPClient: &http.Client{Transport: failingRoundTripper{}},
		Now:        fixedNow,
	})
	settler := NewSettler(next, notifier, WithSettlerAsync(func(fn func()) { fn() }))

	res, err := settler.Settle(context.Background(), billing.SettleRequest{TenantID: 7, UserID: 42})
	if err != nil {
		t.Fatalf("Settle propagated notification failure: %v; MUTATION: blocking on delivery errors should fail here", err)
	}
	if res == nil || !res.NewUserBalance.Equal(decimal.RequireFromString("1.00000000")) {
		t.Fatalf("settle result=%+v", res)
	}
	if next.settleCalls != 1 {
		t.Fatalf("underlying settle calls=%d want 1", next.settleCalls)
	}
}

func TestDefaultNoneSkipsDelivery(t *testing.T) {
	store := fakeStore{settings: DefaultSettings(7, 42)}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	sender := &recordingEmailSender{}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		EmailSender: sender,
		Now:         fixedNow,
	})

	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), 11); err != nil {
		t.Fatalf("NotifyLowBalance default none: %v", err)
	}
	if got := len(httpCalls.Requests()); got != 0 {
		t.Fatalf("http calls=%d want 0", got)
	}
	if got := sender.Count(); got != 0 {
		t.Fatalf("email sends=%d want 0; MUTATION: defaulting to a real adapter should call a sender", got)
	}
}

func TestGotifyUsesConfiguredPriority(t *testing.T) {
	store := fakeStore{settings: Settings{
		TenantID:         7,
		UserID:           42,
		NotifyType:       TypeGotify,
		GotifyURL:        "https://gotify.example.test/message",
		GotifyToken:      "gotify-token",
		GotifyPriority:   8,
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	}}
	httpCalls := &recordingRoundTripper{status: http.StatusOK}
	notifier := NewNotifier(Config{
		Store:      store,
		HTTPClient: &http.Client{Transport: httpCalls},
		Now:        fixedNow,
	})

	if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), 11); err != nil {
		t.Fatalf("NotifyLowBalance gotify: %v", err)
	}

	reqs := httpCalls.Requests()
	if len(reqs) != 1 {
		t.Fatalf("gotify calls=%d want 1", len(reqs))
	}
	var payload map[string]any
	if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
		t.Fatalf("gotify payload json: %v", err)
	}
	if got := int(payload["priority"].(float64)); got != 8 {
		t.Fatalf("gotify priority=%d want 8; MUTATION: hardcoding priority=5 must fail here", got)
	}
}

func TestValidateSettingsRejectsGotifyPriorityOutOfRange(t *testing.T) {
	_, err := ValidateSettings(Settings{
		TenantID:         7,
		UserID:           42,
		NotifyType:       TypeGotify,
		GotifyURL:        "https://gotify.example.test/message",
		GotifyToken:      "gotify-token",
		GotifyPriority:   99,
		BalanceThreshold: decimal.RequireFromString("10.00000000"),
	})
	if !errors.Is(err, ErrInvalidSettings) || !strings.Contains(err.Error(), "gotify_priority") {
		t.Fatalf("error=%v want ErrInvalidSettings: gotify_priority; MUTATION: removing priority range validation must fail here", err)
	}
}

func TestScrubInactiveFieldsClearsGotifyPriorityOutsideGotify(t *testing.T) {
	settings := scrubInactiveFields(Settings{
		TenantID:          7,
		UserID:            42,
		NotifyType:        TypeEmail,
		NotificationEmail: "ops@example.test",
		GotifyURL:         "https://gotify.example.test/message",
		GotifyToken:       "gotify-token",
		GotifyPriority:    8,
		BalanceThreshold:  decimal.RequireFromString("10.00000000"),
	})
	if settings.GotifyPriority != 0 {
		t.Fatalf("gotify_priority=%d want 0; MUTATION: not scrubbing inactive Gotify priority should fail here", settings.GotifyPriority)
	}
}

func TestNotifyTypeNoneShortCircuitsRepeatDBReads(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	store := &countingStore{settings: DefaultSettings(7, 42)}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	notifier := NewNotifier(Config{
		Store:              store,
		HTTPClient:         &http.Client{Transport: httpCalls},
		Now:                func() time.Time { return now },
		NotifyTypeCacheTTL: time.Minute,
	})

	const settlements = 50
	for i := 0; i < settlements; i++ {
		if err := notifier.NotifyLowBalance(context.Background(), 7, 42, decimal.RequireFromString("1.00000000"), int64(i)); err != nil {
			t.Fatalf("NotifyLowBalance iteration %d: %v", i, err)
		}
	}

	if got := store.Calls(); got != 1 {
		t.Fatalf("GetSettings DB reads=%d want 1; MUTATION: dropping the notify_type short-circuit makes the hot path read the DB on every settlement (=%d)", got, settlements)
	}
	if got := len(httpCalls.Requests()); got != 0 {
		t.Fatalf("http calls=%d want 0 for notify_type=none", got)
	}
}

func TestNotifyAlertFiringDispatches(t *testing.T) {
	// 变异:在解析出生效设置后去掉 alert firing 的派发调用;则不会记录到任何出站请求。
	now := time.Date(2026, 6, 7, 9, 30, 0, 0, time.UTC)
	store := fakeStore{activeSettings: []Settings{
		{
			TenantID:      7,
			UserID:        42,
			NotifyType:    TypeWebhook,
			WebhookURL:    "https://hooks.example.test/alerts",
			WebhookSecret: "alert-secret",
		},
		{
			TenantID:   7,
			UserID:     43,
			NotifyType: TypeBark,
			BarkURL:    "https://bark.example.test/push",
			UpdatedBy:  "ops",
			UpdatedAt:  now,
		},
	}}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	err := notifier.NotifyAlertFiring(context.Background(), 7, AlertFiringInfo{
		RuleName:      "latency spike",
		Metric:        "gateway.latency_ms",
		Comparator:    "gte",
		Threshold:     100,
		Severity:      "critical",
		ObservedValue: 150,
		FiredAt:       now,
	})
	if err != nil {
		t.Fatalf("NotifyAlertFiring: %v", err)
	}

	reqs := httpCalls.Requests()
	if len(reqs) != 2 {
		t.Fatalf("alert firing calls=%d want 2 configured channels", len(reqs))
	}
	webhookReq := reqs[0]
	if webhookReq.URL.String() != "https://hooks.example.test/alerts" {
		t.Fatalf("webhook url=%s", webhookReq.URL.String())
	}
	if got := webhookReq.Header.Get("X-HUAKAI-Notification-Event"); got != EventAlertFiring {
		t.Fatalf("event header=%q want %q", got, EventAlertFiring)
	}
	if got := webhookReq.Header.Get("X-HUAKAI-Notification-Signature"); got != webhookSignature("alert-secret", webhookReq.Body) {
		t.Fatalf("webhook signature=%q does not match body", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(webhookReq.Body, &payload); err != nil {
		t.Fatalf("webhook payload json: %v", err)
	}
	if payload["event_type"] != EventAlertFiring || payload["rule_name"] != "latency spike" ||
		payload["metric"] != "gateway.latency_ms" || payload["severity"] != "critical" ||
		payload["observed_value"].(float64) != 150 {
		t.Fatalf("webhook payload=%v want alert firing fields", payload)
	}
	if barkReq := reqs[1]; barkReq.URL.String() != "https://bark.example.test/push" || barkReq.Header.Get("X-HUAKAI-Notification-Event") != EventAlertFiring {
		t.Fatalf("bark request=%s event=%q", barkReq.URL.String(), barkReq.Header.Get("X-HUAKAI-Notification-Event"))
	}
}

func TestNotifyProviderAccountDownBroadcastsToAllChannels(t *testing.T) {
	// 变异:把 webhook 的 EventType/header 设为 EventAlertFiring 而非
	// EventProviderAccountDown,会使 event-header 断言变红;本测试断言的是
	// 具体的新 wire event_type,而不只是 2xx。
	now := time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
	store := fakeStore{activeSettings: []Settings{
		{
			TenantID:      7,
			UserID:        42,
			NotifyType:    TypeWebhook,
			WebhookURL:    "https://hooks.example.test/account-down",
			WebhookSecret: "down-secret",
		},
		{
			TenantID:          7,
			UserID:            43,
			NotifyType:        TypeEmail,
			NotificationEmail: "ops@example.test",
			UpdatedBy:         "ops",
			UpdatedAt:         now,
		},
	}}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	sender := &recordingEmailSender{}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		EmailSender: sender,
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	err := notifier.NotifyProviderAccountDown(context.Background(), 7, ProviderAccountDownInfo{
		ProviderAccountID: 31,
		VendorName:        "anthropic",
		HealthState:       "revoked",
		Outcome:           "auth_expired",
		Severity:          "critical",
	})
	if err != nil {
		t.Fatalf("NotifyProviderAccountDown: %v", err)
	}

	reqs := httpCalls.Requests()
	if len(reqs) != 1 {
		t.Fatalf("webhook calls=%d want 1", len(reqs))
	}
	webhookReq := reqs[0]
	if webhookReq.URL.String() != "https://hooks.example.test/account-down" {
		t.Fatalf("webhook url=%s", webhookReq.URL.String())
	}
	if got := webhookReq.Header.Get("X-HUAKAI-Notification-Event"); got != EventProviderAccountDown {
		t.Fatalf("event header=%q want %q; MUTATION: emitting EventAlertFiring must fail here", got, EventProviderAccountDown)
	}
	if got := webhookReq.Header.Get("X-HUAKAI-Notification-Signature"); got != webhookSignature("down-secret", webhookReq.Body) {
		t.Fatalf("webhook signature=%q does not match body; MUTATION: skipping HMAC must fail", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(webhookReq.Body, &payload); err != nil {
		t.Fatalf("webhook payload json: %v", err)
	}
	if payload["event_type"] != EventProviderAccountDown ||
		payload["health_state"] != "revoked" ||
		payload["outcome"] != "auth_expired" ||
		payload["vendor_name"] != "anthropic" ||
		int64(payload["provider_account_id"].(float64)) != 31 {
		t.Fatalf("webhook payload=%v want provider account down fields", payload)
	}
	if got := sender.Count(); got != 1 {
		t.Fatalf("email sends=%d want 1; MUTATION: dropping the email channel must fail here", got)
	}
}

func TestNotifyProviderAccountDownRateLimited(t *testing.T) {
	// 变异:在 broadcast 中移除 limiter.Allow 检查,会让第二次调用
	// 再次发送,请求计数变成 2 → 变红。
	now := time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
	store := fakeStore{activeSettings: []Settings{{
		TenantID:      7,
		UserID:        42,
		NotifyType:    TypeWebhook,
		WebhookURL:    "https://hooks.example.test/account-down",
		WebhookSecret: "down-secret",
	}}}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	info := ProviderAccountDownInfo{ProviderAccountID: 31, HealthState: "revoked", Outcome: "auth_expired"}
	if err := notifier.NotifyProviderAccountDown(context.Background(), 7, info); err != nil {
		t.Fatalf("first NotifyProviderAccountDown: %v", err)
	}
	if err := notifier.NotifyProviderAccountDown(context.Background(), 7, info); err != nil {
		t.Fatalf("second NotifyProviderAccountDown within window: %v", err)
	}

	if got := len(httpCalls.Requests()); got != 1 {
		t.Fatalf("webhook calls=%d want 1; MUTATION: removing the limiter check makes this 2", got)
	}
}

func TestNotifyProviderAccountDownSkipsTypeNone(t *testing.T) {
	// 变异:在 broadcast 中去掉 TypeNone 的 continue,会尝试向一个
	// 已禁用的接收方投递 → 变红。
	now := time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
	store := fakeStore{activeSettings: []Settings{DefaultSettings(7, 42)}}
	httpCalls := &recordingRoundTripper{status: http.StatusNoContent}
	sender := &recordingEmailSender{}
	notifier := NewNotifier(Config{
		Store:       store,
		HTTPClient:  &http.Client{Transport: httpCalls},
		EmailSender: sender,
		Now:         func() time.Time { return now },
		RateLimiter: NewRateLimiter(time.Hour),
	})

	if err := notifier.NotifyProviderAccountDown(context.Background(), 7, ProviderAccountDownInfo{
		ProviderAccountID: 31,
		HealthState:       "revoked",
		Outcome:           "auth_expired",
	}); err != nil {
		t.Fatalf("NotifyProviderAccountDown: %v", err)
	}

	if got := len(httpCalls.Requests()); got != 0 {
		t.Fatalf("http calls=%d want 0 for notify_type=none", got)
	}
	if got := sender.Count(); got != 0 {
		t.Fatalf("email sends=%d want 0 for notify_type=none; MUTATION: dropping the TypeNone skip must fail here", got)
	}
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
}

type fakeStore struct {
	settings       Settings
	activeSettings []Settings
	err            error
}

func (s fakeStore) GetSettings(context.Context, int64, int64) (Settings, error) {
	if s.err != nil {
		return Settings{}, s.err
	}
	return s.settings, nil
}

func (s fakeStore) ListActiveSettings(context.Context, int64) ([]Settings, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]Settings, len(s.activeSettings))
	copy(out, s.activeSettings)
	return out, nil
}

func (s fakeStore) UpsertSettings(context.Context, Settings) (Settings, error) {
	if s.err != nil {
		return Settings{}, s.err
	}
	return s.settings, nil
}

type recordingRoundTripper struct {
	mu     sync.Mutex
	status int
	reqs   []recordedRequest
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	rt.mu.Lock()
	rt.reqs = append(rt.reqs, recordedRequest{
		Method: req.Method,
		URL:    req.URL,
		Header: req.Header.Clone(),
		Body:   body,
	})
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: rt.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (rt *recordingRoundTripper) Requests() []recordedRequest {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]recordedRequest, len(rt.reqs))
	copy(out, rt.reqs)
	return out
}

type recordedRequest struct {
	Method string
	URL    *url.URL
	Header http.Header
	Body   []byte
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("delivery down")
}

type recordingEmailSender struct {
	mu       sync.Mutex
	messages []mailinfra.Message
}

func (s *recordingEmailSender) SendTenantMessage(_ context.Context, _ int64, msg mailinfra.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	return nil
}

func (s *recordingEmailSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

type fakeSettler struct {
	result      *billing.SettleResult
	err         error
	settleCalls int
}

func (s *fakeSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	s.settleCalls++
	return s.result, s.err
}

func (s *fakeSettler) Abort(context.Context, int64, int64, string, string, int64, json.RawMessage) error {
	return nil
}

func (s *fakeSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *fakeSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

type countingStore struct {
	settings Settings
	err      error
	mu       sync.Mutex
	calls    int
}

func (s *countingStore) GetSettings(context.Context, int64, int64) (Settings, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.err != nil {
		return Settings{}, s.err
	}
	return s.settings, nil
}

func (s *countingStore) UpsertSettings(context.Context, Settings) (Settings, error) {
	if s.err != nil {
		return Settings{}, s.err
	}
	return s.settings, nil
}

func (s *countingStore) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}
