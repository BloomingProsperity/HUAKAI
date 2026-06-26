// 包 paymenthttp 暴露用户充值与 provider webhook 的 HTTP 路由。
//
// 本包只处理 HTTP 边界、provider 验签和 tenant 反查；真实入账、订单状态机、
// 幂等和审计仍由 internal/payment 承担。
package paymenthttp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const (
	HeaderWebhookTimestamp = "X-Huakai-Payment-Timestamp"
	HeaderWebhookSignature = "X-Huakai-Payment-Signature"

	defaultTimestampWindow = 5 * time.Minute
	externalTradePrefix    = "rech_t"
)

var (
	ErrWebhookVerificationFailed = errors.New("paymenthttp: webhook verification failed")
	ErrMockProviderForbidden     = errors.New("paymenthttp: mock provider forbidden in production")
)

type PaymentProvider interface {
	VerifyWebhook(rawBody []byte, headers http.Header, secret string) (payment.VerifiedCallback, error)
}

type ProviderBinding struct {
	Provider PaymentProvider
	Secret   string
}

type ProviderRegistryConfig struct {
	HMACSecrets map[string]string
	EnableMock  bool
	ReleaseMode string
}

type HMACProvider struct {
	clock           func() time.Time
	timestampWindow time.Duration
}

type HMACOption func(*HMACProvider)

func WithClock(clock func() time.Time) HMACOption {
	return func(p *HMACProvider) {
		if clock != nil {
			p.clock = clock
		}
	}
}

func WithTimestampWindow(window time.Duration) HMACOption {
	return func(p *HMACProvider) {
		if window > 0 {
			p.timestampWindow = window
		}
	}
}

func NewHMACProvider(opts ...HMACOption) *HMACProvider {
	p := &HMACProvider{
		clock:           func() time.Time { return time.Now().UTC() },
		timestampWindow: defaultTimestampWindow,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.clock == nil {
		p.clock = func() time.Time { return time.Now().UTC() }
	}
	if p.timestampWindow <= 0 {
		p.timestampWindow = defaultTimestampWindow
	}
	return p
}

func (p *HMACProvider) VerifyWebhook(rawBody []byte, headers http.Header, secret string) (payment.VerifiedCallback, error) {
	secret = strings.TrimSpace(secret)
	if len(rawBody) == 0 || secret == "" {
		return payment.VerifiedCallback{}, ErrWebhookVerificationFailed
	}
	rawTS := strings.TrimSpace(headers.Get(HeaderWebhookTimestamp))
	rawSig := strings.TrimSpace(headers.Get(HeaderWebhookSignature))
	if rawTS == "" || rawSig == "" {
		return payment.VerifiedCallback{}, ErrWebhookVerificationFailed
	}
	ts, err := strconv.ParseInt(rawTS, 10, 64)
	if err != nil {
		return payment.VerifiedCallback{}, ErrWebhookVerificationFailed
	}
	eventTime := time.Unix(ts, 0).UTC()
	now := p.clock().UTC()
	if eventTime.Before(now.Add(-p.timestampWindow)) || eventTime.After(now.Add(p.timestampWindow)) {
		return payment.VerifiedCallback{}, ErrWebhookVerificationFailed
	}

	want := hmacSignature(rawTS, rawBody, secret)
	got := strings.TrimPrefix(strings.ToLower(rawSig), "sha256=")
	if !hmac.Equal([]byte(got), []byte(want)) {
		return payment.VerifiedCallback{}, ErrWebhookVerificationFailed
	}
	cb, err := parseWebhookBody(rawBody)
	if err != nil {
		return payment.VerifiedCallback{}, err
	}
	cb.Timestamp = eventTime
	return cb, nil
}

type mockProvider struct{}

func (mockProvider) VerifyWebhook(rawBody []byte, _ http.Header, _ string) (payment.VerifiedCallback, error) {
	cb, err := parseWebhookBody(rawBody)
	if err != nil {
		return payment.VerifiedCallback{}, err
	}
	if cb.Timestamp.IsZero() {
		cb.Timestamp = time.Now().UTC()
	}
	return cb, nil
}

func BuildProviderBindings(cfg ProviderRegistryConfig) (map[string]ProviderBinding, error) {
	bindings := map[string]ProviderBinding{}
	production := strings.EqualFold(strings.TrimSpace(cfg.ReleaseMode), "production")
	for provider, secret := range cfg.HMACSecrets {
		name := normalizeProviderName(provider)
		if name == "" || strings.TrimSpace(secret) == "" {
			continue
		}
		if production && name == "mock" {
			return nil, ErrMockProviderForbidden
		}
		bindings[name] = ProviderBinding{Provider: NewHMACProvider(), Secret: strings.TrimSpace(secret)}
	}
	if cfg.EnableMock {
		if production {
			return nil, ErrMockProviderForbidden
		}
		bindings["mock"] = ProviderBinding{Provider: mockProvider{}, Secret: "dev-mock-only"}
	}
	return bindings, nil
}

type providerWebhookBody struct {
	TenantID        int64           `json:"tenant_id,omitempty"`
	Provider        string          `json:"provider"`
	ExternalTradeNo string          `json:"external_trade_no"`
	ProviderEventID string          `json:"provider_event_id"`
	EventID         string          `json:"event_id,omitempty"`
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	PaidAt          string          `json:"paid_at,omitempty"`
}

func parseWebhookBody(rawBody []byte) (payment.VerifiedCallback, error) {
	var body providerWebhookBody
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return payment.VerifiedCallback{}, err
	}
	eventID := strings.TrimSpace(body.ProviderEventID)
	if eventID == "" {
		eventID = strings.TrimSpace(body.EventID)
	}
	ts := time.Time{}
	if strings.TrimSpace(body.PaidAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(body.PaidAt))
		if err != nil {
			return payment.VerifiedCallback{}, err
		}
		ts = parsed.UTC()
	}
	return payment.VerifiedCallback{
		Provider:        normalizeProviderName(body.Provider),
		ExternalTradeNo: strings.TrimSpace(body.ExternalTradeNo),
		ProviderEventID: eventID,
		PaidAmount:      body.Amount,
		CurrencyCode:    strings.ToUpper(strings.TrimSpace(body.Currency)),
		Timestamp:       ts,
	}, nil
}

func hmacSignature(timestamp string, rawBody []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

func externalTradeNoForTenant(tenantID int64, suffix string) string {
	return fmt.Sprintf("%s%d_%s", externalTradePrefix, tenantID, strings.TrimSpace(suffix))
}

func tenantIDFromExternalTradeNo(tradeNo string) (int64, bool) {
	tradeNo = strings.TrimSpace(tradeNo)
	if !strings.HasPrefix(tradeNo, externalTradePrefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(tradeNo, externalTradePrefix)
	sep := strings.IndexByte(rest, '_')
	if sep <= 0 {
		return 0, false
	}
	tenantID, err := strconv.ParseInt(rest[:sep], 10, 64)
	return tenantID, err == nil && tenantID > 0
}

func randomExternalTradeSuffix() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func normalizeProviderName(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
