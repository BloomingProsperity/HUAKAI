package accountintake

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
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
			candidate, err = s.prepareExecutionCandidate(ctx, candidate)
			if err != nil {
				result.Status = StatusFailed
				result.Code = "agent_task_registration_failed"
				result.Message = "Agent Identity 任务登记失败，账号未写入"
				break
			}
			if candidate.AuthMode == credentialstore.AuthModeCodexAgent {
				privacy.Zeroize(prepared.candidates[item.Index].Payload)
				prepared.candidates[item.Index] = candidate
			}
			if item.Action == intake.ActionCreate {
				result = s.executeCreate(ctx, prepared, in, item, candidate)
			} else {
				result = s.executeUpdate(ctx, prepared, in, item, candidate)
			}
		default:
			result.Status = StatusFailed
			result.Code = "planned_action_invalid"
			result.Message = "预检动作无效"
		}
		addExecutionSummary(&out.Summary, result.Status)
		out.Items = append(out.Items, result)
	}
	return out, nil
}

func (s *Service) prepareExecutionCandidate(ctx context.Context, candidate credentialacq.CredentialCandidate) (credentialacq.CredentialCandidate, error) {
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

func (s *Service) executeCreate(ctx context.Context, prepared preparedPlan, in ExecuteInput, expected intake.Item, candidate credentialacq.CredentialCandidate) ExecutionItem {
	result := baseExecutionItem(expected)
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
		if err := ensureStableItem(ctx, txStore, prepared.input, candidate, expected); err != nil {
			return err
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
		return insertAdminAudit(ctx, tx, in, prepared.input.TenantID, "create_provider_account", "provider_account", accountID, map[string]any{
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
	s.initializeHealth(ctx, &result, prepared.input.TenantID, accountID, metadata)
	return result
}

func (s *Service) executeUpdate(ctx context.Context, prepared preparedPlan, in ExecuteInput, expected intake.Item, candidate credentialacq.CredentialCandidate) ExecutionItem {
	result := baseExecutionItem(expected)
	var metadata credentialstore.CredentialMetadata
	err := s.credentials.WithTransaction(ctx, func(txStore *credentialstore.Store, database db.DBTX) error {
		tx, ok := database.(pgx.Tx)
		if !ok {
			return errors.New("account intake transaction is not pgx.Tx")
		}
		if err := lockTenantIntake(ctx, tx, prepared.input.TenantID); err != nil {
			return err
		}
		if err := ensureStableItem(ctx, txStore, prepared.input, candidate, expected); err != nil {
			return err
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
		metadata, err = txStore.Rotate(ctx, credentialstore.RotateCredentialInput{
			TenantID:               prepared.input.TenantID,
			ProviderAccountID:      expected.ExistingAccountID,
			CredentialID:           expected.ExistingCredentialID,
			ExpectedVersion:        &version,
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
		return insertAdminAudit(ctx, tx, in, prepared.input.TenantID, "rotate_account_credential", "account_credential", metadata.ID, map[string]any{
			"provider_account_id": expected.ExistingAccountID,
			"vendor":              candidate.Vendor,
			"auth_mode":           candidate.AuthMode,
			"credential_version":  metadata.Version,
			"batch_intake":        true,
			"subscription_label":  candidate.Subscription.Label(),
			"subscription_status": candidate.Subscription.Status,
			"proxy_id":            proxyID,
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
	s.initializeHealth(ctx, &result, prepared.input.TenantID, expected.ExistingAccountID, metadata)
	return result
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

func (s *Service) initializeHealth(ctx context.Context, result *ExecutionItem, tenantID, accountID int64, metadata credentialstore.CredentialMetadata) {
	if s.health == nil {
		result.Warnings = appendUnique(result.Warnings, "channel_health_dependency_unset")
		return
	}
	_, err := s.health.EnsureDefaultActive(ctx, channelhealth.ChannelKey{
		TenantID: tenantID, Vendor: metadata.Vendor,
		ProviderAccountID: accountID, AccountCredentialID: metadata.ID,
		CredentialVersion: int(metadata.Version),
	})
	if err != nil {
		result.Warnings = appendUnique(result.Warnings, "channel_health_initialization_failed")
		return
	}
	result.ChannelHealthInitialized = true
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

func accountName(account AccountDefaults, index int) string {
	if account.ExactName != "" {
		return account.ExactName
	}
	return fmt.Sprintf("%s-%03d", strings.TrimSpace(account.NamePrefix), index+1)
}

func confirmationSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			out[cleaned] = struct{}{}
		}
	}
	return out
}

func validPlanHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validateConfirmations(values []string) error {
	allowed := map[string]struct{}{
		"confirm_weak_identity":            {},
		"confirm_mixed_channel_risk":       {},
		"confirm_unverified_account_match": {},
		"confirm_weak_identity_match":      {},
		"confirm_credential_rotation":      {},
		"confirm_credential_recovery":      {},
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			return fmt.Errorf("%w: unknown confirmation %q", ErrInvalidInput, value)
		}
	}
	return nil
}

func missingConfirmations(required []string, confirmed map[string]struct{}) []string {
	out := make([]string, 0)
	for _, value := range required {
		if _, ok := confirmed[value]; !ok {
			out = appendUnique(out, value)
		}
	}
	return out
}

func hasConfirmation(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func executionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrExecutionStale), errors.Is(err, credentialstore.ErrCredentialVersionConflict):
		return "plan_stale"
	case errors.Is(err, accountcreate.ErrMixedRiskConfirmRequired):
		return "mixed_channel_risk_confirmation_required"
	case errors.Is(err, accountcreate.ErrProtocolIncompatible):
		return "provider_protocol_incompatible"
	default:
		return "execution_failed"
	}
}

func executionErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrExecutionStale), errors.Is(err, credentialstore.ErrCredentialVersionConflict):
		return "执行前账号或凭据状态已变化，请重新预检"
	case errors.Is(err, accountcreate.ErrMixedRiskConfirmRequired):
		return "执行时发现新的渠道混用风险，请重新预检并明确确认"
	case errors.Is(err, accountcreate.ErrProtocolIncompatible):
		return "执行时 provider 协议或账号配置已不兼容，请重新预检"
	default:
		return "该项写入失败，事务已回滚且未留下部分数据"
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func addExecutionSummary(summary *ExecutionSummary, status ExecutionStatus) {
	switch status {
	case StatusCreated:
		summary.Created++
	case StatusUpdated:
		summary.Updated++
	case StatusSkipped:
		summary.Skipped++
	case StatusConflict:
		summary.Conflict++
	case StatusFailed:
		summary.Failed++
	}
}
