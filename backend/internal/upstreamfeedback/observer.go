package upstreamfeedback

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const (
	defaultRefreshTimeout      = 30 * time.Second
	defaultRefreshDedupeWindow = 30 * time.Second
	refreshDedupePurgeLimit    = 1024
)

type ChannelHealth interface {
	ApplySignal(context.Context, channelhealth.Signal) (channelhealth.Record, error)
	ForceCooldown(context.Context, channelhealth.ChannelKey, time.Time, string) (channelhealth.Record, error)
}

type ModelCooldowns interface {
	RecordModelRateLimit(context.Context, rate.ModelCooldownInput) error
}

type CredentialHotRefresher interface {
	RefreshHotPath(context.Context, int64, int64, string) error
}

type AuthCooldown interface {
	OnRefreshResult(context.Context, int64, bool, bool)
}

type RecentRequests interface {
	Record(accountID int64, success bool)
}

type Dependencies struct {
	ChannelHealth          ChannelHealth
	ModelCooldowns         ModelCooldowns
	RateService            rate.Service
	CredentialHotRefresher CredentialHotRefresher
	AuthCooldown           AuthCooldown
	RecentRequests         RecentRequests
	Now                    func() time.Time
	RefreshTimeout         time.Duration
	RefreshDedupeWindow    time.Duration
}

type Observer struct {
	deps Dependencies

	refreshMu    sync.Mutex
	refreshUntil map[refreshKey]time.Time
}

type Attempt struct {
	TenantID       int64
	Account        provider.AccountInfo
	ProtocolFamily string
	ModelKey       string
	RequestID      string
	StartedAt      time.Time
}

type HTTPFailure struct {
	Decision       gateway.AttemptRetryDecision
	Classification gateway.Classification
}

type refreshKey struct {
	tenantID  int64
	accountID int64
}

func NewObserver(deps Dependencies) *Observer {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.RefreshTimeout <= 0 {
		deps.RefreshTimeout = defaultRefreshTimeout
	}
	if deps.RefreshDedupeWindow <= 0 {
		deps.RefreshDedupeWindow = defaultRefreshDedupeWindow
	}
	return &Observer{
		deps:         deps,
		refreshUntil: make(map[refreshKey]time.Time),
	}
}

func (o *Observer) ObserveHTTPError(ctx context.Context, attempt Attempt, statusCode int, headers http.Header, body []byte) HTTPFailure {
	providerName := classificationProvider(attempt)
	decision, classification, err := gateway.ClassifyAttemptHTTPError(statusCode, headers, body, providerName)
	if err != nil {
		classification, _ = gateway.Classify(statusCode, headers, body, providerName)
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  "upstream_error",
		}
		logInternal(ctx, attempt.RequestID, "upstream_error_classification_failed", err)
	}

	modelScoped := o.applyRateState(ctx, attempt, statusCode, headers, body, classification)
	if !modelScoped {
		o.recordSignal(
			ctx,
			attempt,
			gateway.SignalFromClassification(statusCode, classification),
			statusCode,
			rateLimitReset(classification, o.now()),
			gateway.AuthFailureClassFromClassification(classification),
		)
	}
	if decision.RefreshIntent == gateway.RefreshOAuthHotPath {
		o.triggerCredentialRefresh(attempt)
	}
	return HTTPFailure{Decision: decision, Classification: classification}
}

func (o *Observer) ObserveDispatchError(ctx context.Context, attempt Attempt, err error) gateway.AttemptRetryDecision {
	decision := gateway.ClassifyAttemptDispatchError(err)
	classification, _ := gateway.Classify(0, nil, []byte(errorText(err)), classificationProvider(attempt))
	class := dispatchSignalClass(err, decision, classification)
	if class != "" {
		o.recordSignal(ctx, attempt, class, 0, nil, gateway.AuthFailureClassFromClassification(classification))
	}
	if decision.RefreshIntent == gateway.RefreshOAuthHotPath {
		o.triggerCredentialRefresh(attempt)
	}
	return decision
}

func (o *Observer) ObserveChannelError(ctx context.Context, attempt Attempt, statusCode int) {
	o.recordSignal(ctx, attempt, channelhealth.SignalChannelError, statusCode, nil, authcooldown.ClassAmbiguous)
}

func (o *Observer) ObserveSuccess(ctx context.Context, attempt Attempt, statusCode int, headers http.Header) {
	if o == nil {
		return
	}
	if o.deps.RateService != nil &&
		attempt.Account.AccountID > 0 &&
		strings.EqualFold(strings.TrimSpace(attempt.Account.Platform), "anthropic") {
		if err := o.deps.RateService.UpdateSessionWindow(ctx, attempt.Account.AccountID, headers); err != nil {
			logInternal(ctx, attempt.RequestID, "session_window_update_failed", err)
		}
	}
	o.recordSignal(ctx, attempt, channelhealth.SignalSuccess, statusCode, nil, authcooldown.ClassAmbiguous)
}

func (o *Observer) applyRateState(
	ctx context.Context,
	attempt Attempt,
	statusCode int,
	headers http.Header,
	body []byte,
	classification gateway.Classification,
) bool {
	if o == nil || attempt.Account.AccountID <= 0 {
		return false
	}
	var (
		dec         rate.Decision
		hasDecision bool
	)
	if o.deps.RateService != nil {
		var err error
		dec, err = o.deps.RateService.HandleUpstreamError(ctx, attempt.Account.AccountID, statusCode, headers, body)
		if err != nil {
			logInternal(ctx, attempt.RequestID, "upstream_rate_cooldown_decision_failed", err)
		} else {
			hasDecision = true
			if dec.SuppressLocalState {
				return true
			}
		}
	}

	if statusCode == http.StatusNotFound {
		o.recordModelCooldown(ctx, attempt, statusCode, nil, rate.ReasonModelLimitExceeded)
		return false
	}
	if statusCode == http.StatusTooManyRequests && classification.Class == gateway.ErrorClassRateLimited {
		if hasDecision && dec.StateChange != rate.StateNoChange && dec.StateChange != rate.StateRateLimited {
			o.forceAccountCooldown(ctx, attempt, dec)
			return false
		}
		resetAt := rateLimitReset(classification, o.now())
		reason := rate.ReasonRateLimitRPM
		if hasDecision {
			if !dec.CooldownUntil.IsZero() {
				reset := dec.CooldownUntil
				resetAt = &reset
			}
			if dec.Reason != "" {
				reason = dec.Reason
			}
		}
		return o.recordModelCooldown(ctx, attempt, statusCode, resetAt, reason)
	}
	if hasDecision && accountCooldownCandidate(statusCode) {
		o.forceAccountCooldown(ctx, attempt, dec)
	}
	return false
}

func (o *Observer) forceAccountCooldown(ctx context.Context, attempt Attempt, dec rate.Decision) {
	if o == nil || o.deps.ChannelHealth == nil || dec.StateChange == rate.StateNoChange || dec.CooldownUntil.IsZero() {
		return
	}
	key, ok := healthKey(attempt)
	if !ok {
		return
	}
	if _, err := o.deps.ChannelHealth.ForceCooldown(ctx, key, dec.CooldownUntil, string(dec.Reason)); err != nil {
		logInternal(ctx, attempt.RequestID, "account_cooldown_write_failed", err)
	}
}

func (o *Observer) recordModelCooldown(
	ctx context.Context,
	attempt Attempt,
	statusCode int,
	resetAt *time.Time,
	reason rate.Reason,
) bool {
	if o == nil || o.deps.ModelCooldowns == nil ||
		attempt.TenantID <= 0 || attempt.Account.AccountID <= 0 ||
		strings.TrimSpace(attempt.ModelKey) == "" {
		return false
	}
	in := rate.ModelCooldownInput{
		TenantID:          attempt.TenantID,
		ProviderAccountID: attempt.Account.AccountID,
		ModelKey:          strings.TrimSpace(attempt.ModelKey),
		Reason:            reason,
		StatusCode:        statusCode,
		UpstreamRequestID: strings.TrimSpace(attempt.RequestID),
	}
	if resetAt != nil {
		in.ResetAt = resetAt.UTC()
	}
	if err := o.deps.ModelCooldowns.RecordModelRateLimit(ctx, in); err != nil {
		logInternal(ctx, attempt.RequestID, "model_rate_limit_record_failed", err)
		return false
	}
	return true
}

func (o *Observer) recordSignal(
	ctx context.Context,
	attempt Attempt,
	class channelhealth.SignalClass,
	statusCode int,
	resetAt *time.Time,
	authClass authcooldown.FailureClass,
) {
	if o == nil || class == "" {
		return
	}
	if o.deps.RecentRequests != nil && attempt.Account.AccountID > 0 && class != channelhealth.SignalAuthChallenge {
		o.deps.RecentRequests.Record(attempt.Account.AccountID, class == channelhealth.SignalSuccess)
	}
	if o.deps.ChannelHealth == nil {
		return
	}
	key, ok := healthKey(attempt)
	if !ok {
		return
	}
	latency := o.now().Sub(attempt.StartedAt)
	if attempt.StartedAt.IsZero() || latency < 0 {
		latency = 0
	}
	if _, err := o.deps.ChannelHealth.ApplySignal(ctx, channelhealth.Signal{
		Key:              key,
		Class:            class,
		StatusCode:       statusCode,
		LatencyMS:        latency.Milliseconds(),
		RequestID:        strings.TrimSpace(attempt.RequestID),
		RateLimitResetAt: resetAt,
		AuthFailureClass: authClass,
	}); err != nil {
		logInternal(ctx, attempt.RequestID, "channel_health_signal_failed", err)
	}
}

func (o *Observer) triggerCredentialRefresh(attempt Attempt) {
	if o == nil || o.deps.CredentialHotRefresher == nil ||
		attempt.TenantID <= 0 || attempt.Account.AccountID <= 0 ||
		!o.admitRefresh(attempt.TenantID, attempt.Account.AccountID) {
		return
	}
	vendor := strings.TrimSpace(attempt.Account.Platform)
	if vendor == "" {
		vendor = pool.VendorFromProtocolFamily(attempt.ProtocolFamily)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), o.deps.RefreshTimeout)
		defer cancel()
		err := o.deps.CredentialHotRefresher.RefreshHotPath(
			ctx,
			attempt.TenantID,
			attempt.Account.AccountID,
			vendor,
		)
		if o.deps.AuthCooldown != nil {
			o.deps.AuthCooldown.OnRefreshResult(
				ctx,
				attempt.Account.AccountID,
				err == nil,
				err != nil && authcooldown.IsPermanentRefreshError(err),
			)
		}
		if err != nil {
			logInternal(ctx, attempt.RequestID, "credential_hot_refresh_failed", err)
		}
	}()
}

func (o *Observer) admitRefresh(tenantID, accountID int64) bool {
	now := o.now()
	key := refreshKey{tenantID: tenantID, accountID: accountID}
	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()
	if len(o.refreshUntil) > refreshDedupePurgeLimit {
		for candidate, until := range o.refreshUntil {
			if !now.Before(until) {
				delete(o.refreshUntil, candidate)
			}
		}
	}
	if until, ok := o.refreshUntil[key]; ok && now.Before(until) {
		return false
	}
	o.refreshUntil[key] = now.Add(o.deps.RefreshDedupeWindow)
	return true
}

func (o *Observer) now() time.Time {
	if o == nil || o.deps.Now == nil {
		return time.Now()
	}
	return o.deps.Now()
}

func healthKey(attempt Attempt) (channelhealth.ChannelKey, bool) {
	key := channelhealth.ChannelKey{
		TenantID:            attempt.TenantID,
		Vendor:              strings.TrimSpace(attempt.Account.Platform),
		ProviderAccountID:   attempt.Account.AccountID,
		AccountCredentialID: attempt.Account.AccountCredentialID,
		CredentialVersion:   attempt.Account.CredentialVersion,
	}
	if err := key.Validate(); err != nil {
		return channelhealth.ChannelKey{}, false
	}
	key.ChannelID = key.StableChannelID()
	return key, true
}

func dispatchSignalClass(err error, decision gateway.AttemptRetryDecision, classification gateway.Classification) channelhealth.SignalClass {
	if err == nil {
		return ""
	}
	switch transport.TransportErrorClassOf(err) {
	case transport.TransportErrorClassSidecarUnavailable,
		transport.TransportErrorClassSidecarProfileUnavailable:
		return ""
	}
	switch decision.TransportClass {
	case gateway.TransportErrorCredentialExpired:
		return channelhealth.SignalAuthChallenge
	case gateway.TransportErrorConnectTimeout,
		gateway.TransportErrorNetworkTimeout,
		gateway.TransportErrorUpstreamHeaderTimeout,
		gateway.TransportErrorUpstreamBodyIdleTimeout:
		return channelhealth.SignalTimeout
	case gateway.TransportErrorTLSHandshakeFailed,
		gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure:
		return channelhealth.SignalChannelError
	case gateway.TransportErrorLocalDispatch:
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return channelhealth.SignalTimeout
	}
	return gateway.SignalFromClassification(0, classification)
}

func rateLimitReset(classification gateway.Classification, now time.Time) *time.Time {
	if classification.RetryAfterMs <= 0 {
		return nil
	}
	reset := now.Add(time.Duration(classification.RetryAfterMs) * time.Millisecond)
	return &reset
}

func accountCooldownCandidate(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, 529,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func classificationProvider(attempt Attempt) string {
	if strings.EqualFold(strings.TrimSpace(attempt.ProtocolFamily), "bedrock_invoke") {
		return "bedrock"
	}
	return strings.TrimSpace(attempt.Account.Platform)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func logInternal(ctx context.Context, requestID, code string, err error) {
	clienterr.LogInternal(ctx, requestID, code, err)
}
