package accountintake

import (
	"context"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type ClaudeCookieExchanger interface {
	Exchange(context.Context, claudecookie.Input) (claudecookie.Result, error)
}

type CookiePlanInput struct {
	TenantID       int64
	SessionKey     string
	OrganizationID string
	SetupToken     bool
	Account        AccountDefaults
	ActorID        string
	ActorRole      string
	RequestID      string
	Reason         string
}

type CookiePlanResult struct {
	FlowID         string      `json:"flow_id"`
	ExpiresAt      time.Time   `json:"expires_at"`
	OrganizationID string      `json:"organization_id"`
	AuthMode       string      `json:"auth_mode"`
	PlanHash       string      `json:"plan_hash"`
	Plan           intake.Plan `json:"plan"`
}

type CookieExecuteInput struct {
	TenantID      int64
	FlowID        string
	PlanHash      string
	Confirmations []string
	ActorID       string
	ActorRole     string
	RequestID     string
	Reason        string
}

type CookieService struct {
	intake    *Service
	staged    *StagedStore
	exchanger ClaudeCookieExchanger
	now       func() time.Time
}

func NewCookieService(intakeService *Service, staged *StagedStore, exchanger ClaudeCookieExchanger) *CookieService {
	return &CookieService{intake: intakeService, staged: staged, exchanger: exchanger}
}

func (s *CookieService) Plan(ctx context.Context, in CookiePlanInput) (CookiePlanResult, error) {
	if s == nil || s.intake == nil || s.staged == nil || s.exchanger == nil {
		return CookiePlanResult{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" {
		return CookiePlanResult{}, ErrInvalidInput
	}
	converted, err := s.exchanger.Exchange(ctx, claudecookie.Input{
		SessionKey: in.SessionKey, OrganizationID: in.OrganizationID, SetupToken: in.SetupToken,
	})
	if err != nil {
		return CookiePlanResult{}, err
	}
	planInput := PlanInput{
		TenantID: in.TenantID, SourceKind: intake.SourceJSON,
		DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: converted.AuthMode,
		Content: converted.ImportContent, Account: in.Account, Now: s.nowTime(),
	}
	plan, err := s.intake.Plan(ctx, planInput)
	if err != nil {
		return CookiePlanResult{}, err
	}
	source := "claude_cookie"
	if in.SetupToken {
		source = "claude_setup_cookie"
	}
	staged, err := s.staged.Stage(ctx, StageInput{
		TenantID: in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
		SourceKind: source, Vendor: credentialstore.VendorAnthropic, AuthMode: converted.AuthMode,
		PlanInput: planInput, PlanHash: plan.PlanHash, Content: converted.ImportContent,
		RequestID: in.RequestID, Reason: in.Reason,
	})
	if err != nil {
		return CookiePlanResult{}, err
	}
	return CookiePlanResult{
		FlowID: staged.ID, ExpiresAt: staged.ExpiresAt,
		OrganizationID: converted.OrganizationID, AuthMode: converted.AuthMode,
		PlanHash: plan.PlanHash, Plan: plan.Plan,
	}, nil
}

func (s *CookieService) Execute(ctx context.Context, in CookieExecuteInput) (ExecutionResult, error) {
	if s == nil || s.intake == nil || s.staged == nil {
		return ExecutionResult{}, ErrNotConfigured
	}
	claimed, err := s.staged.Claim(ctx, in.TenantID, in.ActorID, in.FlowID, in.PlanHash)
	if err != nil {
		return ExecutionResult{}, err
	}
	result, executeErr := s.intake.Execute(ctx, ExecuteInput{
		PlanInput: claimed.PlanInput, PlanHash: in.PlanHash, Confirmations: in.Confirmations,
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, Reason: in.Reason,
	})
	success := executeErr == nil && result.Summary.Failed == 0
	finishErr := s.staged.Finish(ctx, in.TenantID, in.ActorID, in.ActorRole, in.FlowID, in.RequestID, in.Reason, success, result.Summary)
	if executeErr != nil {
		return ExecutionResult{}, executeErr
	}
	if finishErr != nil {
		for i := range result.Items {
			result.Items[i].Warnings = appendUnique(result.Items[i].Warnings, "credential_flow_finish_log_failed")
		}
	}
	return result, nil
}

func (s *CookieService) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
