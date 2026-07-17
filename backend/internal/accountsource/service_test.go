package accountsource

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type loaderStub struct {
	loaded Loaded
}

func (s loaderStub) Load(context.Context, int64, string) (Loaded, error) {
	out := s.loaded
	out.Items = append([]Item(nil), s.loaded.Items...)
	for index := range out.Items {
		out.Items[index].Candidate.Payload = append([]byte(nil), s.loaded.Items[index].Candidate.Payload...)
	}
	return out, nil
}

type intakeStub struct {
	planInputs    []accountintake.CandidatePlanInput
	executeInputs []accountintake.CandidateExecuteInput
}

func (s *intakeStub) PlanCandidate(_ context.Context, input accountintake.CandidatePlanInput) (accountintake.PlanResult, error) {
	s.planInputs = append(s.planInputs, input)
	return accountintake.PlanResult{PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Plan: intake.Plan{SourceKind: input.SourceKind}}, nil
}

func (s *intakeStub) ExecuteCandidate(_ context.Context, input accountintake.CandidateExecuteInput) (accountintake.ExecutionResult, error) {
	s.executeInputs = append(s.executeInputs, input)
	if input.Finalize == nil {
		panic("Finalize must be set")
	}
	_ = input.Finalize(context.Background(), db.DBTX(nil), accountintake.ExecutionItem{})
	return accountintake.ExecutionResult{PlanHash: input.PlanHash}, nil
}

func TestPlanMapsEveryItemIntoUnifiedCandidatePlan(t *testing.T) {
	expires := time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)
	loader := loaderStub{loaded: Loaded{Session: Session{ID: "session", TenantID: 7, SourceKind: intake.SourceCRSSync,
		SourceCommitment: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExpiresAt: expires},
		Items: []Item{{Template: AccountTemplate{Name: "alpha", SourceProvider: "openai", AccountType: "api_key", Enabled: true},
			Candidate: credentialacq.CredentialCandidate{Vendor: "openai", AuthMode: "api_key", Payload: json.RawMessage(`{"api_key":"secret"}`)}}}}}
	intakeService := &intakeStub{}
	service := NewService(loader, intakeService)
	plan, err := service.Plan(context.Background(), PlanInput{TenantID: 7, SessionID: "session", Mappings: []Mapping{{SourceProvider: "openai", ProviderID: 20, ChannelID: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Code != "planned" || len(intakeService.planInputs) != 1 {
		t.Fatalf("plan=%+v inputs=%d", plan, len(intakeService.planInputs))
	}
	got := intakeService.planInputs[0]
	if got.Account.ProviderID != 20 || got.Account.ChannelID != 30 || got.Account.Name != "alpha" || got.SourceKind != intake.SourceCRSSync {
		t.Fatalf("candidate plan input=%+v", got)
	}
	if len(loader.loaded.Items[0].Candidate.Payload) == 0 {
		t.Fatal("测试 fixture 不应被服务副本清零")
	}
}

func TestPlanReportsAmbiguousMappingWithoutCallingIntake(t *testing.T) {
	loader := loaderStub{loaded: Loaded{Session: Session{ID: "session", TenantID: 7, SourceKind: intake.SourceCRSSync,
		SourceCommitment: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExpiresAt: time.Now().Add(time.Minute)},
		Items: []Item{{Template: AccountTemplate{Name: "alpha", SourceProvider: "openai", AccountType: "api_key"},
			Candidate: credentialacq.CredentialCandidate{Vendor: "openai", AuthMode: "api_key", Payload: []byte(`{"api_key":"secret"}`)}}}}}
	intakeService := &intakeStub{}
	service := NewService(loader, intakeService)
	plan, err := service.Plan(context.Background(), PlanInput{TenantID: 7, SessionID: "session", Mappings: []Mapping{
		{SourceProvider: "openai", ProviderID: 20, ChannelID: 30}, {SourceProvider: "openai", ProviderID: 21, ChannelID: 31},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Code != "mapping_required" || len(intakeService.planInputs) != 0 {
		t.Fatalf("plan=%+v calls=%d", plan, len(intakeService.planInputs))
	}
}

func TestExecuteRejectsNonTenantOperator(t *testing.T) {
	service := NewService(loaderStub{}, &intakeStub{})
	_, err := service.Execute(context.Background(), ExecuteInput{
		PlanInput: PlanInput{TenantID: 7}, Selections: []ExecuteSelection{{Index: 0}},
		ActorID: "admin_token:1", ActorRole: "platform_admin",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Execute err=%v，期望拒绝非租户运营身份", err)
	}
}

func TestExecuteRejectsSessionFromAnotherSource(t *testing.T) {
	loader := loaderStub{loaded: Loaded{Session: Session{
		ID: "session", TenantID: 7, SourceKind: intake.SourceAccountRecovery,
		SourceCommitment: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}, Items: []Item{{Candidate: credentialacq.CredentialCandidate{Payload: []byte(`{"api_key":"secret"}`)}}}}}
	intakeService := &intakeStub{}
	service := NewService(loader, intakeService)
	_, err := service.Execute(context.Background(), ExecuteInput{
		PlanInput: PlanInput{TenantID: 7, SessionID: "session"}, ExpectedSource: intake.SourceCRSSync,
		Selections: []ExecuteSelection{{Index: 0}}, ActorID: "admin_token:1", ActorRole: "tenant_operator",
	})
	if !errors.Is(err, ErrSessionSource) {
		t.Fatalf("Execute err=%v，期望拒绝跨来源执行", err)
	}
	if len(intakeService.executeInputs) != 0 {
		t.Fatalf("跨来源会话不应进入统一账号写入，calls=%d", len(intakeService.executeInputs))
	}
}
