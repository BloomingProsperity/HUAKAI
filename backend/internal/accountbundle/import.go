package accountbundle

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type preparedImport struct {
	result   ImportPlan
	content  payloadContent
	accounts map[string]PortableAccount
	proxies  map[string]PortableProxy
}

func (s *Service) PlanImport(ctx context.Context, in ImportPlanInput) (ImportPlan, error) {
	prepared, err := s.prepareImport(ctx, in)
	if err != nil {
		return ImportPlan{}, err
	}
	defer zeroPortableContent(&prepared.content)
	return prepared.result, nil
}

func (s *Service) prepareImport(ctx context.Context, in ImportPlanInput) (preparedImport, error) {
	defer func() { in.Password = "" }()
	if err := s.ready(); err != nil {
		return preparedImport{}, err
	}
	if err := validateOperator(in.TenantID, in.ActorScopeTenantID, in.ActorID, in.ActorRole); err != nil {
		return preparedImport{}, err
	}
	if len(in.Destinations) == 0 {
		return preparedImport{}, ErrInvalidInput
	}
	content, err := open(in.Envelope, in.Password)
	if err != nil {
		return preparedImport{}, err
	}
	if len(content.Accounts) == 0 || len(content.Accounts) > MaxAccounts {
		zeroPortableContent(&content)
		return preparedImport{}, ErrInvalidInput
	}
	proxies := make(map[string]PortableProxy, len(content.Proxies))
	for _, proxy := range content.Proxies {
		if strings.TrimSpace(proxy.Ref) == "" {
			zeroPortableContent(&content)
			return preparedImport{}, ErrIntegrity
		}
		if _, exists := proxies[proxy.Ref]; exists {
			zeroPortableContent(&content)
			return preparedImport{}, ErrIntegrity
		}
		proxies[proxy.Ref] = proxy
	}
	out := ImportPlan{ContractVersion: contractVersion, Items: make([]ImportPlanItem, 0, len(content.Accounts))}
	accounts := make(map[string]PortableAccount, len(content.Accounts))
	for index, account := range content.Accounts {
		item := s.planImportAccount(ctx, in.TenantID, index, account, proxies, in.Destinations)
		if _, exists := accounts[account.Ref]; exists || strings.TrimSpace(account.Ref) == "" {
			item.Status = "conflict"
			item.Code = "duplicate_account_ref"
			item.Message = "迁移包存在重复账号引用"
		}
		accounts[account.Ref] = account
		addImportPlanSummary(&out, item.Status)
		out.Items = append(out.Items, item)
	}
	hash, err := stableHash(struct {
		ContractVersion  string                 `json:"contract_version"`
		CiphertextSHA256 string                 `json:"ciphertext_sha256"`
		Destinations     map[string]Destination `json:"destinations"`
		Items            []ImportPlanItem       `json:"items"`
	}{contractVersion, in.Envelope.CiphertextSHA256, in.Destinations, out.Items})
	if err != nil {
		zeroPortableContent(&content)
		return preparedImport{}, err
	}
	out.BundleHash = hash
	return preparedImport{result: out, content: content, accounts: accounts, proxies: proxies}, nil
}

func (s *Service) planImportAccount(ctx context.Context, tenantID int64, index int, account PortableAccount, proxies map[string]PortableProxy, destinations map[string]Destination) ImportPlanItem {
	key := destinationKey(account.SourceProviderID, account.SourceChannelID)
	item := ImportPlanItem{
		Index: index, AccountRef: account.Ref, Name: account.Config.Name, DestinationKey: key,
		Status: "failed", Code: "bundle_account_invalid", Message: "迁移包账号不符合导入合同",
	}
	destination, ok := destinations[key]
	if !ok || destination.ProviderID <= 0 || destination.ChannelID <= 0 {
		item.Code = "destination_missing"
		item.Message = "来源 provider/channel 没有配置目标映射"
		return item
	}
	defaults, err := defaultsFromPortable(account.Config, destination)
	if err != nil {
		return item
	}
	if account.ProxyRef != "" {
		proxy, exists := proxies[account.ProxyRef]
		if !exists {
			item.Code = "proxy_mapping_missing"
			item.Message = "账号引用的代理不在迁移包中"
			return item
		}
		defaults.Proxy = &accountintake.ProxyMaterial{
			Name: proxy.Name, Protocol: proxy.Protocol, Host: proxy.Host, Port: proxy.Port,
			AuthUsername: proxy.AuthUsername, AuthSecret: proxy.AuthSecret, SourceRef: proxy.Ref,
		}
	}
	content, err := credentialContent(account.Credential)
	if err != nil {
		return item
	}
	defer privacy.Zeroize(content)
	planInput := accountintake.PlanInput{
		TenantID: tenantID, SourceKind: intake.SourceJSON,
		DefaultVendor: account.Credential.Vendor, DefaultAuthMode: account.Credential.AuthMode,
		Content: string(content), Account: defaults,
	}
	plan, err := s.intake.Plan(ctx, planInput)
	if err != nil {
		item.Code = "account_plan_failed"
		item.Message = "账号无法生成统一接入计划"
		return item
	}
	item.PlanHash = plan.PlanHash
	item.Plan = &plan.Plan
	if len(plan.Plan.Items) != 1 {
		item.Code = "credential_count_invalid"
		return item
	}
	planned := plan.Plan.Items[0]
	item.RequiredConfirmations = append([]string(nil), planned.RequiredConfirmations...)
	switch planned.Action {
	case intake.ActionCreate:
		exists, err := s.accountNameExists(ctx, tenantID, account.Config.Name)
		if err != nil {
			item.Code = "account_name_check_failed"
			item.Message = "无法确认目标租户账号名称是否冲突"
			return item
		}
		if exists {
			item.Status = "conflict"
			item.Code = "account_name_conflict"
			item.Message = "目标租户已有同名账号，但上游身份不匹配"
			return item
		}
		item.Status = "ready"
		item.Code = "create_ready"
		item.Message = "将创建账号并恢复公开配置、加密凭据和代理映射"
	case intake.ActionUpdate:
		updatedAt, compatible, err := s.existingAccountState(ctx, tenantID, planned.ExistingAccountID, destination, account.Config.AccountType)
		if err != nil || !compatible {
			item.Status = "conflict"
			item.Code = "existing_account_destination_conflict"
			item.Message = "已有上游身份账号与目标 provider/channel 或账号类型不一致"
			return item
		}
		item.ExistingAccountUpdatedAt = &updatedAt
		item.RequiredConfirmations = appendUnique(item.RequiredConfirmations, "confirm_account_config_replace")
		item.Status = "ready"
		item.Code = "update_ready"
		item.Message = "将原子轮换凭据并替换已有账号的稳定公开配置"
	case intake.ActionSkip:
		item.Status = "skipped"
		item.Code = planned.Code
		item.Message = planned.Message
	case intake.ActionConflict:
		item.Status = "conflict"
		item.Code = planned.Code
		item.Message = planned.Message
	default:
		item.Status = "failed"
		item.Code = planned.Code
		item.Message = planned.Message
	}
	return item
}

func (s *Service) ExecuteImport(ctx context.Context, in ImportExecuteInput) (ImportExecutionResult, error) {
	prepared, err := s.prepareImport(ctx, in.ImportPlanInput)
	if err != nil {
		return ImportExecutionResult{}, err
	}
	defer zeroPortableContent(&prepared.content)
	if !compareHash(strings.TrimSpace(in.BundleHash), prepared.result.BundleHash) {
		return ImportExecutionResult{}, ErrPlanChanged
	}
	if len(in.Entries) == 0 || len(in.Entries) > len(prepared.result.Items) {
		return ImportExecutionResult{}, ErrInvalidInput
	}
	plannedByRef := make(map[string]ImportPlanItem, len(prepared.result.Items))
	for _, item := range prepared.result.Items {
		plannedByRef[item.AccountRef] = item
	}
	out := ImportExecutionResult{BundleHash: prepared.result.BundleHash, Items: make([]ImportExecutionItem, 0, len(in.Entries))}
	seen := make(map[string]struct{}, len(in.Entries))
	for _, entry := range in.Entries {
		ref := strings.TrimSpace(entry.AccountRef)
		if _, exists := seen[ref]; exists {
			out.Items = append(out.Items, ImportExecutionItem{AccountRef: ref, Status: "conflict", Code: "duplicate_account_entry", Message: "同一账号不能在一批中执行两次"})
			out.Conflict++
			continue
		}
		seen[ref] = struct{}{}
		planned, ok := plannedByRef[ref]
		account, accountOK := prepared.accounts[ref]
		if !ok || !accountOK || planned.Status != "ready" || !compareHash(entry.PlanHash, planned.PlanHash) {
			out.Items = append(out.Items, ImportExecutionItem{AccountRef: ref, Status: "conflict", Code: "account_plan_changed", Message: "账号不在可执行计划中或计划已经变化"})
			out.Conflict++
			continue
		}
		missing := missingConfirmations(planned.RequiredConfirmations, entry.Confirmations)
		if len(missing) > 0 {
			out.Items = append(out.Items, ImportExecutionItem{AccountRef: ref, Status: "conflict", Code: "confirmation_required", Message: "缺少确认：" + strings.Join(missing, ",")})
			out.Conflict++
			continue
		}
		item := s.executeImportAccount(ctx, in, entry, planned, account, prepared.proxies)
		switch item.Status {
		case "completed":
			out.Completed++
		case "skipped":
			out.Skipped++
		case "conflict":
			out.Conflict++
		default:
			out.Failed++
		}
		out.Items = append(out.Items, item)
	}
	if out.Completed > 0 {
		if err := s.recordBundleEvent(ctx, in.TenantID, in.ActorID, in.ActorRole, "account_bundle_imported", in.RequestID, in.Reason, map[string]any{
			"completed": out.Completed, "skipped": out.Skipped, "conflict": out.Conflict,
			"failed": out.Failed, "bundle_hash": out.BundleHash,
		}); err != nil {
			for index := range out.Items {
				if out.Items[index].Result == nil {
					continue
				}
				for resultIndex := range out.Items[index].Result.Items {
					out.Items[index].Result.Items[resultIndex].Warnings = append(out.Items[index].Result.Items[resultIndex].Warnings, "account_bundle_log_write_failed")
				}
			}
		}
	}
	return out, nil
}

func (s *Service) executeImportAccount(ctx context.Context, batch ImportExecuteInput, entry ImportExecuteEntry, planned ImportPlanItem, account PortableAccount, proxies map[string]PortableProxy) ImportExecutionItem {
	item := ImportExecutionItem{AccountRef: account.Ref, Status: "failed", Code: "account_import_failed", Message: "账号迁移执行失败"}
	destination := batch.Destinations[planned.DestinationKey]
	defaults, err := defaultsFromPortable(account.Config, destination)
	if err != nil {
		return item
	}
	if account.ProxyRef != "" {
		proxy, ok := proxies[account.ProxyRef]
		if !ok {
			item.Code = "proxy_mapping_missing"
			return item
		}
		defaults.Proxy = &accountintake.ProxyMaterial{
			Name: proxy.Name, Protocol: proxy.Protocol, Host: proxy.Host, Port: proxy.Port,
			AuthUsername: proxy.AuthUsername, AuthSecret: proxy.AuthSecret, SourceRef: proxy.Ref,
		}
	}
	content, err := credentialContent(account.Credential)
	if err != nil {
		return item
	}
	defer privacy.Zeroize(content)
	result, err := s.intake.Execute(ctx, accountintake.ExecuteInput{
		PlanInput: accountintake.PlanInput{
			TenantID: batch.TenantID, SourceKind: intake.SourceJSON,
			DefaultVendor: account.Credential.Vendor, DefaultAuthMode: account.Credential.AuthMode,
			Content: string(content), Account: defaults,
		},
		PlanHash: entry.PlanHash, Confirmations: withoutConfigConfirmation(entry.Confirmations),
		ReplaceExistingConfig:    planned.ExistingAccountUpdatedAt != nil,
		ExpectedAccountUpdatedAt: planned.ExistingAccountUpdatedAt,
		ActorID:                  batch.ActorID, ActorRole: batch.ActorRole, RequestID: batch.RequestID, Reason: batch.Reason,
	})
	if err != nil {
		if errors.Is(err, accountintake.ErrPlanChanged) || errors.Is(err, accountintake.ErrExecutionStale) {
			item.Status = "conflict"
			item.Code = "account_plan_changed"
			item.Message = "执行前账号或凭据状态已变化，请重新预检"
		}
		return item
	}
	item.Result = &result
	if result.Summary.Conflict > 0 {
		item.Status = "conflict"
		item.Code = "account_conflict"
		item.Message = "账号仍需人工消歧"
	} else if result.Summary.Failed > 0 {
		item.Status = "failed"
		item.Code = "account_execution_failed"
	} else {
		item.Status = "completed"
		item.Code = "account_imported"
		item.Message = "账号公开配置、凭据和代理映射已恢复"
	}
	return item
}

func defaultsFromPortable(config PublicConfig, destination Destination) (accountintake.AccountDefaults, error) {
	if destination.ProviderID <= 0 || destination.ChannelID <= 0 || strings.TrimSpace(config.Name) == "" {
		return accountintake.AccountDefaults{}, ErrInvalidInput
	}
	enabled := config.Enabled
	concurrency, queueSticky, queueFallback := config.CapConcurrency, config.CapQueueSticky, config.CapQueueFallback
	priority, weight := config.Priority, config.StaticWeight
	rpm, tpm, cost := config.RPMLimit, config.TPMLimit, config.WindowCostLimitCents
	maxSessions := config.MaxSessions
	disableCooling, tlsRotate := config.DisableCooling, config.TLSFingerprintRotate
	customEnabled, poolMode, tempEnabled := config.CustomErrorCodesEnabled, config.PoolMode, config.TempUnschedulableEnabled
	return accountintake.AccountDefaults{
		ProviderID: destination.ProviderID, ChannelID: destination.ChannelID,
		ExactName: strings.TrimSpace(config.Name), AccountType: strings.TrimSpace(config.AccountType),
		Enabled: &enabled, ExpiresAt: config.ExpiresAt,
		CapConcurrency: &concurrency, CapQueueSticky: &queueSticky, CapQueueFallback: &queueFallback,
		Priority: &priority, StaticWeight: &weight, UpstreamCostRatio: config.UpstreamCostRatio,
		ProbeModel: config.ProbeModel, Tags: append([]string(nil), config.Tags...),
		Extra:           append(json.RawMessage(nil), config.Extra...),
		ModelAllowList:  append([]string(nil), config.ModelAllowList...),
		CapabilityFlags: append([]string(nil), config.CapabilityFlags...),
		RPMLimit:        &rpm, TPMLimit: &tpm, WindowCostLimitCents: &cost, MaxSessions: &maxSessions,
		DisableCooling: &disableCooling, RefreshLeadSeconds: config.RefreshLeadSeconds,
		TLSFingerprintRotate: &tlsRotate, CustomErrorCodesEnabled: &customEnabled,
		CustomErrorCodes: append([]int32(nil), config.CustomErrorCodes...), PoolMode: &poolMode,
		TempUnschedulableEnabled: &tempEnabled,
		TempUnschedulableRules:   append(json.RawMessage(nil), config.TempUnschedulableRules...),
	}, nil
}

func credentialContent(credential PortableCredential) ([]byte, error) {
	var fields map[string]any
	if json.Unmarshal(credential.Payload, &fields) != nil || fields == nil {
		return nil, ErrIntegrity
	}
	fields["vendor"] = strings.TrimSpace(credential.Vendor)
	fields["auth_mode"] = strings.TrimSpace(credential.AuthMode)
	for key, value := range map[string]string{
		"external_account_id":      credential.ExternalAccountID,
		"external_subject_id":      credential.ExternalSubjectID,
		"external_account_email":   credential.ExternalAccountEmail,
		"external_identity_source": credential.ExternalIdentitySource,
	} {
		if strings.TrimSpace(value) != "" {
			fields[key] = strings.TrimSpace(value)
		}
	}
	return json.Marshal(fields)
}

func (s *Service) accountNameExists(ctx context.Context, tenantID int64, name string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM provider_accounts WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL
)`, tenantID, strings.TrimSpace(name)).Scan(&exists)
	return exists, err
}

func (s *Service) existingAccountState(ctx context.Context, tenantID, accountID int64, destination Destination, accountType string) (time.Time, bool, error) {
	var providerID, channelID int64
	var actualType string
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT provider_id, channel_id, account_type, updated_at
FROM provider_accounts WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`, tenantID, accountID).
		Scan(&providerID, &channelID, &actualType, &updatedAt)
	if err != nil {
		return time.Time{}, false, err
	}
	return updatedAt.UTC(), providerID == destination.ProviderID && channelID == destination.ChannelID && actualType == accountType, nil
}

func addImportPlanSummary(plan *ImportPlan, status string) {
	switch status {
	case "ready":
		plan.Ready++
	case "skipped":
		plan.Skipped++
	case "conflict":
		plan.Conflict++
	default:
		plan.Failed++
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func missingConfirmations(required, provided []string) []string {
	set := make(map[string]struct{}, len(provided))
	for _, value := range provided {
		set[strings.TrimSpace(value)] = struct{}{}
	}
	out := make([]string, 0)
	for _, value := range required {
		if _, ok := set[value]; !ok {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func withoutConfigConfirmation(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "confirm_account_config_replace" {
			out = append(out, value)
		}
	}
	return out
}
