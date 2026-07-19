package accountintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/crssource"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

var crsSourceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type CRSSource interface {
	Fetch(context.Context, crssource.Input) (crssource.Export, error)
}

type CRSPlanInput struct {
	TenantID     int64
	BaseURL      string
	Username     string
	Password     string
	Destinations map[string]AccountDefaults
	SyncProxies  bool
	ActorID      string
	ActorRole    string
	RequestID    string
	Reason       string
}

type CRSPlanItem struct {
	FlowID       string       `json:"flow_id,omitempty"`
	ExpiresAt    *time.Time   `json:"expires_at,omitempty"`
	SourceType   string       `json:"source_type"`
	SourceIDHint string       `json:"source_id_hint"`
	Name         string       `json:"name"`
	Vendor       string       `json:"vendor,omitempty"`
	AuthMode     string       `json:"auth_mode,omitempty"`
	Status       string       `json:"status"`
	Code         string       `json:"code"`
	Message      string       `json:"message"`
	Warnings     []string     `json:"warnings,omitempty"`
	PlanHash     string       `json:"plan_hash,omitempty"`
	Plan         *intake.Plan `json:"plan,omitempty"`
}

type CRSPlanSummary struct {
	Ready    int `json:"ready"`
	Skipped  int `json:"skipped"`
	Conflict int `json:"conflict"`
	Failed   int `json:"failed"`
}

type CRSPlanResult struct {
	SourceRef string         `json:"source_ref"`
	Items     []CRSPlanItem  `json:"items"`
	Summary   CRSPlanSummary `json:"summary"`
}

type CRSExecuteEntry struct {
	FlowID        string
	PlanHash      string
	Confirmations []string
}

type CRSExecuteInput struct {
	TenantID  int64
	Entries   []CRSExecuteEntry
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
}

type CRSExecutionItem struct {
	FlowID  string           `json:"flow_id"`
	Status  string           `json:"status"`
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Result  *ExecutionResult `json:"result,omitempty"`
}

type CRSExecutionSummary struct {
	Completed int `json:"completed"`
	Conflict  int `json:"conflict"`
	Failed    int `json:"failed"`
}

type CRSExecutionResult struct {
	Items   []CRSExecutionItem  `json:"items"`
	Summary CRSExecutionSummary `json:"summary"`
}

type crsStagedAuxiliary struct {
	Version   int              `json:"version"`
	Proxy     *crssource.Proxy `json:"proxy,omitempty"`
	SourceRef string           `json:"source_ref"`
}

type CRSService struct {
	intake *Service
	staged *StagedStore
	source CRSSource
	now    func() time.Time
}

func NewCRSService(intakeService *Service, staged *StagedStore, source CRSSource) *CRSService {
	return &CRSService{intake: intakeService, staged: staged, source: source}
}

func (s *CRSService) Plan(ctx context.Context, in CRSPlanInput) (CRSPlanResult, error) {
	if s == nil || s.intake == nil || s.staged == nil || s.source == nil {
		return CRSPlanResult{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" || len(in.Destinations) == 0 {
		return CRSPlanResult{}, ErrInvalidInput
	}
	exported, err := s.source.Fetch(ctx, crssource.Input{BaseURL: in.BaseURL, Username: in.Username, Password: in.Password})
	if err != nil {
		return CRSPlanResult{}, err
	}
	sourceRef := exported.SourceRef()
	if sourceRef == "" {
		return CRSPlanResult{}, ErrInvalidInput
	}
	out := CRSPlanResult{SourceRef: sourceRef, Items: make([]CRSPlanItem, 0, len(exported.Accounts))}
	seen := make(map[string]int, len(exported.Accounts))
	for index, account := range exported.Accounts {
		item := s.planAccount(ctx, in, sourceRef, index, account, seen)
		switch item.Status {
		case "ready":
			out.Summary.Ready++
		case "skipped":
			out.Summary.Skipped++
		case "conflict":
			out.Summary.Conflict++
		default:
			out.Summary.Failed++
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *CRSService) planAccount(ctx context.Context, in CRSPlanInput, sourceRef string, index int, source crssource.Account, seen map[string]int) CRSPlanItem {
	hint := sourceIDHint(sourceRef, source.SourceType, source.SourceID)
	item := CRSPlanItem{
		SourceType: source.SourceType, SourceIDHint: hint, Name: safeSourceName(source.Name, hint),
		Vendor: source.Vendor, AuthMode: source.AuthMode, Status: "failed", Code: "source_account_invalid",
		Message: "来源账号不符合接入合同", Warnings: append([]string(nil), source.Warnings...),
	}
	if source.InvalidCode != "" {
		item.Code = source.InvalidCode
		return item
	}
	if !crsSourceTypePattern.MatchString(source.SourceType) || strings.TrimSpace(source.SourceID) == "" {
		return item
	}
	identityKey := source.SourceType + "\x00" + strings.TrimSpace(source.SourceID)
	if first, exists := seen[identityKey]; exists {
		item.Status = "conflict"
		item.Code = "duplicate_source_identity"
		item.Message = fmt.Sprintf("来源中存在重复账号身份，与第 %d 项冲突", first+1)
		return item
	}
	seen[identityKey] = index
	destination, ok := in.Destinations[source.SourceType]
	if !ok {
		item.Code = "destination_missing"
		item.Message = "该来源账号类型没有配置目标 provider 与 channel"
		return item
	}
	planInput, auxiliary, err := buildCRSPlanInput(in.TenantID, destination, sourceRef, source, in.SyncProxies, s.nowTime())
	if err != nil {
		item.Code = "source_mapping_invalid"
		return item
	}
	plan, err := s.intake.Plan(ctx, planInput)
	if err != nil {
		item.Code = "account_plan_failed"
		item.Message = "来源账号无法生成统一接入计划"
		return item
	}
	if plan.Plan.Summary.Conflict > 0 {
		item.Status = "conflict"
	} else if plan.Plan.Summary.Fail > 0 {
		item.Status = "failed"
	} else if plan.Plan.Summary.Create+plan.Plan.Summary.Update == 0 {
		item.Status = "skipped"
	} else {
		item.Status = "ready"
	}
	item.Code = "plan_ready"
	item.Message = "已生成一次性账号接入计划"
	item.PlanHash = plan.PlanHash
	item.Plan = &plan.Plan
	if item.Status != "ready" {
		return item
	}
	staged, err := s.staged.Stage(ctx, StageInput{
		TenantID: in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
		SourceKind: "crs_sync", Vendor: source.Vendor, AuthMode: source.AuthMode,
		PlanInput: planInput, PlanHash: plan.PlanHash, Content: planInput.Content, Auxiliary: auxiliary,
		RequestID: in.RequestID, Reason: in.Reason,
	})
	if err != nil {
		item.Status = "failed"
		item.Code = "credential_stage_failed"
		item.Message = "来源凭据无法进入短期加密暂存"
		item.PlanHash = ""
		item.Plan = nil
		return item
	}
	item.FlowID = staged.ID
	expiresAt := staged.ExpiresAt
	item.ExpiresAt = &expiresAt
	return item
}

func (s *CRSService) Execute(ctx context.Context, in CRSExecuteInput) (CRSExecutionResult, error) {
	if s == nil || s.intake == nil || s.staged == nil {
		return CRSExecutionResult{}, ErrNotConfigured
	}
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" || len(in.Entries) == 0 || len(in.Entries) > intake.MaxCandidates {
		return CRSExecutionResult{}, ErrInvalidInput
	}
	out := CRSExecutionResult{Items: make([]CRSExecutionItem, 0, len(in.Entries))}
	seen := map[string]struct{}{}
	for _, entry := range in.Entries {
		flowID := strings.TrimSpace(entry.FlowID)
		if _, exists := seen[flowID]; exists {
			out.Items = append(out.Items, CRSExecutionItem{FlowID: flowID, Status: "conflict", Code: "duplicate_flow", Message: "同一短期流程不能在一批中执行两次"})
			out.Summary.Conflict++
			continue
		}
		seen[flowID] = struct{}{}
		item := s.executeEntry(ctx, in, entry)
		switch item.Status {
		case "completed":
			out.Summary.Completed++
		case "conflict":
			out.Summary.Conflict++
		default:
			out.Summary.Failed++
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *CRSService) executeEntry(ctx context.Context, batch CRSExecuteInput, entry CRSExecuteEntry) CRSExecutionItem {
	item := CRSExecutionItem{FlowID: strings.TrimSpace(entry.FlowID), Status: "failed", Code: "credential_flow_failed", Message: "短期账号流程执行失败"}
	claimed, err := s.staged.Claim(ctx, batch.TenantID, batch.ActorID, entry.FlowID, entry.PlanHash)
	if err != nil {
		item.Status, item.Code, item.Message = classifyCRSFlowError(err)
		return item
	}
	defer privacy.Zeroize(claimed.Auxiliary)
	defer func() { claimed.PlanInput.Content = "" }()
	auxiliary, err := decodeCRSAuxiliary(claimed.Auxiliary)
	if err != nil {
		_ = s.staged.Finish(ctx, batch.TenantID, batch.ActorID, batch.ActorRole, entry.FlowID, batch.RequestID, batch.Reason, false, ExecutionSummary{Failed: 1})
		item.Code = "source_auxiliary_invalid"
		return item
	}
	if auxiliary.Proxy != nil {
		defer func() { auxiliary.Proxy.Password = "" }()
		claimed.PlanInput.Account.Proxy = &ProxyMaterial{
			Name: "CRS " + auxiliary.SourceRef, Protocol: auxiliary.Proxy.Protocol,
			Host: auxiliary.Proxy.Host, Port: auxiliary.Proxy.Port,
			AuthUsername: auxiliary.Proxy.Username, AuthSecret: auxiliary.Proxy.Password,
			SourceRef: auxiliary.SourceRef,
		}
	}
	result, executeErr := s.intake.Execute(ctx, ExecuteInput{
		PlanInput: claimed.PlanInput, PlanHash: entry.PlanHash, Confirmations: entry.Confirmations,
		ActorID: batch.ActorID, ActorRole: batch.ActorRole, RequestID: batch.RequestID, Reason: batch.Reason,
	})
	success := executeErr == nil && result.Summary.Failed == 0
	finishErr := s.staged.Finish(ctx, batch.TenantID, batch.ActorID, batch.ActorRole, entry.FlowID, batch.RequestID, batch.Reason, success, result.Summary)
	clearProxyMaterial(claimed.PlanInput.Account.Proxy)
	if executeErr != nil {
		item.Status, item.Code, item.Message = classifyCRSFlowError(executeErr)
		return item
	}
	if finishErr != nil {
		for index := range result.Items {
			result.Items[index].Warnings = appendUnique(result.Items[index].Warnings, "credential_flow_finish_log_failed")
		}
	}
	item.Result = &result
	if result.Summary.Conflict > 0 {
		item.Status = "conflict"
		item.Code = "account_conflict"
		item.Message = "账号接入计划需要重新消歧"
	} else if result.Summary.Failed > 0 {
		item.Status = "failed"
		item.Code = "account_execution_failed"
		item.Message = "账号接入执行失败"
	} else {
		item.Status = "completed"
		item.Code = "account_execution_completed"
		item.Message = "账号接入执行完成"
	}
	return item
}

func buildCRSPlanInput(tenantID int64, destination AccountDefaults, sourceRef string, source crssource.Account, syncProxy bool, now time.Time) (PlanInput, json.RawMessage, error) {
	destination.AccountType = source.AccountType
	destination.NamePrefix = combinedAccountName(destination.NamePrefix, source.Name, sourceIDHint(sourceRef, source.SourceType, source.SourceID))
	enabled := source.Enabled
	if destination.Enabled == nil {
		destination.Enabled = &enabled
	} else if !source.Enabled {
		destination.Enabled = &enabled
	}
	if destination.Priority == nil {
		priority := source.Priority
		destination.Priority = &priority
	}
	if destination.CapConcurrency == nil {
		concurrency := source.Concurrency
		if concurrency <= 0 {
			concurrency = 3
		}
		destination.CapConcurrency = &concurrency
	}
	identity := sourceIdentity(sourceRef, source.SourceType, source.SourceID)
	credentials := make(map[string]any, len(source.Credentials)+4)
	for key, value := range source.Credentials {
		credentials[key] = value
	}
	credentials["vendor"] = source.Vendor
	credentials["auth_mode"] = source.AuthMode
	credentials["external_account_id"] = identity
	if email := firstMapString(source.Credentials, "email", "account_email"); email != "" {
		credentials["external_account_email"] = email
	}
	content, err := json.Marshal(credentials)
	if err != nil {
		return PlanInput{}, nil, ErrInvalidInput
	}
	proxyFingerprint := ""
	if syncProxy && source.Proxy != nil {
		proxyFingerprint = proxyDescriptorFingerprint(*source.Proxy)
	}
	extra, err := mergeCRSExtra(destination.Extra, map[string]any{
		"source_kind": "crs_sync", "source_ref": sourceRef, "source_type": source.SourceType,
		"source_account_hint": sourceIDHint(sourceRef, source.SourceType, source.SourceID),
		"source_schedulable":  source.Schedulable, "proxy_binding_fingerprint": proxyFingerprint,
	})
	if err != nil {
		return PlanInput{}, nil, err
	}
	destination.Extra = extra
	auxiliary, err := json.Marshal(crsStagedAuxiliary{Version: 1, Proxy: proxyForStage(syncProxy, source.Proxy), SourceRef: sourceRef})
	if err != nil {
		return PlanInput{}, nil, ErrInvalidInput
	}
	return PlanInput{
		TenantID: tenantID, SourceKind: intake.SourceCRSSync,
		DefaultVendor: source.Vendor, DefaultAuthMode: source.AuthMode,
		Content: string(content), Account: destination, Now: now,
	}, auxiliary, nil
}

func decodeCRSAuxiliary(raw json.RawMessage) (crsStagedAuxiliary, error) {
	var auxiliary crsStagedAuxiliary
	if json.Unmarshal(raw, &auxiliary) != nil || auxiliary.Version != 1 || auxiliary.SourceRef == "" {
		return crsStagedAuxiliary{}, ErrInvalidInput
	}
	return auxiliary, nil
}

func classifyCRSFlowError(err error) (string, string, string) {
	switch {
	case errors.Is(err, ErrStagedCredentialNotFound):
		return "failed", "credential_flow_not_found", "短期账号流程不存在"
	case errors.Is(err, ErrStagedCredentialExpired):
		return "failed", "credential_flow_expired", "短期账号流程已过期"
	case errors.Is(err, ErrStagedCredentialReplay):
		return "conflict", "credential_flow_replayed", "短期账号流程已被领取"
	case errors.Is(err, ErrPlanChanged):
		return "conflict", "account_plan_changed", "账号状态已变化，需要重新预检"
	default:
		return "failed", "credential_flow_failed", "短期账号流程执行失败"
	}
}

func mergeCRSExtra(raw json.RawMessage, additions map[string]any) (json.RawMessage, error) {
	merged := map[string]any{}
	if len(raw) > 0 && json.Unmarshal(raw, &merged) != nil {
		return nil, ErrInvalidInput
	}
	for key, value := range additions {
		merged[key] = value
	}
	return json.Marshal(merged)
}

func proxyForStage(enabled bool, proxy *crssource.Proxy) *crssource.Proxy {
	if !enabled || proxy == nil {
		return nil
	}
	copy := *proxy
	return &copy
}

func clearProxyMaterial(proxy *ProxyMaterial) {
	if proxy == nil {
		return
	}
	proxy.AuthSecret = ""
}

func sourceIdentity(sourceRef, sourceType, sourceID string) string {
	sum := sha256.Sum256([]byte(sourceRef + "\x00" + sourceType + "\x00" + strings.TrimSpace(sourceID)))
	return "crs_" + hex.EncodeToString(sum[:])
}

func sourceIDHint(sourceRef, sourceType, sourceID string) string {
	identity := sourceIdentity(sourceRef, sourceType, sourceID)
	if len(identity) <= 16 {
		return identity
	}
	return identity[:16]
}

func proxyDescriptorFingerprint(proxy crssource.Proxy) string {
	value := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(proxy.Protocol)), strings.ToLower(strings.TrimSpace(proxy.Host)),
		fmt.Sprint(proxy.Port), strings.TrimSpace(proxy.Username),
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func combinedAccountName(prefix, sourceName, hint string) string {
	prefix = strings.TrimSpace(prefix)
	name := safeSourceName(sourceName, hint)
	combined := name
	if prefix != "" {
		combined = prefix + "-" + name
	}
	if len(combined) > 190 {
		combined = combined[:190]
	}
	return strings.TrimSpace(combined)
}

func safeSourceName(name, hint string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return hint
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func firstMapString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *CRSService) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
