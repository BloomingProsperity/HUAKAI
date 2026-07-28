package accountintake

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func (s *Service) Execute(ctx context.Context, in ExecuteInput) (ExecutionResult, error) {
	in.PlanHash = strings.TrimSpace(in.PlanHash)
	if in.PlanHash == "" {
		return ExecutionResult{}, ErrPlanHashMissing
	}
	if !validPlanHash(in.PlanHash) {
		return ExecutionResult{}, fmt.Errorf("%w: plan_hash must be 64 lowercase hexadecimal characters", ErrInvalidInput)
	}
	if err := validateConfirmations(in.Confirmations); err != nil {
		return ExecutionResult{}, err
	}
	prepared, err := s.prepare(ctx, in.PlanInput)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer zeroizeCandidates(prepared.candidates)
	if in.CommitHook != nil && len(prepared.result.Plan.Items) != 1 {
		return ExecutionResult{}, ErrInvalidInput
	}
	if subtle.ConstantTimeCompare([]byte(in.PlanHash), []byte(prepared.result.PlanHash)) != 1 {
		return ExecutionResult{}, ErrPlanChanged
	}
	confirmed := confirmationSet(in.Confirmations)
	out := ExecutionResult{
		PlanHash: prepared.result.PlanHash,
		Items:    make([]ExecutionItem, 0, len(prepared.result.Plan.Items)),
	}
	for _, item := range prepared.result.Plan.Items {
		result := ExecutionItem{
			Index: item.Index, PlannedAction: item.Action,
			Code: item.Code, Message: item.Message,
			ProviderAccountID:   item.ExistingAccountID,
			AccountCredentialID: item.ExistingCredentialID,
			CredentialVersion:   item.ExistingCredentialVersion,
			Subscription:        item.Subscription,
			SystemLabels:        append([]string(nil), item.SystemLabels...),
		}
		switch item.Action {
		case intake.ActionSkip:
			result.Status = StatusSkipped
		case intake.ActionConflict:
			result.Status = StatusConflict
		case intake.ActionFail:
			result.Status = StatusFailed
		case intake.ActionCreate, intake.ActionUpdate:
			if missing := missingConfirmations(item.RequiredConfirmations, confirmed); len(missing) > 0 {
				result.Status = StatusConflict
				result.Code = "confirmation_required"
				result.Message = "缺少执行该项所需的明确确认"
				result.Warnings = append(result.Warnings, missing...)
				break
			}
			if item.Index < 0 || item.Index >= len(prepared.candidates) {
				result.Status = StatusFailed
				result.Code = "candidate_index_invalid"
				result.Message = "预检项与凭据候选不一致"
				break
			}
			candidate := prepared.candidates[item.Index]
			// 先补齐凭据(过期即刷新)再考虑铸号:铸号要拿 access_token 去上游注册,
			// 用一张已过期的会话票必然 401。顺序颠倒会出现「不开铸号能导入、开了反而
			// 整批失败」的反直觉结果。
			candidate, err = s.prepareExecutionCandidate(ctx, candidate)
			if err != nil {
				result.Status, result.Code, result.Message = preparationFailure(candidate, err)
				break
			}
			minted, mintErr := s.mintForExecution(ctx, prepared.input.Account.MintAgentIdentity, candidate)
			if mintErr != nil {
				// 复核副本是独立分配，外层 defer 覆盖不到；失败分支必须就地擦，
				// 否则会话 token 会一直留在堆上等 GC。
				privacy.Zeroize(minted.PlanCandidate.Payload)
				result.Status, result.Code, result.Message = preparationFailure(minted.Candidate, mintErr)
				result.Warnings = appendOrphanRuntimeWarning(result.Warnings, minted.RuntimeID)
				break
			}
			candidate = minted.Candidate
			preMintCandidate, mintedRuntimeID := minted.PlanCandidate, minted.RuntimeID
			if candidate.AuthMode == credentialstore.AuthModeCodexAgent {
				privacy.Zeroize(prepared.candidates[item.Index].Payload)
				prepared.candidates[item.Index] = candidate
			}
			if item.Action == intake.ActionCreate {
				result = s.executeCreate(ctx, prepared, in, item, candidate, preMintCandidate)
			} else {
				result = s.executeUpdate(ctx, prepared, in, item, candidate, preMintCandidate)
			}
			// 复核副本是独立分配，不在 prepared.candidates 的 defer 清零覆盖面内；
			// 它整份持有会话 token 或 Agent 私钥，用完立即擦，不等 GC。
			privacy.Zeroize(preMintCandidate.Payload)
			// 铸号是不可回滚的上游副作用:写库没成功就意味着上游多了一个无人认领的
			// runtime,必须让运营在结果里直接看到它,否则只能去翻日志。
			if result.Status != StatusCreated && result.Status != StatusUpdated {
				result.Warnings = appendOrphanRuntimeWarning(result.Warnings, mintedRuntimeID)
			}
		default:
			result.Status = StatusFailed
			result.Code = "planned_action_invalid"
			result.Message = "预检动作无效"
		}
		if s.activation != nil && (result.Status == StatusCreated || result.Status == StatusUpdated) && result.ProviderAccountID > 0 {
			s.activation.NotifyAccountActivated(prepared.input.TenantID, result.ProviderAccountID)
		}
		addExecutionSummary(&out.Summary, result.Status)
		out.Items = append(out.Items, result)
	}
	return out, nil
}

func (s *Service) prepareExecutionCandidate(ctx context.Context, candidate credentialacq.CredentialCandidate) (credentialacq.CredentialCandidate, error) {
	if importCredentialNeedsRefresh(candidate.Payload, time.Now().UTC()) {
		if s == nil || s.refresher == nil {
			return candidate, ErrImportCredentialRefreshUnavailable
		}
		refreshed, err := s.refresher.RefreshImportCredential(ctx, candidate, time.Now().UTC())
		if err != nil {
			return candidate, errors.Join(ErrImportCredentialRefreshFailed, err)
		}
		candidate = refreshed
	}
	projectProfile := projectenrich.ProfileForMode(candidate.Vendor, candidate.AuthMode)
	if projectProfile != "" {
		if s == nil || s.projects == nil {
			return candidate, fmt.Errorf("%w: 项目解析器未配置", projectenrich.ErrProjectMetadataUnavailable)
		}
		enriched, enrichErr := s.projects.Enrich(ctx, projectProfile, candidate.Payload)
		if len(enriched.Payload) > 0 {
			candidate.Payload = enriched.Payload
		}
		if enriched.SubscriptionVerified {
			subscriptionVendor := subscriptionprofile.VendorAntigravity
			if projectProfile == projectenrich.ProfileGeminiCodeAssist {
				subscriptionVendor = subscriptionprofile.VendorGemini
			}
			candidate.Subscription = subscriptionprofile.FromRaw(
				subscriptionVendor,
				enriched.SubscriptionTierRaw,
				subscriptionprofile.SourceProviderAPI,
				subscriptionprofile.TrustVerifiedAPI,
				subscriptionprofile.VerificationVerified,
				candidate.ExternalSubjectID,
				candidate.ExternalAccountID,
			)
		}
		if enriched.SubscriptionConflict {
			subscriptionVendor := subscriptionprofile.VendorAntigravity
			if projectProfile == projectenrich.ProfileGeminiCodeAssist {
				subscriptionVendor = subscriptionprofile.VendorGemini
			}
			candidate.Subscription = subscriptionprofile.Missing(subscriptionVendor, subscriptionprofile.SourceProviderAPI)
			candidate.Subscription.Trust = subscriptionprofile.TrustVerifiedAPI
			candidate.Subscription.Verification = subscriptionprofile.VerificationVerified
			candidate.Subscription.Status = subscriptionprofile.StatusConflict
			candidate.Subscription.ErrorClass = "subscription_metadata_conflict"
		}
		if enrichErr != nil && !errors.Is(enrichErr, projectenrich.ErrSubscriptionMetadataDeferred) {
			return candidate, enrichErr
		}
	}
	if credentialstore.Normalize(candidate.Vendor) != credentialstore.VendorOpenAI ||
		credentialstore.Normalize(candidate.AuthMode) != credentialstore.AuthModeCodexAgent {
		return candidate, nil
	}
	if s == nil || s.agentTasks == nil {
		return credentialacq.CredentialCandidate{}, ErrNotConfigured
	}
	payload, err := s.agentTasks.EnsureTask(ctx, candidate.Payload)
	if err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	candidate.Payload = payload
	return candidate, nil
}

func importCredentialNeedsRefresh(payload []byte, now time.Time) bool {
	var fields map[string]any
	if json.Unmarshal(payload, &fields) != nil {
		return false
	}
	raw, _ := fields["expires_at"].(string)
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return !expiresAt.After(now.Add(2 * time.Minute))
}

// executeCreate 的 planCandidate 是预检据以成案的那份候选(铸号前)，只用于事务内
// 稳定性复核；写库一律用 candidate(铸号后的最终材料)。
func (s *Service) executeCreate(ctx context.Context, prepared preparedPlan, in ExecuteInput, expected intake.Item, candidate, planCandidate credentialacq.CredentialCandidate) ExecutionItem {
	result := baseExecutionItem(expected)
	applyExecutionSubscription(&result, candidate.Subscription)
	var accountID int64
	var metadata credentialstore.CredentialMetadata
	err := s.credentials.WithTransaction(ctx, func(txStore *credentialstore.Store, database db.DBTX) error {
		tx, ok := database.(pgx.Tx)
		if !ok {
			return errors.New("account intake transaction is not pgx.Tx")
		}
		if err := lockTenantIntake(ctx, tx, prepared.input.TenantID); err != nil {
			return err
		}
		if err := ensureStableItem(ctx, txStore, prepared.input, planCandidate, expected); err != nil {
			return err
		}
		if isCodexIntake(prepared.input) {
			if err := lockRunnableCodexLane(
				ctx, tx, prepared.input.TenantID, prepared.input.Account.ProviderID, prepared.input.Account.ChannelID,
			); err != nil {
				return err
			}
		}
		proxyID, err := s.resolveProxy(ctx, tx, prepared.input, in)
		if err != nil {
			return err
		}
		actorID := strings.TrimSpace(in.ActorID)
		accountResult, err := accountcreate.InsertTx(ctx, tx, accountcreate.Params{
			Insert: admindb.InsertProviderAccountParams{
				TenantID:                   prepared.input.TenantID,
				ProviderID:                 prepared.input.Account.ProviderID,
				ChannelID:                  prepared.input.Account.ChannelID,
				Name:                       accountName(prepared.input.Account, expected.Index),
				AccountType:                prepared.input.Account.AccountType,
				Enabled:                    prepared.input.Account.Enabled,
				ExpiresAt:                  nullableTimestamp(prepared.input.Account.ExpiresAt),
				Credentials:                []byte(`{}`),
				CapConcurrency:             prepared.input.Account.CapConcurrency,
				CapQueueSticky:             prepared.input.Account.CapQueueSticky,
				CapQueueFallback:           prepared.input.Account.CapQueueFallback,
				Priority:                   prepared.input.Account.Priority,
				StaticWeight:               prepared.input.Account.StaticWeight,
				UpstreamCostRatio:          prepared.input.Account.UpstreamCostRatio,
				ProbeModel:                 prepared.input.Account.ProbeModel,
				Tags:                       prepared.input.Account.Tags,
				Extra:                      normalizedExtra(prepared.input.Account.Extra),
				ModelAllowList:             prepared.input.Account.ModelAllowList,
				CapabilityFlags:            prepared.input.Account.CapabilityFlags,
				RPMLimit:                   prepared.input.Account.RPMLimit,
				TPMLimit:                   prepared.input.Account.TPMLimit,
				WindowCostLimitCents:       prepared.input.Account.WindowCostLimitCents,
				MaxSessions:                prepared.input.Account.MaxSessions,
				DisableCooling:             prepared.input.Account.DisableCooling,
				RefreshLeadSeconds:         prepared.input.Account.RefreshLeadSeconds,
				TLSFingerprintRotate:       prepared.input.Account.TLSFingerprintRotate,
				CustomErrorCodesEnabled:    prepared.input.Account.CustomErrorCodesEnabled,
				CustomErrorCodes:           prepared.input.Account.CustomErrorCodes,
				PoolMode:                   prepared.input.Account.PoolMode,
				TempUnschedulableEnabled:   prepared.input.Account.TempUnschedulableEnabled,
				TempUnschedulableRulesJSON: normalizedRules(prepared.input.Account.TempUnschedulableRules),
				ProxyID:                    proxyID,
				ActorID:                    optionalString(actorID),
			},
			Candidate: mixedchannelrisk.Account{
				ProviderID:  prepared.input.Account.ProviderID,
				ChannelID:   prepared.input.Account.ChannelID,
				AccountType: prepared.input.Account.AccountType,
				Vendor:      candidate.Vendor, AuthMode: candidate.AuthMode,
			},
			ProviderFamily: prepared.providerFamily,
			Confirmed:      hasConfirmation(in.Confirmations, "confirm_mixed_channel_risk"),
		})
		if err != nil {
			return err
		}
		accountID = accountResult.ID
		metadata, err = txStore.Create(ctx, credentialstore.CreateCredentialInput{
			TenantID: prepared.input.TenantID, ProviderAccountID: accountID,
			Vendor: candidate.Vendor, AuthMode: candidate.AuthMode,
			Payload: candidate.Payload, ActorID: actorID,
			ExternalAccountID:      candidate.ExternalAccountID,
			ExternalSubjectID:      candidate.ExternalSubjectID,
			ExternalAccountEmail:   candidate.ExternalAccountEmail,
			ExternalIdentitySource: candidate.AccountIDSource,
			Subscription:           candidate.Subscription,
		})
		if err != nil {
			return err
		}
		if err := initializeHealthTx(ctx, tx, prepared.input.TenantID, accountID, metadata); err != nil {
			return err
		}
		if err := insertAdminAudit(ctx, tx, in, prepared.input.TenantID, "create_provider_account", "provider_account", accountID, map[string]any{
			"provider_id":                  prepared.input.Account.ProviderID,
			"channel_id":                   prepared.input.Account.ChannelID,
			"vendor":                       candidate.Vendor,
			"auth_mode":                    candidate.AuthMode,
			"credential_id":                metadata.ID,
			"credential_version":           metadata.Version,
			"batch_intake":                 true,
			"subscription_label":           candidate.Subscription.Label(),
			"subscription_status":          candidate.Subscription.Status,
			"mixed_channel_risk_confirmed": expected.MixedChannelRisk != nil && expected.MixedChannelRisk.HighRisk,
			"proxy_id":                     proxyID,
		}); err != nil {
			return err
		}
		return runExecutionCommitHook(ctx, tx, in, ExecutionCommit{
			Status: StatusCreated, ProviderAccountID: accountID,
			AccountCredentialID: metadata.ID, CredentialVersion: metadata.Version,
		})
	})
	if err != nil {
		result.Status = StatusFailed
		result.Code = executionErrorCode(err)
		result.Message = executionErrorMessage(err)
		return result
	}
	result.Status = StatusCreated
	result.Code = "account_created"
	result.Message = "账号与加密凭据已创建"
	result.ProviderAccountID = accountID
	result.AccountCredentialID = metadata.ID
	result.CredentialVersion = metadata.Version
	result.ChannelHealthInitialized = true
	return result
}

// executeUpdate 的 planCandidate 同 executeCreate：复核用铸号前候选，写库用铸号后材料。
func (s *Service) executeUpdate(ctx context.Context, prepared preparedPlan, in ExecuteInput, expected intake.Item, candidate, planCandidate credentialacq.CredentialCandidate) ExecutionItem {
	result := baseExecutionItem(expected)
	applyExecutionSubscription(&result, candidate.Subscription)
	var metadata credentialstore.CredentialMetadata
	err := s.credentials.WithTransaction(ctx, func(txStore *credentialstore.Store, database db.DBTX) error {
		tx, ok := database.(pgx.Tx)
		if !ok {
			return errors.New("account intake transaction is not pgx.Tx")
		}
		if err := lockTenantIntake(ctx, tx, prepared.input.TenantID); err != nil {
			return err
		}
		if err := ensureStableItem(ctx, txStore, prepared.input, planCandidate, expected); err != nil {
			return err
		}
		if err := accountcreate.ValidateCredentialCompatibility(
			ctx, admindb.New(tx), prepared.input.TenantID, expected.ExistingAccountID, candidate.Vendor, candidate.AuthMode,
		); err != nil {
			return err
		}
		if isCodexIntake(prepared.input) {
			account, err := admindb.New(tx).GetAdminProviderAccount(ctx, admindb.GetAdminProviderAccountParams{
				ID: expected.ExistingAccountID, TenantID: prepared.input.TenantID,
			})
			if err != nil {
				return err
			}
			if err := lockRunnableCodexLane(ctx, tx, prepared.input.TenantID, account.ProviderID, account.ChannelID); err != nil {
				return err
			}
		}
		proxyID, err := s.resolveProxy(ctx, tx, prepared.input, in)
		if err != nil {
			return err
		}
		version := expected.ExistingCredentialVersion
		if in.ReplaceExistingConfig {
			if err := replaceAccountConfiguration(ctx, tx, prepared.input, in, expected.ExistingAccountID, proxyID); err != nil {
				return err
			}
		} else if proxyID != nil {
			tag, err := tx.Exec(ctx, `UPDATE provider_accounts
SET proxy_id=$1, updated_at=clock_timestamp()
WHERE id=$2 AND tenant_id=$3`, *proxyID, expected.ExistingAccountID, prepared.input.TenantID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrExecutionStale
			}
		}
		if isCodexIntake(prepared.input) && !in.ReplaceExistingConfig {
			if err := ensureCodexCapabilities(ctx, tx, prepared.input.TenantID, expected.ExistingAccountID); err != nil {
				return err
			}
		}
		metadata, err = txStore.Rotate(ctx, credentialstore.RotateCredentialInput{
			TenantID:               prepared.input.TenantID,
			ProviderAccountID:      expected.ExistingAccountID,
			CredentialID:           expected.ExistingCredentialID,
			ExpectedVersion:        &version,
			Vendor:                 candidate.Vendor,
			AuthMode:               candidate.AuthMode,
			Payload:                candidate.Payload,
			ActorID:                strings.TrimSpace(in.ActorID),
			ExternalAccountID:      candidate.ExternalAccountID,
			ExternalSubjectID:      candidate.ExternalSubjectID,
			ExternalAccountEmail:   candidate.ExternalAccountEmail,
			ExternalIdentitySource: candidate.AccountIDSource,
			Subscription:           candidate.Subscription,
		})
		if err != nil {
			return err
		}
		if err := initializeHealthTx(ctx, tx, prepared.input.TenantID, expected.ExistingAccountID, metadata); err != nil {
			return err
		}
		if err := insertAdminAudit(ctx, tx, in, prepared.input.TenantID, "rotate_account_credential", "account_credential", metadata.ID, map[string]any{
			"provider_account_id": expected.ExistingAccountID,
			"vendor":              candidate.Vendor,
			"auth_mode":           candidate.AuthMode,
			"credential_version":  metadata.Version,
			"batch_intake":        true,
			"subscription_label":  candidate.Subscription.Label(),
			"subscription_status": candidate.Subscription.Status,
			"proxy_id":            proxyID,
			"codex_capabilities_present": isCodexIntake(prepared.input) &&
				containsAllStrings(prepared.input.Account.CapabilityFlags, codexDefaultCapabilities),
		}); err != nil {
			return err
		}
		return runExecutionCommitHook(ctx, tx, in, ExecutionCommit{
			Status: StatusUpdated, ProviderAccountID: expected.ExistingAccountID,
			AccountCredentialID: metadata.ID, CredentialVersion: metadata.Version,
		})
	})
	if err != nil {
		result.Status = StatusFailed
		result.Code = executionErrorCode(err)
		result.Message = executionErrorMessage(err)
		return result
	}
	result.Status = StatusUpdated
	result.Code = "credential_rotated"
	result.Message = "已有账号凭据已按版本锁轮换"
	result.ProviderAccountID = expected.ExistingAccountID
	result.AccountCredentialID = metadata.ID
	result.CredentialVersion = metadata.Version
	result.ChannelHealthInitialized = true
	return result
}

func runExecutionCommitHook(ctx context.Context, tx pgx.Tx, in ExecuteInput, commit ExecutionCommit) error {
	if in.CommitHook == nil {
		return nil
	}
	return in.CommitHook(ctx, tx, commit)
}

func containsAllStrings(values, required []string) bool {
	for _, requiredValue := range required {
		found := false
		for _, value := range values {
			if value == requiredValue {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Service) resolveProxy(ctx context.Context, tx pgx.Tx, plan PlanInput, execution ExecuteInput) (*int64, error) {
	if plan.Account.Proxy == nil {
		return nil, nil
	}
	if s == nil || s.proxies == nil {
		return nil, ErrNotConfigured
	}
	id, err := s.proxies.ResolveTx(ctx, tx, ProxyResolveInput{
		TenantID: plan.TenantID, Material: *plan.Account.Proxy,
		ActorID: strings.TrimSpace(execution.ActorID), ActorRole: execution.ActorRole,
		RequestID: strings.TrimSpace(execution.RequestID), Reason: strings.TrimSpace(execution.Reason),
	})
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func ensureStableItem(ctx context.Context, store *credentialstore.Store, in PlanInput, candidate credentialacq.CredentialCandidate, expected intake.Item) error {
	inventory, err := store.ListIdentityInventory(ctx, in.TenantID, "")
	if err != nil {
		return err
	}
	current := intake.BuildCandidates(intake.BuildInput{
		TenantID: in.TenantID, SourceKind: in.SourceKind,
		DefaultVendor: in.DefaultVendor, DefaultAuthMode: in.DefaultAuthMode,
		Existing: intake.ExistingFromIdentityMetadata(inventory), Now: in.Now,
	}, []credentialacq.CredentialCandidate{candidate})
	if len(current.Plan.Items) != 1 {
		return ErrExecutionStale
	}
	item := current.Plan.Items[0]
	if item.Action != expected.Action ||
		item.Code != expected.Code ||
		!equalStrings(item.RequiredConfirmations, expected.RequiredConfirmations) {
		return ErrExecutionStale
	}
	if expected.Action == intake.ActionUpdate &&
		(item.ExistingAccountID != expected.ExistingAccountID ||
			item.ExistingCredentialID != expected.ExistingCredentialID ||
			item.ExistingCredentialVersion != expected.ExistingCredentialVersion) {
		return ErrExecutionStale
	}
	return nil
}

func lockTenantIntake(ctx context.Context, tx pgx.Tx, tenantID int64) error {
	lockKey := fmt.Sprintf("account-credential-intake:%d", tenantID)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey)
	return err
}

func insertAdminAudit(ctx context.Context, tx pgx.Tx, in ExecuteInput, tenantID int64, action, targetType string, targetID int64, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reason := optionalString(strings.TrimSpace(in.Reason))
	requestID := optionalString(strings.TrimSpace(in.RequestID))
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    strings.TrimSpace(in.ActorID),
		ActorRole:  strings.TrimSpace(in.ActorRole),
		Action:     action,
		TargetType: targetType,
		TargetID:   &targetID,
		RequestID:  requestID,
		Reason:     reason,
		Payload:    raw,
	})
	return err
}

func initializeHealthTx(ctx context.Context, tx pgx.Tx, tenantID, accountID int64, metadata credentialstore.CredentialMetadata) error {
	health := channelhealth.NewService(
		channelhealth.NewPostgresStore(tx),
		channelhealth.DefaultPolicy(),
		nil,
	)
	_, err := health.EnsureDefaultActive(ctx, channelhealth.ChannelKey{
		TenantID: tenantID, Vendor: metadata.Vendor,
		ProviderAccountID: accountID, AccountCredentialID: metadata.ID,
		CredentialVersion: int(metadata.Version),
	})
	return err
}

func baseExecutionItem(item intake.Item) ExecutionItem {
	return ExecutionItem{
		Index: item.Index, PlannedAction: item.Action,
		Code: item.Code, Message: item.Message,
		ProviderAccountID:   item.ExistingAccountID,
		AccountCredentialID: item.ExistingCredentialID,
		CredentialVersion:   item.ExistingCredentialVersion,
		Subscription:        item.Subscription,
		SystemLabels:        append([]string(nil), item.SystemLabels...),
	}
}

func applyExecutionSubscription(result *ExecutionItem, observation subscriptionprofile.Observation) {
	if result == nil {
		return
	}
	result.Subscription = nil
	result.SystemLabels = nil
	if observation.Empty() {
		return
	}
	public := observation
	public.SubjectRef = ""
	public.WorkspaceRef = ""
	result.Subscription = &public
	if label := observation.Label(); label != "" {
		result.SystemLabels = []string{label}
	}
}
