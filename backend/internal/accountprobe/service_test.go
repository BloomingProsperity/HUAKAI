package accountprobe

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestProbeUsesExactAccountPathAndWritesSuccessHealth(t *testing.T) {
	vault := probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 91, Platform: credentialstore.VendorOpenAI,
			AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 301, CredentialVersion: 4,
		},
	}
	dispatcher := &probeDispatcherStub{response: successfulProbeEnvelope()}
	health := &probeHealthStub{}
	service := NewService(vault, dispatcher, health)
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service.now = sequenceClock(
		startedAt,
		startedAt.Add(125*time.Millisecond),
	)

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 91,
		ModelAllowList: []string{" z-model ", "a-model", "a-model"},
		RequestID:      "req-probe-success",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK || !result.Attempted || result.Model != "a-model" ||
		result.ProtocolFamily != "openai_chat" || result.LatencyMS != 125 {
		t.Fatalf("result=%+v", result)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want 1", dispatcher.calls)
	}
	dispatchInput := gateway.HCSFDispatchInputFromContext(dispatcher.ctx)
	if dispatchInput.Account.AccountID != 91 || dispatchInput.Account.TenantID != 7 ||
		dispatchInput.Credential.Value != "secret" || dispatchInput.UpstreamModelID != "a-model" {
		t.Fatalf("dispatch input=%+v", dispatchInput)
	}
	if dispatcher.env == nil || dispatcher.env.RequestControls.MaxTokens == nil ||
		*dispatcher.env.RequestControls.MaxTokens != probeMaxOutputTokens ||
		len(dispatcher.env.Messages) != 1 || len(dispatcher.env.Messages[0].Content) != 1 ||
		dispatcher.env.Messages[0].Content[0].Text != "Reply only with OK." {
		t.Fatalf("probe envelope=%+v", dispatcher.env)
	}
	if len(health.signals) != 1 {
		t.Fatalf("health signals=%d want 1", len(health.signals))
	}
	signal := health.signals[0]
	if signal.Class != channelhealth.SignalSuccess || signal.Key.ProviderAccountID != 91 ||
		signal.Key.AccountCredentialID != 301 || signal.Key.CredentialVersion != 4 ||
		signal.RequestID != "req-probe-success" || signal.LatencyMS != 125 {
		t.Fatalf("health signal=%+v", signal)
	}
	if !result.HealthSignalRecorded || len(result.Warnings) != 0 {
		t.Fatalf("health projection=%+v", result)
	}
}

func TestProbeWithoutModelFailsBeforeNetwork(t *testing.T) {
	dispatcher := &probeDispatcherStub{response: successfulProbeEnvelope()}
	health := &probeHealthStub{}
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 91, Platform: credentialstore.VendorOpenAI,
			AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 301, CredentialVersion: 4,
		},
	}, dispatcher, health)

	result, err := service.Probe(context.Background(), Input{TenantID: 7, AccountID: 91})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.OK || result.Attempted || result.ErrorClass != errorModelRequired {
		t.Fatalf("result=%+v", result)
	}
	if dispatcher.calls != 0 || len(health.signals) != 0 {
		t.Fatalf("missing model reached network or health: dispatch=%d health=%d", dispatcher.calls, len(health.signals))
	}
}

func TestProbeHTTPAuthFailureIsClassifiedWithoutSecretLeak(t *testing.T) {
	secretMarker := "upstream-secret-marker"
	dispatcher := &probeDispatcherStub{err: &gateway.UpstreamHTTPError{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(`{"error":{"type":"invalid_grant","message":"` + secretMarker + `"}}`),
	}}
	health := &probeHealthStub{}
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: secretMarker},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 92, Platform: credentialstore.VendorAnthropic,
			AccountType: credentialstore.AuthModeClaudeAIOAuth, AccountCredentialID: 302, CredentialVersion: 2,
		},
	}, dispatcher, health)
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service.now = sequenceClock(
		startedAt,
		startedAt.Add(10*time.Millisecond),
	)

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 92, ProbeModel: "claude-probe", RequestID: "req-auth-failure",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.OK || !result.Attempted || result.StatusCode != http.StatusUnauthorized ||
		result.ErrorClass == "" || result.HealthSignal != channelhealth.SignalAuthChallenge {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(result.Message, secretMarker) || strings.Contains(result.ErrorClass, secretMarker) {
		t.Fatalf("result leaked upstream secret: %+v", result)
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalAuthChallenge {
		t.Fatalf("health signals=%+v", health.signals)
	}
}

func TestProbeTimeoutUsesTypedHealthSignalAndNeverRetries(t *testing.T) {
	dispatcher := &probeDispatcherStub{waitForContext: true}
	health := &probeHealthStub{}
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 93, Platform: credentialstore.VendorGemini,
			AccountType: credentialstore.AuthModeAIStudioAPIKey, AccountCredentialID: 303, CredentialVersion: 1,
		},
	}, dispatcher, health)
	service.timeout = 10 * time.Millisecond
	service.now = sequenceClock(
		time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 12, 0, 1, 0, time.UTC),
	)

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 93, ProbeModel: "gemini-probe",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.OK || result.HealthSignal != channelhealth.SignalTimeout ||
		result.ErrorClass != string(gateway.TransportErrorNetworkTimeout) {
		t.Fatalf("result=%+v", result)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("account probe retried or skipped exact account: calls=%d", dispatcher.calls)
	}
	if !result.HealthSignalRecorded || len(health.signals) != 1 {
		t.Fatalf("上游超时后健康状态未回流: result=%+v signals=%+v", result, health.signals)
	}
	if health.ctxErr != nil {
		t.Fatalf("健康回流错误复用了已取消的上游上下文: %v", health.ctxErr)
	}
}

func TestProbeHealthWriteFailureDoesNotRewriteUpstreamResult(t *testing.T) {
	health := &probeHealthStub{err: errors.New("health store unavailable")}
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 94, Platform: credentialstore.VendorGrok,
			AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 304, CredentialVersion: 1,
		},
	}, &probeDispatcherStub{response: successfulProbeEnvelope()}, health)

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 94, ProbeModel: "grok-probe",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK || result.HealthSignalRecorded ||
		len(result.Warnings) != 1 || result.Warnings[0] != warningHealthNotStored {
		t.Fatalf("result=%+v", result)
	}
}

func TestProbeRejectsBufferedResponseWithoutBusinessContent(t *testing.T) {
	tests := []struct {
		name     string
		response *proto.CanonicalResponse
	}{
		{name: "空对象", response: &proto.CanonicalResponse{}},
		{
			name: "只有用量",
			response: &proto.CanonicalResponse{
				Usage: proto.CanonicalUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
			},
		},
		{
			name: "空内容块",
			response: &proto.CanonicalResponse{
				Content: []proto.CanonicalContentBlock{{Type: "text", Text: "   "}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := proto.NewEmptyEnvelope()
			env.BufferedResponse = tc.response
			health := &probeHealthStub{}
			service := NewService(probeVaultStub{
				credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				account: provider.AccountInfo{
					TenantID: 7, AccountID: 94, Platform: credentialstore.VendorOpenAI,
					AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 304, CredentialVersion: 1,
				},
			}, &probeDispatcherStub{response: env}, health)

			result, err := service.Probe(context.Background(), Input{
				TenantID: 7, AccountID: 94, ProbeModel: "probe-model",
			})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if result.OK || result.ErrorClass != errorEmptyResponse ||
				result.HealthSignal != channelhealth.SignalChannelError {
				t.Fatalf("result=%+v", result)
			}
			if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalChannelError {
				t.Fatalf("health signals=%+v", health.signals)
			}
		})
	}
}

func TestProbeRejectsUnverifiedSessionLane(t *testing.T) {
	dispatcher := &probeDispatcherStub{response: successfulProbeEnvelope()}
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 95, Platform: credentialstore.VendorCursor,
			AccountType: credentialstore.AuthModeOAuth, AccountCredentialID: 305, CredentialVersion: 1,
		},
	}, dispatcher, &probeHealthStub{})

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 95, ProbeModel: "cursor-probe",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Attempted || result.ErrorClass != errorModeUnsupported || dispatcher.calls != 0 {
		t.Fatalf("result=%+v calls=%d", result, dispatcher.calls)
	}
}

func TestProbeLegacyCredentialReportsHealthGapWithoutFailingProbe(t *testing.T) {
	service := NewService(probeVaultStub{
		credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		account: provider.AccountInfo{
			TenantID: 7, AccountID: 96, Platform: credentialstore.VendorKimi,
			AccountType: credentialstore.AuthModeAPIKey,
		},
	}, &probeDispatcherStub{response: successfulProbeEnvelope()}, &probeHealthStub{})

	result, err := service.Probe(context.Background(), Input{
		TenantID: 7, AccountID: 96, ProbeModel: "kimi-probe",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK || result.HealthSignalRecorded ||
		len(result.Warnings) != 1 || result.Warnings[0] != warningLegacyHealthKey {
		t.Fatalf("result=%+v", result)
	}
}

type probeVaultStub struct {
	credential provider.Credential
	account    provider.AccountInfo
	err        error
}

func (s probeVaultStub) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	if s.err != nil {
		return provider.Credential{}, provider.AccountInfo{}, s.err
	}
	if s.account.TenantID != tenantID || s.account.AccountID != accountID {
		return provider.Credential{}, provider.AccountInfo{}, provider.ErrAccountNotFound
	}
	return s.credential, s.account, nil
}

type probeDispatcherStub struct {
	ctx            context.Context
	env            *proto.HCSF
	response       *proto.HCSF
	err            error
	calls          int
	waitForContext bool
}

func (s *probeDispatcherStub) DispatchHCSF(ctx context.Context, env *proto.HCSF) (*proto.HCSF, error) {
	s.calls++
	s.ctx = ctx
	s.env = env
	if s.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.response, s.err
}

type probeHealthStub struct {
	signals []channelhealth.Signal
	err     error
	ctxErr  error
}

func (s *probeHealthStub) ApplySignal(ctx context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.ctxErr = ctx.Err()
	s.signals = append(s.signals, signal)
	return channelhealth.Record{}, s.err
}

func successfulProbeEnvelope() *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		ID: "probe-response", Model: "probe-model",
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "OK"}},
		StopReason: proto.CanonicalStopEndTurn,
	}
	return env
}

func sequenceClock(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if len(values) == 0 {
			return time.Time{}
		}
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
