package accountintake

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type OAuthStartInput struct {
	TenantID        int64
	Vendor          string
	AuthMode        string
	Account         AccountDefaults
	ActorID         string
	ActorRole       string
	RequestID       string
	Reason          string
	RedirectURI     string
	RequestedScopes []string
	Client          credentialacq.OAuthClientConfig
}

type OAuthPlanResult struct {
	Flow      credentialacq.Session `json:"flow"`
	PlanHash  string                `json:"plan_hash"`
	Plan      intake.Plan           `json:"plan"`
	ExpiresAt time.Time             `json:"expires_at"`
}

type OAuthExecuteInput struct {
	TenantID      int64
	FlowID        string
	PlanHash      string
	Confirmations []string
	ActorID       string
	ActorRole     string
	RequestID     string
	Reason        string
}

type OAuthService struct {
	intake     *Service
	staged     *StagedStore
	sessions   *credentialacq.PostgresSessionStore
	exchangers *credentialacq.ExchangerRegistry
	poller     credentialacq.DeviceCodePoller
	now        func() time.Time
}

func NewOAuthService(
	intakeService *Service,
	staged *StagedStore,
	sessions *credentialacq.PostgresSessionStore,
	exchangers *credentialacq.ExchangerRegistry,
	poller credentialacq.DeviceCodePoller,
) *OAuthService {
	return &OAuthService{intake: intakeService, staged: staged, sessions: sessions, exchangers: exchangers, poller: poller}
}

func (s *OAuthService) Start(ctx context.Context, in OAuthStartInput) (credentialacq.OAuthStartResult, error) {
	if s == nil || s.intake == nil || s.staged == nil || s.sessions == nil || s.exchangers == nil {
		return credentialacq.OAuthStartResult{}, ErrNotConfigured
	}
	in.Vendor = credentialstore.Normalize(in.Vendor)
	in.AuthMode = credentialstore.Normalize(in.AuthMode)
	in.ActorID = strings.TrimSpace(in.ActorID)
	if in.TenantID <= 0 || in.ActorID == "" || !validIntakeActorRole(in.ActorRole) ||
		!credentialacq.ModeAcquisitionReleased(in.Vendor, in.AuthMode) ||
		!credentialacq.SourceAllowedForMode(in.Vendor, in.AuthMode, credentialacq.FlowKindOAuth) {
		return credentialacq.OAuthStartResult{}, ErrInvalidInput
	}
	structural := normalizeInput(PlanInput{
		TenantID: in.TenantID, SourceKind: intake.SourceOAuth,
		DefaultVendor: in.Vendor, DefaultAuthMode: in.AuthMode,
		Content: "oauth-pending", Account: in.Account, Now: s.nowTime(),
	})
	if err := validateInput(structural); err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	client := in.Client
	if strings.TrimSpace(in.RedirectURI) != "" {
		client.RedirectURI = strings.TrimSpace(in.RedirectURI)
	}
	start, err := credentialacq.StartOAuthFlowWithRegistry(ctx, s.sessions, credentialacq.StartInput{
		TenantID: in.TenantID, Vendor: in.Vendor, AuthMode: in.AuthMode,
		Kind: credentialacq.FlowKindOAuth, ActorID: in.ActorID, ActorRole: in.ActorRole,
		RedirectURI: strings.TrimSpace(in.RedirectURI), RequestedScopes: append([]string(nil), in.RequestedScopes...),
		RedactedContext: map[string]any{"purpose": credentialacq.SessionPurposeAccountIntake},
		Purpose:         credentialacq.SessionPurposeAccountIntake,
	}, client, s.exchangers)
	if err != nil {
		return credentialacq.OAuthStartResult{}, err
	}
	auxiliary, err := encodeOAuthAuxiliary(structural.Account.Proxy)
	if err != nil {
		_, _ = s.sessions.Cancel(context.WithoutCancel(ctx), start.Session.ID)
		return credentialacq.OAuthStartResult{}, err
	}
	account := structural.Account
	clearProxyMaterial(account.Proxy)
	account.Proxy = nil
	_, err = s.staged.StageOAuthPending(ctx, OAuthDraftInput{
		ID: start.Session.ID, TenantID: in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
		Vendor: in.Vendor, AuthMode: in.AuthMode, Account: account, Auxiliary: auxiliary,
		ExpiresAt: start.Session.ExpiresAt, RequestID: in.RequestID, Reason: in.Reason,
	})
	if err != nil {
		_, _ = s.sessions.Cancel(context.WithoutCancel(ctx), start.Session.ID)
		return credentialacq.OAuthStartResult{}, err
	}
	return start, nil
}

func (s *OAuthService) Callback(ctx context.Context, flowID, state, code string) (OAuthPlanResult, error) {
	return s.callback(ctx, flowID, state, code, 0, "", "")
}

func (s *OAuthService) CallbackForActor(ctx context.Context, flowID, state, code string, tenantID int64, actorID, actorRole string) (OAuthPlanResult, error) {
	if tenantID <= 0 || strings.TrimSpace(actorID) == "" || !validIntakeActorRole(actorRole) {
		return OAuthPlanResult{}, ErrInvalidInput
	}
	return s.callback(ctx, flowID, state, code, tenantID, strings.TrimSpace(actorID), actorRole)
}

func (s *OAuthService) callback(ctx context.Context, flowID, state, code string, tenantID int64, actorID, actorRole string) (OAuthPlanResult, error) {
	if s == nil || s.sessions == nil || s.staged == nil || s.exchangers == nil {
		return OAuthPlanResult{}, ErrNotConfigured
	}
	existing, err := s.sessions.Get(ctx, strings.TrimSpace(flowID))
	if err != nil {
		return OAuthPlanResult{}, err
	}
	if !credentialacq.IsAccountIntakeSession(existing) {
		return OAuthPlanResult{}, ErrInvalidInput
	}
	if tenantID > 0 && (existing.TenantID != tenantID || existing.ActorID != actorID || existing.ActorRole != actorRole) {
		return OAuthPlanResult{}, ErrInvalidInput
	}
	// 错误 state 只拒绝本次回调，不把仍可用的授权流程置为失败，避免仅凭 flow_id 造成拒绝服务。
	if !credentialacq.OAuthStateMatches(existing.StateHash, state) {
		return OAuthPlanResult{}, credentialacq.ErrStateMismatch
	}
	if existing.Status == credentialacq.StatusCallbackReceived || existing.Status == credentialacq.StatusValidated {
		// 候选可能已经可靠暂存，但请求在会话状态推进或返回响应前中断；优先从暂存恢复，
		// 避免重新消费一次性授权码。
		if recovered, recoverErr := s.Plan(ctx, existing.TenantID, existing.ActorID, existing.ID); recoverErr == nil {
			return recovered, nil
		}
	}
	_, validated, err := credentialacq.CompleteOAuthCallbackWithRegistryAndPersist(
		ctx, s.sessions, existing.ID, strings.TrimSpace(state), strings.TrimSpace(code), s.exchangers,
		func(persistCtx context.Context, session credentialacq.Session, candidate credentialacq.CredentialCandidate) error {
			return s.persistOAuthCandidate(persistCtx, session, candidate)
		},
	)
	if err != nil {
		return OAuthPlanResult{}, err
	}
	return s.Plan(ctx, validated.TenantID, validated.ActorID, validated.ID)
}

func (s *OAuthService) Poll(ctx context.Context, tenantID int64, actorID, flowID, requestID string) (OAuthPlanResult, time.Duration, error) {
	if s == nil || s.sessions == nil || s.staged == nil {
		return OAuthPlanResult{}, 0, ErrNotConfigured
	}
	session, err := s.sessions.Get(ctx, strings.TrimSpace(flowID))
	if err != nil {
		return OAuthPlanResult{}, 0, err
	}
	if !credentialacq.IsAccountIntakeSession(session) || session.TenantID != tenantID || session.ActorID != strings.TrimSpace(actorID) {
		return OAuthPlanResult{}, 0, ErrInvalidInput
	}
	candidate, validated, err := credentialacq.PollDeviceCodeFlow(ctx, s.sessions, session, s.poller, nil, actorID, requestID)
	if errors.Is(err, credentialacq.ErrDevicePollPending) || errors.Is(err, credentialacq.ErrDevicePollInProgress) {
		return OAuthPlanResult{Flow: validated, ExpiresAt: validated.ExpiresAt}, credentialacq.DevicePollRetryAfter(err), err
	}
	if err != nil {
		return OAuthPlanResult{}, 0, err
	}
	result, err := s.storeAndPlanCandidate(ctx, validated, candidate)
	return result, 0, err
}

func (s *OAuthService) Plan(ctx context.Context, tenantID int64, actorID, flowID string) (OAuthPlanResult, error) {
	if s == nil || s.sessions == nil || s.staged == nil || s.intake == nil {
		return OAuthPlanResult{}, ErrNotConfigured
	}
	session, err := s.sessions.Get(ctx, strings.TrimSpace(flowID))
	if err != nil {
		return OAuthPlanResult{}, err
	}
	if !credentialacq.IsAccountIntakeSession(session) || session.TenantID != tenantID || session.ActorID != strings.TrimSpace(actorID) {
		return OAuthPlanResult{}, ErrInvalidInput
	}
	claimed, err := s.staged.LoadOAuthCandidate(ctx, tenantID, actorID, flowID)
	if err != nil {
		return OAuthPlanResult{}, err
	}
	defer func() { claimed.PlanInput.Content = "" }()
	if err := restoreOAuthProxy(&claimed); err != nil {
		return OAuthPlanResult{}, err
	}
	defer clearProxyMaterial(claimed.PlanInput.Account.Proxy)
	plan, err := s.intake.Plan(ctx, claimed.PlanInput)
	if err != nil {
		return OAuthPlanResult{}, err
	}
	if err := s.staged.MarkOAuthPlanned(ctx, tenantID, actorID, flowID, plan.PlanHash, claimed.PlanInput); err != nil {
		return OAuthPlanResult{}, err
	}
	return OAuthPlanResult{Flow: session, PlanHash: plan.PlanHash, Plan: plan.Plan, ExpiresAt: session.ExpiresAt}, nil
}

func (s *OAuthService) Execute(ctx context.Context, in OAuthExecuteInput) (ExecutionResult, error) {
	if s == nil || s.sessions == nil || s.staged == nil || s.intake == nil {
		return ExecutionResult{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || !validIntakeActorRole(in.ActorRole) {
		return ExecutionResult{}, ErrInvalidInput
	}
	session, err := s.sessions.Get(ctx, strings.TrimSpace(in.FlowID))
	if err != nil {
		return ExecutionResult{}, err
	}
	if !credentialacq.IsAccountIntakeSession(session) || session.TenantID != in.TenantID || session.ActorID != strings.TrimSpace(in.ActorID) || session.ActorRole != in.ActorRole {
		return ExecutionResult{}, ErrInvalidInput
	}
	claimed, err := s.staged.LoadOAuthCandidate(ctx, in.TenantID, in.ActorID, in.FlowID)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer func() { claimed.PlanInput.Content = "" }()
	if err := restoreOAuthProxy(&claimed); err != nil {
		return ExecutionResult{}, err
	}
	defer clearProxyMaterial(claimed.PlanInput.Account.Proxy)
	result, executeErr := s.intake.Execute(ctx, ExecuteInput{
		PlanInput: claimed.PlanInput, PlanHash: in.PlanHash, Confirmations: in.Confirmations,
		ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, Reason: in.Reason,
		CommitHook: func(commitCtx context.Context, tx pgx.Tx, commit ExecutionCommit) error {
			summary, err := executionCommitSummary(commit)
			if err != nil {
				return err
			}
			if err := s.staged.ClaimTx(commitCtx, tx, in.TenantID, in.ActorID, in.FlowID, in.PlanHash); err != nil {
				return err
			}
			if _, err := s.sessions.MarkFinalizedTx(commitCtx, tx, session.ID, commit.AccountCredentialID); err != nil {
				return err
			}
			return s.staged.FinishTx(
				commitCtx, tx, in.TenantID, in.ActorID, in.ActorRole,
				in.FlowID, in.RequestID, in.Reason, true, summary,
			)
		},
	})
	if executeErr != nil {
		return ExecutionResult{}, executeErr
	}
	// 失败路径收尾:主执行事务在完成日志/账号写入失败时整体回滚,账号未落库、会话未 finalize,
	// 但暂存流程仍停在 staged。成功路径已在 CommitHook 内 FinishTx(true) 收尾;失败路径这里在
	// 独立事务补写 failed 终态 + credential_acquisition_failed 审计,防流程永久悬挂。补写失败只记
	// 高辨识度日志、不掩盖原始执行结果(cleanup worker 兜底收超时 claimed)。
	if result.Summary.Failed > 0 {
		// 脱离原请求取消(客户端可能已断开),但加超时上界防 DB 池耗尽/锁等待时无限挂起
		//(镜像同文件 persistOAuthCandidate 的 8s 收尾窗口)。
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		defer cancel()
		if ferr := s.staged.FinishFailedFromStaged(
			finishCtx, in.TenantID, in.ActorID, in.ActorRole,
			in.FlowID, in.PlanHash, in.RequestID, in.Reason, result.Summary,
		); ferr != nil {
			slog.WarnContext(ctx, "account intake 失败流程终态补写失败",
				"component", "account_intake_oauth",
				"event_class", "staged_finalize_failed_write_failed",
				"tenant_id", in.TenantID, "flow_id", strings.TrimSpace(in.FlowID),
				"error", ferr.Error())
		}
	}
	return result, nil
}

func (s *OAuthService) storeAndPlanCandidate(ctx context.Context, session credentialacq.Session, candidate credentialacq.CredentialCandidate) (OAuthPlanResult, error) {
	if err := s.persistOAuthCandidate(ctx, session, candidate); err != nil {
		return OAuthPlanResult{}, err
	}
	return s.Plan(ctx, session.TenantID, session.ActorID, session.ID)
}

func (s *OAuthService) persistOAuthCandidate(ctx context.Context, session credentialacq.Session, candidate credentialacq.CredentialCandidate) error {
	defer privacy.Zeroize(candidate.Payload)
	candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
	candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
	if candidate.Vendor != credentialstore.Normalize(session.Vendor) || candidate.AuthMode != credentialstore.Normalize(session.AuthMode) {
		return credentialacq.ErrInvalidTokenShape
	}
	content, err := intake.EncodeOAuthCandidate(candidate)
	if err != nil {
		return err
	}
	defer func() {
		content = ""
	}()
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	var storeErr error
	for attempt := 0; attempt < 3; attempt++ {
		_, storeErr = s.staged.StoreOAuthCandidate(persistCtx, session.TenantID, session.ActorID, session.ID, content)
		if storeErr == nil {
			break
		}
		if errors.Is(storeErr, ErrStagedCredentialReplay) {
			// 上一次写入可能已提交但响应丢失；候选状态机已离开 oauth_pending，按幂等成功处理。
			return nil
		}
		if errors.Is(storeErr, ErrInvalidInput) || errors.Is(storeErr, ErrStagedCredentialNotFound) || errors.Is(storeErr, ErrStagedCredentialExpired) {
			return storeErr
		}
		if attempt < 2 {
			select {
			case <-persistCtx.Done():
				return persistCtx.Err()
			case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
			}
		}
	}
	if storeErr != nil {
		return storeErr
	}
	return nil
}

type oauthStagedAuxiliary struct {
	Version int            `json:"version"`
	Proxy   *ProxyMaterial `json:"proxy,omitempty"`
}

func encodeOAuthAuxiliary(proxy *ProxyMaterial) (json.RawMessage, error) {
	auxiliary := oauthStagedAuxiliary{Version: 1}
	if proxy != nil {
		copy := *proxy
		auxiliary.Proxy = &copy
	}
	return json.Marshal(auxiliary)
}

func restoreOAuthProxy(claimed *ClaimedCredential) error {
	if claimed == nil {
		return ErrInvalidInput
	}
	var auxiliary oauthStagedAuxiliary
	if json.Unmarshal(claimed.Auxiliary, &auxiliary) != nil || auxiliary.Version != 1 {
		return ErrInvalidInput
	}
	if auxiliary.Proxy != nil {
		copy := *auxiliary.Proxy
		claimed.PlanInput.Account.Proxy = &copy
	}
	return nil
}

func (s *OAuthService) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
