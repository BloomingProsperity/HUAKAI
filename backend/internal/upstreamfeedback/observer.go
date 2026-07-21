package upstreamfeedback

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/routingsignal"
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
	RoutingSignals         routingsignal.Recorder
	Now                    func() time.Time
	RefreshTimeout         time.Duration
	RefreshDedupeWindow    time.Duration
	Logger                 *slog.Logger
}

type Observer struct {
	deps Dependencies

	refreshMu    sync.Mutex
	refreshUntil map[refreshKey]time.Time
}

type Attempt struct {
	TenantID          int64
	Account           provider.AccountInfo
	ProtocolFamily    string
	ModelKey          string
	RequestID         string
	StartedAt         time.Time
	StatusCodeMapping map[int]int
}

type HTTPFailure struct {
	Decision              gateway.AttemptRetryDecision
	Classification        gateway.Classification
	AccountPolicyDecision rate.Decision
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
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Observer{
		deps:         deps,
		refreshUntil: make(map[refreshKey]time.Time),
	}
}

func (o *Observer) ObserveHTTPError(ctx context.Context, attempt Attempt, statusCode int, headers http.Header, body []byte) HTTPFailure {
	failure, err := classifyHTTPError(attempt, statusCode, headers, body)
	if err != nil {
		logInternal(ctx, attempt.RequestID, "upstream_error_classification_failed", err)
	}

	modelScoped, accountDecision, hasAccountDecision := o.applyRateState(ctx, attempt, statusCode, headers, body, failure.Classification)
	suppressHealth := false
	if hasAccountDecision {
		failure.AccountPolicyDecision = accountDecision
		suppressHealth = ApplyAccountPolicy(ctx, o.deps.Logger, attempt, statusCode, &failure.Decision, failure.Classification, accountDecision)
	}
	if !modelScoped && !suppressHealth {
		o.recordSignal(
			ctx,
			attempt,
			gateway.SignalFromClassification(statusCode, failure.Classification),
			statusCode,
			rateLimitReset(failure.Classification, o.now()),
			gateway.AuthFailureClassFromClassification(failure.Classification),
		)
	}
	if failure.Decision.RefreshIntent == gateway.RefreshOAuthHotPath {
		o.triggerCredentialRefresh(attempt)
	}
	return failure
}

// ApplyAccountPolicy 是所有协议共用的账号错误策略出口：只改客户端投影，计算健康
// 信号是否允许抑制，并记录不含上游正文的结构化命中日志。
func ApplyAccountPolicy(
	ctx context.Context,
	logger *slog.Logger,
	attempt Attempt,
	upstreamStatus int,
	decision *gateway.AttemptRetryDecision,
	classification gateway.Classification,
	accountDecision rate.Decision,
) bool {
	if decision == nil {
		return false
	}
	ApplyClientProjection(decision, accountDecision)
	healthSignalSuppressed := HealthSuppressionAllowed(accountDecision, classification)
	logAccountPolicyMatch(ctx, logger, attempt, upstreamStatus, *decision, classification, accountDecision, healthSignalSuppressed)
	return healthSignalSuppressed
}

func logAccountPolicyMatch(
	ctx context.Context,
	logger *slog.Logger,
	attempt Attempt,
	upstreamStatus int,
	decision gateway.AttemptRetryDecision,
	classification gateway.Classification,
	accountDecision rate.Decision,
	healthSignalSuppressed bool,
) {
	if accountDecision.ClientRuleID == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	errorCode := decision.ClientCode
	if errorCode == "" {
		errorCode = clienterr.CodeUpstreamDispatchError
	}
	logger.InfoContext(ctx, "上游错误命中账号策略",
		logcontract.FieldCategory, string(logcontract.CategoryError),
		logcontract.FieldEventType, "upstream_error.account_policy_matched",
		logcontract.FieldResult, string(logResultForStatus(upstreamStatus)),
		logcontract.FieldErrorClass, string(logErrorClass(classification)),
		logcontract.FieldErrorCode, errorCode,
		logcontract.FieldRetryable, decision.RetryableBeforeDelivery,
		logcontract.FieldActorKind, string(logcontract.ActorSystem),
		logcontract.FieldActorRef, "gateway",
		logcontract.FieldTenantID, attempt.TenantID,
		logcontract.FieldTargetType, "provider_account",
		logcontract.FieldTargetRef, strconv.FormatInt(attempt.Account.AccountID, 10),
		"request_id", strings.TrimSpace(attempt.RequestID),
		"account_policy_rule_id", accountDecision.ClientRuleID,
		"static_classifier_rule_id", classification.RuleID,
		"static_error_class", string(classification.Class),
		"upstream_status_code", upstreamStatus,
		"client_status_code", decision.ClientStatus,
		"affect_health", !accountDecision.SuppressHealthSignal,
		"health_signal_suppressed", healthSignalSuppressed,
	)
}

func logResultForStatus(statusCode int) logcontract.Result {
	if statusCode >= 400 && statusCode < 500 {
		return logcontract.ResultClientFailure
	}
	return logcontract.ResultServerFailure
}

func logErrorClass(classification gateway.Classification) logcontract.ErrorClass {
	switch classification.Class {
	case gateway.ErrorClassOAuthInvalidGrant, gateway.ErrorClassTokenRevoked, gateway.ErrorClassCredentialRejected:
		return logcontract.ErrorAuthentication
	case gateway.ErrorClassRateLimited:
		return logcontract.ErrorRateLimit
	case gateway.ErrorClassNetworkTimeout, gateway.ErrorClassUpstreamTimeout:
		return logcontract.ErrorTimeout
	default:
		return logcontract.ErrorDependency
	}
}

// ClassifyHTTPError 提供无健康副作用的统一客户端错误投影。
// 没有 Observer 的测试或降级接线也必须消费同一静态分类与 binding 状态映射。
func ClassifyHTTPError(attempt Attempt, statusCode int, headers http.Header, body []byte) HTTPFailure {
	failure, _ := classifyHTTPError(attempt, statusCode, headers, body)
	return failure
}

func classifyHTTPError(attempt Attempt, statusCode int, headers http.Header, body []byte) (HTTPFailure, error) {
	providerName := classificationProvider(attempt)
	decision, classification, err := gateway.ClassifyAttemptHTTPError(statusCode, headers, body, providerName)
	if err != nil {
		classification, _ = gateway.Classify(statusCode, headers, body, providerName)
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  "upstream_error",
		}
	}
	applyCredentialAuthSemantics(attempt.Account, &decision, &classification)
	if mapped, ok := attempt.StatusCodeMapping[statusCode]; ok && mapped >= 400 && mapped <= 599 {
		decision.ClientStatus = mapped
	}
	return HTTPFailure{Decision: decision, Classification: classification}, err
}

// applyCredentialAuthSemantics 把状态码分类和实际账号凭据合同合并。静态 Key、
// 长效 Token 与签名凭据被上游拒绝时仍须换号和进入鉴权降级车道，但绝不能
// 伪装成 OAuth invalid_grant，也不能启动无意义的 OAuth 热刷新。
func applyCredentialAuthSemantics(account provider.AccountInfo, decision *gateway.AttemptRetryDecision, classification *gateway.Classification) {
	if decision == nil || classification == nil || decision.RefreshIntent != gateway.RefreshOAuthHotPath {
		return
	}
	handler, ok := credentialstore.DefaultHandlerRegistry().Lookup(account.Platform, account.AccountType)
	if !ok || handler.Refreshable() {
		return
	}
	switch classification.Class {
	case gateway.ErrorClassOAuthInvalidGrant, gateway.ErrorClassTokenRevoked:
		classification.Class = gateway.ErrorClassCredentialRejected
		decision.RefreshIntent = gateway.RefreshNone
		decision.AbortReason = "upstream_credential_rejected"
	}
}

// ApplyClientProjection 只把账号规则允许覆盖的三个客户端字段写入 attempt 决策。
func ApplyClientProjection(decision *gateway.AttemptRetryDecision, accountDecision rate.Decision) {
	if decision == nil {
		return
	}
	if accountDecision.ClientStatus >= 400 && accountDecision.ClientStatus <= 599 {
		decision.ClientStatus = accountDecision.ClientStatus
	}
	if accountDecision.ClientCode != "" {
		decision.ClientCode = accountDecision.ClientCode
	}
	if accountDecision.ClientMessage != "" {
		decision.ClientMessage = accountDecision.ClientMessage
	}
	decision.ClientRuleID = accountDecision.ClientRuleID
}

// HealthSuppressionAllowed 保证账号规则不能隐藏铁证禁用或鉴权恢复信号。
func HealthSuppressionAllowed(decision rate.Decision, classification gateway.Classification) bool {
	if !decision.SuppressHealthSignal {
		return false
	}
	return classification.Tier != gateway.TierIronClad &&
		classification.RetryAction != gateway.RetryActionPermanentDisable &&
		classification.FsmTransition != gateway.FsmTransitionDisabled
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
	if o.deps.RateService != nil && attempt.Account.AccountID > 0 && sessionWindowFeedbackSupported(attempt.Account) {
		if err := o.deps.RateService.UpdateSessionWindow(ctx, attempt.Account.AccountID, headers); err != nil {
			logInternal(ctx, attempt.RequestID, "session_window_update_failed", err)
		}
	}
	o.recordSignal(ctx, attempt, channelhealth.SignalSuccess, statusCode, nil, authcooldown.ClassAmbiguous)
}

func sessionWindowFeedbackSupported(account provider.AccountInfo) bool {
	vendor := strings.ToLower(strings.TrimSpace(account.Platform))
	mode := strings.ToLower(strings.TrimSpace(account.AccountType))
	switch vendor {
	case credentialstore.VendorAnthropic:
		return mode == credentialstore.AuthModeClaudeAIOAuth
	case credentialstore.VendorOpenAI:
		switch mode {
		case credentialstore.AuthModeChatGPTOAuth,
			credentialstore.AuthModeCodexCLIOAuth,
			credentialstore.AuthModeCodexWebOAuth,
			credentialstore.AuthModeCodexAgent:
			return true
		}
	}
	return false
}

func (o *Observer) applyRateState(
	ctx context.Context,
	attempt Attempt,
	statusCode int,
	headers http.Header,
	body []byte,
	classification gateway.Classification,
) (bool, rate.Decision, bool) {
	if o == nil || attempt.Account.AccountID <= 0 {
		return false, rate.Decision{}, false
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
				return true, dec, true
			}
		}
	}

	// 账号自定义规则可匹配任意 HTTP 状态。只按状态码白名单写冷却会让 400/403/404
	// 等规则只改客户端文案、却马上被选号重选，因此规则产生的显式整号冷却优先落地。
	if hasDecision && dec.StateChange == rate.StateTempUnsched {
		o.forceAccountCooldown(ctx, attempt, dec)
		return false, dec, true
	}
	if statusCode == http.StatusNotFound {
		o.recordModelCooldown(ctx, attempt, statusCode, nil, rate.ReasonModelLimitExceeded)
		return false, dec, hasDecision
	}
	if statusCode == http.StatusTooManyRequests && classification.Class == gateway.ErrorClassRateLimited {
		if hasDecision && dec.StateChange != rate.StateNoChange && dec.StateChange != rate.StateRateLimited {
			o.forceAccountCooldown(ctx, attempt, dec)
			return false, dec, true
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
		return o.recordModelCooldown(ctx, attempt, statusCode, resetAt, reason), dec, hasDecision
	}
	if hasDecision && accountCooldownCandidate(statusCode) {
		o.forceAccountCooldown(ctx, attempt, dec)
	}
	return false, dec, hasDecision
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
	if o.deps.RoutingSignals != nil && attempt.TenantID > 0 && attempt.Account.AccountID > 0 {
		now := o.now().UTC()
		latency := now.Sub(attempt.StartedAt)
		latencyValid := !attempt.StartedAt.IsZero() && latency >= 0
		if err := o.deps.RoutingSignals.RecordRoutingSignal(ctx, routingsignal.Observation{
			TenantID:          attempt.TenantID,
			ProviderAccountID: attempt.Account.AccountID,
			Success:           class == channelhealth.SignalSuccess,
			Latency:           latency,
			LatencyValid:      latencyValid,
			ObservedAt:        now,
		}); err != nil {
			logInternal(ctx, attempt.RequestID, "routing_signal_write_failed", err)
		}
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
