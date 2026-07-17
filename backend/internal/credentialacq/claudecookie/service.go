package claudecookie

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type Exchanger interface {
	Exchange(context.Context, string, string) (ExchangeResult, error)
}

type AccountIntake interface {
	PlanCandidate(context.Context, accountintake.CandidatePlanInput) (accountintake.PlanResult, error)
	ExecuteCandidate(context.Context, accountintake.CandidateExecuteInput) (accountintake.ExecutionResult, error)
}

type Service struct {
	exchanger Exchanger
	store     *Store
	accounts  AccountIntake
	now       func() time.Time
}

type ConvertInput struct {
	TenantID       int64
	SessionKey     string
	OrganizationID string
	ActorID        string
	ActorRole      string
	RequestID      string
}

type PlanInput struct {
	TenantID  int64
	SessionID string
	Account   accountintake.AccountDefaults
	ActorID   string
}

type ExecuteInput struct {
	PlanInput
	PlanHash      string
	Confirmations []string
	ActorRole     string
	RequestID     string
	Reason        string
}

func NewService(exchanger Exchanger, store *Store, accounts AccountIntake) *Service {
	return &Service{exchanger: exchanger, store: store, accounts: accounts, now: time.Now}
}

func (s *Service) WithNow(now func() time.Time) *Service {
	copy := *s
	copy.now = now
	return &copy
}

func (s *Service) Convert(ctx context.Context, in ConvertInput) (Session, error) {
	if s == nil || s.exchanger == nil || s.store == nil {
		return Session{}, ErrSessionClosed
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || strings.TrimSpace(in.ActorRole) != "tenant_operator" {
		return Session{}, ErrInvalidInput
	}
	exchanged, err := s.exchanger.Exchange(ctx, in.SessionKey, in.OrganizationID)
	if err != nil {
		return Session{}, err
	}
	candidate, err := credentialacq.BuildClaudeAIOAuthCandidate(in.TenantID, in.ActorID, credentialacq.ClaudeAIOAuthTokenInput{
		AccessToken: exchanged.AccessToken, RefreshToken: exchanged.RefreshToken,
		TokenType: exchanged.TokenType, Scope: exchanged.Scope, ExpiresIn: exchanged.ExpiresIn,
		AccountUUID: exchanged.AccountUUID, AccountEmailAddress: exchanged.AccountEmailAddress, Email: exchanged.Email,
	}, s.nowTime())
	if err != nil {
		return Session{}, err
	}
	defer privacy.Zeroize(candidate.Payload)
	return s.store.Create(ctx, CreateInput{
		TenantID: in.TenantID, Candidate: candidate, Organization: exchanged.Organization,
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID,
		ExpiresAt: s.nowTime().Add(DefaultTTL),
	})
}

func (s *Service) Plan(ctx context.Context, in PlanInput) (accountintake.PlanResult, error) {
	if s == nil || s.store == nil || s.accounts == nil {
		return accountintake.PlanResult{}, ErrSessionClosed
	}
	loaded, err := s.store.Load(ctx, in.TenantID, in.SessionID)
	if err != nil {
		return accountintake.PlanResult{}, err
	}
	defer privacy.Zeroize(loaded.Candidate.Payload)
	if strings.TrimSpace(loaded.Session.ActorID) != strings.TrimSpace(in.ActorID) {
		return accountintake.PlanResult{}, ErrSessionNotFound
	}
	return s.accounts.PlanCandidate(ctx, accountintake.CandidatePlanInput{
		TenantID: in.TenantID, SourceKind: intake.SourceClaudeCookie,
		Candidate: loaded.Candidate, SourceCommitment: loaded.Session.CandidateCommitment,
		Account: in.Account, Now: s.nowTime(),
	})
}

func (s *Service) Execute(ctx context.Context, in ExecuteInput) (accountintake.ExecutionResult, error) {
	if s == nil || s.store == nil || s.accounts == nil {
		return accountintake.ExecutionResult{}, ErrSessionClosed
	}
	loaded, err := s.store.Load(ctx, in.TenantID, in.SessionID)
	if err != nil {
		return accountintake.ExecutionResult{}, err
	}
	defer privacy.Zeroize(loaded.Candidate.Payload)
	if strings.TrimSpace(loaded.Session.ActorID) != strings.TrimSpace(in.ActorID) ||
		strings.TrimSpace(loaded.Session.ActorRole) != strings.TrimSpace(in.ActorRole) {
		return accountintake.ExecutionResult{}, ErrSessionNotFound
	}
	finalize := func(ctx context.Context, database db.DBTX, item accountintake.ExecutionItem) error {
		err := s.store.Consume(ctx, database, in.TenantID, loaded.Session.ID,
			loaded.Session.CandidateCommitment, item.ProviderAccountID, item.AccountCredentialID)
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionExpired) ||
			errors.Is(err, ErrSessionConsumed) || errors.Is(err, ErrSessionClosed) || errors.Is(err, ErrSessionChanged) {
			return accountintake.ErrExecutionStale
		}
		return err
	}
	return s.accounts.ExecuteCandidate(ctx, accountintake.CandidateExecuteInput{
		CandidatePlanInput: accountintake.CandidatePlanInput{
			TenantID: in.TenantID, SourceKind: intake.SourceClaudeCookie,
			Candidate: loaded.Candidate, SourceCommitment: loaded.Session.CandidateCommitment,
			Account: in.Account, Now: s.nowTime(),
		},
		PlanHash: in.PlanHash, Confirmations: in.Confirmations,
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, Reason: in.Reason,
		Finalize: finalize,
	})
}

func (s *Service) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
