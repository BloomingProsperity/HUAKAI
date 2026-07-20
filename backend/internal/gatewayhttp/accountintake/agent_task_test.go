package accountintake

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func TestPrepareExecutionCandidateRegistersOnlyAgentMode(t *testing.T) {
	registrar := &agentTaskRegistrarStub{payload: []byte(`{"task_id":"new-task"}`)}
	service := (&Service{}).WithAgentTaskRegistrar(registrar)
	agent, err := service.prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		Payload: []byte(`{"task_id":""}`),
	})
	if err != nil || string(agent.Payload) != `{"task_id":"new-task"}` || registrar.calls != 1 {
		t.Fatalf("candidate=%s calls=%d err=%v", agent.Payload, registrar.calls, err)
	}
	normal, err := service.prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"token"}`),
	})
	if err != nil || string(normal.Payload) != `{"access_token":"token"}` || registrar.calls != 1 {
		t.Fatalf("normal=%s calls=%d err=%v", normal.Payload, registrar.calls, err)
	}
}

func TestPrepareExecutionCandidateFailsClosedWithoutRegistrar(t *testing.T) {
	_, err := (&Service{}).prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		Payload: []byte(`{}`),
	})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareExecutionCandidateEnrichesBothAntigravityStorageModes(t *testing.T) {
	for _, tc := range []struct {
		name, vendor, authMode string
	}{
		{name: "原生形态", vendor: credentialstore.VendorAntigravity, authMode: credentialstore.AuthModeOAuth},
		{name: "兼容形态", vendor: credentialstore.VendorGemini, authMode: credentialstore.AuthModeAntigravity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enricher := &projectEnricherStub{result: projectenrich.Result{
				Payload:             []byte(`{"access_token":"new","project_id":"project-1","subscription_tier_raw":"g1-pro-tier"}`),
				SubscriptionTierRaw: "g1-pro-tier", SubscriptionVerified: true,
			}}
			candidate, err := (&Service{}).WithProjectEnricher(enricher).prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
				Vendor: tc.vendor, AuthMode: tc.authMode, Payload: []byte(`{"access_token":"old"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if enricher.calls != 1 || enricher.vendor != credentialstore.VendorAntigravity {
				t.Fatalf("补齐调用不符：calls=%d vendor=%q", enricher.calls, enricher.vendor)
			}
			if candidate.Subscription.Label() != "antigravity:pro" || candidate.Subscription.Trust != "verified_api" {
				t.Fatalf("套餐投影不符：%+v", candidate.Subscription)
			}
		})
	}
}

func TestPrepareExecutionCandidateRejectsAntigravityProjectConflict(t *testing.T) {
	enricher := &projectEnricherStub{
		result: projectenrich.Result{Payload: []byte(`{"project_metadata_status":"conflict"}`), SubscriptionConflict: true},
		err:    projectenrich.ErrProjectMetadataConflict,
	}
	candidate, err := (&Service{}).WithProjectEnricher(enricher).prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		Payload: []byte(`{"access_token":"token","project_id":"old"}`),
	})
	if !errors.Is(err, projectenrich.ErrProjectMetadataConflict) {
		t.Fatalf("err=%v", err)
	}
	status, code, _ := preparationFailure(candidate, err)
	if status != StatusConflict || code != "project_metadata_conflict" {
		t.Fatalf("status=%q code=%q", status, code)
	}
}

func TestPrepareExecutionCandidateFailsClosedWithoutAntigravityProjectEnricher(t *testing.T) {
	candidate := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		Payload: []byte(`{"access_token":"token"}`),
	}
	_, err := (&Service{}).prepareExecutionCandidate(context.Background(), candidate)
	if !errors.Is(err, projectenrich.ErrProjectMetadataUnavailable) {
		t.Fatalf("err=%v", err)
	}
	status, code, _ := preparationFailure(candidate, err)
	if status != StatusFailed || code != "project_metadata_unavailable" {
		t.Fatalf("status=%q code=%q", status, code)
	}
}

func TestPrepareExecutionCandidateAllowsDeferredAntigravitySubscription(t *testing.T) {
	enricher := &projectEnricherStub{
		result: projectenrich.Result{Payload: []byte(`{"access_token":"token","project_id":"known","subscription_metadata_status":"operator_attention"}`)},
		err:    projectenrich.ErrSubscriptionMetadataDeferred,
	}
	candidate, err := (&Service{}).WithProjectEnricher(enricher).prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth,
		Payload: []byte(`{"access_token":"token","project_id":"known"}`),
	})
	if err != nil {
		t.Fatalf("已有项目时套餐更新失败不应阻断导入：%v", err)
	}
	if string(candidate.Payload) != `{"access_token":"token","project_id":"known","subscription_metadata_status":"operator_attention"}` {
		t.Fatalf("待更新状态未保留：%s", candidate.Payload)
	}
}

func TestApplyExecutionSubscriptionUsesFinalObservationWithoutIdentityLeak(t *testing.T) {
	result := ExecutionItem{
		Subscription: &subscriptionprofile.Observation{Vendor: "antigravity", Plan: "unknown", Status: "missing"},
		SystemLabels: []string{"antigravity:unknown"},
	}
	observation := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorAntigravity,
		"g1-pro-tier",
		subscriptionprofile.SourceProviderAPI,
		subscriptionprofile.TrustVerifiedAPI,
		subscriptionprofile.VerificationVerified,
		"upstream-user",
		"upstream-workspace",
	)
	applyExecutionSubscription(&result, observation)
	if result.Subscription == nil || result.Subscription.Label() != "antigravity:pro" ||
		result.Subscription.SubjectRef != "" || result.Subscription.WorkspaceRef != "" ||
		len(result.SystemLabels) != 1 || result.SystemLabels[0] != "antigravity:pro" {
		t.Fatalf("执行结果没有使用最终脱敏套餐：%+v", result)
	}
}

type agentTaskRegistrarStub struct {
	payload []byte
	err     error
	calls   int
}

type projectEnricherStub struct {
	result projectenrich.Result
	err    error
	calls  int
	vendor string
}

func (s *projectEnricherStub) Enrich(_ context.Context, vendor string, _ []byte) (projectenrich.Result, error) {
	s.calls++
	s.vendor = vendor
	return s.result, s.err
}

func (s *agentTaskRegistrarStub) EnsureTask(context.Context, []byte) ([]byte, error) {
	s.calls++
	return append([]byte(nil), s.payload...), s.err
}
