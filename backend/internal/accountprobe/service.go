// Package accountprobe 对指定上游账号执行受预算约束的真实模型探测。
//
// 探测只验证该账号自身的凭据、代理、TLS 和协议链，不经过用户选号、余额、
// 配额或结算，因此不会向任何最终用户计费，也不会换号或回退到其他账号。
package accountprobe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

const (
	defaultTimeout         = 30 * time.Second
	healthFeedbackTimeout  = 3 * time.Second
	probeMaxOutputTokens   = 8
	probeIngressPath       = "/internal/provider-account-probe"
	errorModelRequired     = "probe_model_required"
	errorModeUnsupported   = "probe_mode_unsupported"
	errorEmptyResponse     = "upstream_empty_response"
	errorResponseTooLarge  = "upstream_response_too_large"
	errorDispatchFailed    = "upstream_dispatch_failed"
	warningHealthNotStored = "health_signal_not_recorded"
	warningLegacyHealthKey = "legacy_credential_health_unavailable"
)

var ErrNotConfigured = errors.New("accountprobe: service not configured")

type CredentialVault interface {
	Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error)
}

type Dispatcher interface {
	DispatchHCSF(context.Context, *proto.HCSF) (*proto.HCSF, error)
}

type HealthRecorder interface {
	ApplySignal(context.Context, channelhealth.Signal) (channelhealth.Record, error)
}

type Input struct {
	TenantID       int64
	AccountID      int64
	ProbeModel     string
	ModelAllowList []string
	RequestID      string
}

type Result struct {
	OK                   bool
	Attempted            bool
	Model                string
	ProtocolFamily       string
	Vendor               string
	AuthMode             string
	StatusCode           int
	ErrorClass           string
	Message              string
	LatencyMS            int64
	TestedAt             time.Time
	HealthSignal         channelhealth.SignalClass
	HealthSignalRecorded bool
	Warnings             []string
}

type Service struct {
	vault      CredentialVault
	dispatcher Dispatcher
	health     HealthRecorder
	contracts  *servingcapability.ContractRegistry
	timeout    time.Duration
	now        func() time.Time
}

func NewService(vault CredentialVault, dispatcher Dispatcher, health HealthRecorder) *Service {
	return &Service{
		vault: vault, dispatcher: dispatcher, health: health,
		contracts: servingcapability.DefaultContractRegistry(),
		timeout:   defaultTimeout,
		now:       time.Now,
	}
}

func (s *Service) Probe(ctx context.Context, in Input) (Result, error) {
	if s == nil || s.vault == nil || s.dispatcher == nil || s.contracts == nil {
		return Result{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || in.AccountID <= 0 {
		return Result{}, fmt.Errorf("accountprobe: tenant_id 和 account_id 必须为正数")
	}

	credential, account, err := s.vault.Resolve(ctx, in.TenantID, in.AccountID)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Model:    chooseProbeModel(in.ProbeModel, in.ModelAllowList),
		Vendor:   credentialstore.Normalize(account.Platform),
		AuthMode: credentialstore.Normalize(account.AccountType),
	}
	if result.Model == "" {
		result.ErrorClass = errorModelRequired
		result.Message = "未配置探测模型，且账号模型白名单为空"
		return result, nil
	}

	family, ok := chooseProtocolFamily(s.contracts, account, credential)
	if !ok {
		result.ErrorClass = errorModeUnsupported
		result.Message = "该账号模式没有已验证的文本模型探测协议"
		return result, nil
	}
	result.ProtocolFamily = family

	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startedAt := s.clockNow()
	env := newProbeEnvelope(in, result, startedAt)
	dispatchAccount, transportMode := gateway.ResolveDispatchTransport(account, family)
	dispatchCtx := gateway.ContextWithHCSFDispatchInput(requestCtx, gateway.HCSFDispatchInput{
		ProtocolFamily:  family,
		UpstreamModelID: result.Model,
		Account:         dispatchAccount,
		Credential:      credential,
		TransportMode:   transportMode,
	})

	result.Attempted = true
	response, dispatchErr := s.dispatcher.DispatchHCSF(dispatchCtx, env)
	finishedAt := s.clockNow()
	result.TestedAt = finishedAt.UTC()
	result.LatencyMS = durationMillis(finishedAt.Sub(startedAt))

	var authClass authcooldown.FailureClass
	var resetAt *time.Time
	if dispatchErr != nil {
		result.StatusCode, result.ErrorClass, result.Message, result.HealthSignal, authClass, resetAt =
			classifyFailure(dispatchErr, result.Vendor, finishedAt)
	} else if response == nil || !probeResponseHasBusinessContent(response.BufferedResponse) {
		result.ErrorClass = errorEmptyResponse
		result.Message = "上游未返回可解析的模型响应"
		result.HealthSignal = channelhealth.SignalChannelError
	} else {
		result.OK = true
		result.StatusCode = http.StatusOK
		result.Message = "上游模型探测成功"
		result.HealthSignal = channelhealth.SignalSuccess
	}

	// 上游超时会取消 requestCtx，但超时本身仍必须回流到账号健康。
	// 收尾写入与客户端取消解耦，并用短超时限制后台占用。
	feedbackCtx, feedbackCancel := context.WithTimeout(context.WithoutCancel(ctx), healthFeedbackTimeout)
	s.recordHealth(feedbackCtx, &result, account, in.RequestID, authClass, resetAt)
	feedbackCancel()
	return result, nil
}

func probeResponseHasBusinessContent(response *proto.CanonicalResponse) bool {
	if response == nil {
		return false
	}
	for _, block := range response.Content {
		if strings.TrimSpace(block.Text) != "" ||
			strings.TrimSpace(block.Thinking) != "" ||
			strings.TrimSpace(block.ReasoningSummary) != "" ||
			strings.TrimSpace(block.CallID) != "" ||
			strings.TrimSpace(block.Name) != "" ||
			probeJSONPayloadHasValue(block.Data) ||
			probeJSONPayloadHasValue(block.Input) ||
			probeJSONPayloadHasValue(block.ToolResult) ||
			probeJSONPayloadHasValue(block.Image) {
			return true
		}
	}
	return false
}

func probeJSONPayloadHasValue(raw []byte) bool {
	value := strings.TrimSpace(string(raw))
	switch value {
	case "", "null", `""`, "{}", "[]":
		return false
	default:
		return true
	}
}

func (s *Service) clockNow() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func chooseProbeModel(configured string, allowList []string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	models := make([]string, 0, len(allowList))
	seen := make(map[string]struct{}, len(allowList))
	for _, model := range allowList {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

func chooseProtocolFamily(registry *servingcapability.ContractRegistry, account provider.AccountInfo, credential provider.Credential) (string, bool) {
	vendor := credentialstore.Normalize(account.Platform)
	authMode := credentialstore.Normalize(account.AccountType)
	if authMode == "upstream_static" &&
		credential.Type == provider.CredentialTypeUpstreamPassthrough &&
		strings.TrimSpace(credential.Extra["base_url"]) != "" {
		return registrydefault.ProtocolOpenAIChat, true
	}

	for _, contract := range registry.ForMode(vendor, authMode) {
		if contract.Lane != servingcapability.ServingLaneChatHCSF ||
			!contract.WireVerified ||
			contract.ReleaseState == servingcapability.ReleaseStateScaffold ||
			contract.ReleaseState == servingcapability.ReleaseStateRetired ||
			contract.RequestMarshalShape == "native_raw" {
			continue
		}
		return contract.Family, true
	}
	return "", false
}

func newProbeEnvelope(in Input, result Result, at time.Time) *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("account-probe-%d-%d", in.AccountID, at.UnixNano())
	}
	env.RequestMeta = proto.RequestMeta{
		RequestID:         requestID,
		TenantID:          in.TenantID,
		AccountID:         in.AccountID,
		ClientProtocol:    proto.ClientProtocolOpenAIChat,
		ProtocolFamily:    result.ProtocolFamily,
		EndpointFamily:    result.ProtocolFamily,
		Provider:          result.Vendor,
		Model:             result.Model,
		UpstreamModel:     result.Model,
		IngressPath:       probeIngressPath,
		EvidenceLabel:     proto.EvidenceSmoke,
		NativePassthrough: false,
	}
	env.Messages = []proto.CanonicalMessage{{
		Role: "user",
		Content: []proto.CanonicalContentBlock{{
			Type: "text",
			Text: "Reply only with OK.",
		}},
	}}
	maxTokens := probeMaxOutputTokens
	env.RequestControls.MaxTokens = &maxTokens
	env.StreamPlan.Mode = proto.StreamModeBuffered
	env.Policy.Audit.Label = "provider_account_probe"
	return env
}

func classifyFailure(err error, vendor string, now time.Time) (int, string, string, channelhealth.SignalClass, authcooldown.FailureClass, *time.Time) {
	var upstreamErr *gateway.UpstreamHTTPError
	if errors.As(err, &upstreamErr) {
		_, classification, classifyErr := gateway.ClassifyAttemptHTTPError(
			upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, vendor,
		)
		if classifyErr != nil {
			return upstreamErr.StatusCode, errorDispatchFailed, safeFailureMessage(errorDispatchFailed),
				channelhealth.SignalChannelError, authcooldown.ClassAmbiguous, nil
		}
		errorClass := string(classification.Class)
		if errorClass == "" {
			errorClass = errorDispatchFailed
		}
		var resetAt *time.Time
		if classification.RetryAfterMs > 0 {
			value := now.Add(time.Duration(classification.RetryAfterMs) * time.Millisecond).UTC()
			resetAt = &value
		}
		return upstreamErr.StatusCode, errorClass, safeFailureMessage(errorClass),
			gateway.SignalFromClassification(upstreamErr.StatusCode, classification),
			gateway.AuthFailureClassFromClassification(classification), resetAt
	}
	if errors.Is(err, gateway.ErrUpstreamResponseTooLarge) {
		return 0, errorResponseTooLarge, safeFailureMessage(errorResponseTooLarge),
			channelhealth.SignalChannelError, authcooldown.ClassAmbiguous, nil
	}

	decision := gateway.ClassifyAttemptDispatchError(err)
	errorClass := string(decision.TransportClass)
	signal := channelhealth.SignalClass("")
	switch decision.TransportClass {
	case gateway.TransportErrorConnectTimeout,
		gateway.TransportErrorNetworkTimeout,
		gateway.TransportErrorUpstreamHeaderTimeout,
		gateway.TransportErrorUpstreamBodyIdleTimeout:
		signal = channelhealth.SignalTimeout
	case gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure,
		gateway.TransportErrorUpstreamConnect:
		signal = channelhealth.SignalChannelError
	}
	if errorClass == "" || decision.TransportClass == gateway.TransportErrorLocalDispatch {
		errorClass = errorDispatchFailed
	}
	return 0, errorClass, safeFailureMessage(errorClass), signal, authcooldown.ClassAmbiguous, nil
}

func safeFailureMessage(errorClass string) string {
	switch errorClass {
	case string(gateway.ErrorClassOAuthInvalidGrant),
		string(gateway.ErrorClassTokenRevoked),
		string(gateway.ErrorClassCredentialRejected):
		return "上游拒绝当前账号凭据，需要重新认证或更换凭据"
	case string(gateway.ErrorClassRateLimited):
		return "上游对当前账号限流，请在重置时间后重试"
	case string(gateway.ErrorClassCreditsRefillable),
		string(gateway.ErrorClassCreditExhausted):
		return "当前账号的上游额度不足"
	case string(gateway.ErrorClassKYCRequired),
		string(gateway.ErrorClassOrgDisabled),
		string(gateway.ErrorClassWorkspaceDeactivated):
		return "当前账号在上游不可用，需要人工检查账号状态"
	case string(gateway.ErrorClassServerError),
		string(gateway.ErrorClassOverloaded):
		return "上游服务暂时不可用"
	case string(gateway.ErrorClassNetworkTimeout),
		string(gateway.ErrorClassUpstreamTimeout),
		string(gateway.TransportErrorConnectTimeout),
		string(gateway.TransportErrorUpstreamHeaderTimeout),
		string(gateway.TransportErrorUpstreamBodyIdleTimeout):
		return "连接上游超时"
	case errorResponseTooLarge:
		return "上游响应超过安全读取上限"
	default:
		return "上游模型探测失败"
	}
}

func (s *Service) recordHealth(ctx context.Context, result *Result, account provider.AccountInfo, requestID string, authClass authcooldown.FailureClass, resetAt *time.Time) {
	if result == nil || result.HealthSignal == "" {
		return
	}
	if s.health == nil {
		result.Warnings = append(result.Warnings, warningHealthNotStored)
		return
	}
	key := channelhealth.ChannelKey{
		TenantID:            account.TenantID,
		Vendor:              credentialstore.Normalize(account.Platform),
		ProviderAccountID:   account.AccountID,
		AccountCredentialID: account.AccountCredentialID,
		CredentialVersion:   account.CredentialVersion,
	}
	if err := key.Validate(); err != nil {
		result.Warnings = append(result.Warnings, warningLegacyHealthKey)
		return
	}
	_, err := s.health.ApplySignal(ctx, channelhealth.Signal{
		Key: key, Class: result.HealthSignal, StatusCode: result.StatusCode,
		LatencyMS: result.LatencyMS, At: result.TestedAt, RequestID: strings.TrimSpace(requestID),
		RateLimitResetAt: resetAt, AuthFailureClass: authClass,
	})
	if err != nil {
		result.Warnings = append(result.Warnings, warningHealthNotStored)
		return
	}
	result.HealthSignalRecorded = true
}

func durationMillis(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	millis := value.Milliseconds()
	if millis > math.MaxInt32 {
		return math.MaxInt32
	}
	return millis
}
