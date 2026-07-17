package accountsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type IntakeService interface {
	PlanCandidate(context.Context, accountintake.CandidatePlanInput) (accountintake.PlanResult, error)
	ExecuteCandidate(context.Context, accountintake.CandidateExecuteInput) (accountintake.ExecutionResult, error)
}

type SessionLoader interface {
	Load(context.Context, int64, string) (Loaded, error)
}

type Mapping struct {
	SourceProvider string `json:"source_provider,omitempty"`
	Vendor         string `json:"vendor,omitempty"`
	ProviderID     int64  `json:"provider_id"`
	ChannelID      int64  `json:"channel_id"`
	AccountType    string `json:"account_type,omitempty"`
	NamePrefix     string `json:"name_prefix,omitempty"`
}

type PlanInput struct {
	TenantID  int64
	SessionID string
	Mappings  []Mapping
}

type ItemPlan struct {
	Index          int                       `json:"index"`
	SourceName     string                    `json:"source_name"`
	SourceProvider string                    `json:"source_provider,omitempty"`
	Vendor         string                    `json:"vendor"`
	AuthMode       string                    `json:"auth_mode"`
	PlanHash       string                    `json:"plan_hash,omitempty"`
	Plan           *accountintake.PlanResult `json:"plan,omitempty"`
	Code           string                    `json:"code"`
	Message        string                    `json:"message"`
}

type BatchPlan struct {
	SessionID string     `json:"intake_session_id"`
	ExpiresAt string     `json:"expires_at"`
	Items     []ItemPlan `json:"items"`
}

type ExecuteSelection struct {
	Index         int      `json:"index"`
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
}

type ExecuteInput struct {
	PlanInput
	ExpectedSource intake.SourceKind
	Selections     []ExecuteSelection
	ActorID        string
	ActorRole      string
	RequestID      string
	Reason         string
}

type ItemExecution struct {
	Index   int                            `json:"index"`
	Status  accountintake.ExecutionStatus  `json:"status"`
	Code    string                         `json:"code"`
	Message string                         `json:"message"`
	Result  *accountintake.ExecutionResult `json:"result,omitempty"`
}

type BatchExecution struct {
	SessionID string          `json:"intake_session_id"`
	Items     []ItemExecution `json:"items"`
}

type Service struct {
	store  SessionLoader
	intake IntakeService
}

func NewService(store SessionLoader, intake IntakeService) *Service {
	return &Service{store: store, intake: intake}
}

func (s *Service) Plan(ctx context.Context, in PlanInput) (BatchPlan, error) {
	if s == nil || s.store == nil || s.intake == nil || in.TenantID <= 0 {
		return BatchPlan{}, ErrInvalidInput
	}
	loaded, err := s.store.Load(ctx, in.TenantID, in.SessionID)
	if err != nil {
		return BatchPlan{}, err
	}
	defer ZeroizeItems(loaded.Items)
	out := BatchPlan{SessionID: loaded.Session.ID, ExpiresAt: loaded.Session.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Items: make([]ItemPlan, 0, len(loaded.Items))}
	for index, item := range loaded.Items {
		entry := baseItemPlan(index, item)
		defaults, mapErr := ResolveDefaults(item, in.Mappings)
		if mapErr != nil {
			entry.Code = "mapping_required"
			entry.Message = mapErr.Error()
			out.Items = append(out.Items, entry)
			continue
		}
		result, planErr := s.intake.PlanCandidate(ctx, accountintake.CandidatePlanInput{
			TenantID: in.TenantID, SourceKind: loaded.Session.SourceKind,
			Candidate: item.Candidate, SourceCommitment: itemCommitment(loaded.Session.SourceCommitment, index),
			Account: defaults,
		})
		if planErr != nil {
			entry.Code = "plan_failed"
			entry.Message = "账号候选预检失败"
		} else {
			entry.PlanHash = result.PlanHash
			entry.Plan = &result
			entry.Code = "planned"
			entry.Message = "账号候选已完成预检"
		}
		out.Items = append(out.Items, entry)
	}
	return out, nil
}

func (s *Service) Execute(ctx context.Context, in ExecuteInput) (BatchExecution, error) {
	if s == nil || s.store == nil || s.intake == nil || in.TenantID <= 0 || !validSource(in.ExpectedSource) || len(in.Selections) == 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" {
		return BatchExecution{}, ErrInvalidInput
	}
	loaded, err := s.store.Load(ctx, in.TenantID, in.SessionID)
	if err != nil {
		return BatchExecution{}, err
	}
	defer ZeroizeItems(loaded.Items)
	if loaded.Session.SourceKind != in.ExpectedSource {
		return BatchExecution{}, ErrSessionSource
	}
	seen := make(map[int]struct{}, len(in.Selections))
	out := BatchExecution{SessionID: loaded.Session.ID, Items: make([]ItemExecution, 0, len(in.Selections))}
	for _, selection := range in.Selections {
		entry := ItemExecution{Index: selection.Index}
		if selection.Index < 0 || selection.Index >= len(loaded.Items) {
			entry.Status, entry.Code, entry.Message = accountintake.StatusFailed, "index_invalid", "账号候选序号无效"
			out.Items = append(out.Items, entry)
			continue
		}
		if _, duplicate := seen[selection.Index]; duplicate {
			entry.Status, entry.Code, entry.Message = accountintake.StatusFailed, "duplicate_selection", "同一账号候选不能重复执行"
			out.Items = append(out.Items, entry)
			continue
		}
		seen[selection.Index] = struct{}{}
		item := loaded.Items[selection.Index]
		defaults, mapErr := ResolveDefaults(item, in.Mappings)
		if mapErr != nil {
			entry.Status, entry.Code, entry.Message = accountintake.StatusFailed, "mapping_required", mapErr.Error()
			out.Items = append(out.Items, entry)
			continue
		}
		result, executeErr := s.intake.ExecuteCandidate(ctx, accountintake.CandidateExecuteInput{
			CandidatePlanInput: accountintake.CandidatePlanInput{
				TenantID: in.TenantID, SourceKind: loaded.Session.SourceKind, Candidate: item.Candidate,
				SourceCommitment: itemCommitment(loaded.Session.SourceCommitment, selection.Index), Account: defaults,
			},
			PlanHash: selection.PlanHash, Confirmations: selection.Confirmations,
			ActorID: in.ActorID, ActorRole: in.ActorRole, RequestID: in.RequestID, Reason: in.Reason,
			Finalize: func(context.Context, db.DBTX, accountintake.ExecutionItem) error { return nil },
		})
		if executeErr != nil {
			entry.Status, entry.Code, entry.Message = accountintake.StatusFailed, "execute_failed", "账号候选执行失败，请重新预检"
		} else if len(result.Items) != 1 {
			entry.Status, entry.Code, entry.Message = accountintake.StatusFailed, "execution_result_invalid", "账号候选执行结果数量异常"
		} else {
			entry.Status, entry.Code, entry.Message = result.Items[0].Status, result.Items[0].Code, result.Items[0].Message
			entry.Result = &result
		}
		out.Items = append(out.Items, entry)
	}
	return out, nil
}

func validSource(source intake.SourceKind) bool {
	return source == intake.SourceCRSSync || source == intake.SourceAccountRecovery
}

func ResolveDefaults(item Item, mappings []Mapping) (accountintake.AccountDefaults, error) {
	var matched *Mapping
	for index := range mappings {
		mapping := &mappings[index]
		providerMatch := strings.TrimSpace(item.Template.SourceProvider) != "" && strings.EqualFold(strings.TrimSpace(mapping.SourceProvider), strings.TrimSpace(item.Template.SourceProvider))
		vendorMatch := strings.TrimSpace(mapping.SourceProvider) == "" && strings.EqualFold(strings.TrimSpace(mapping.Vendor), strings.TrimSpace(item.Candidate.Vendor))
		if !providerMatch && !vendorMatch {
			continue
		}
		if matched != nil {
			return accountintake.AccountDefaults{}, fmt.Errorf("同一来源账号命中多个目标映射")
		}
		matched = mapping
	}
	if matched == nil || matched.ProviderID <= 0 || matched.ChannelID <= 0 {
		return accountintake.AccountDefaults{}, fmt.Errorf("来源 provider/vendor 尚未映射到目标 provider 和 channel")
	}
	name := strings.TrimSpace(item.Template.Name)
	if prefix := strings.TrimSpace(matched.NamePrefix); prefix != "" {
		name = prefix + "-" + name
	}
	if name == "" {
		name = fmt.Sprintf("imported-%s", shortCommitment(item.Candidate.Payload))
	}
	accountType := strings.TrimSpace(matched.AccountType)
	if accountType == "" {
		accountType = strings.TrimSpace(item.Template.AccountType)
	}
	enabled := item.Template.Enabled
	return accountintake.AccountDefaults{
		ProviderID: matched.ProviderID, ChannelID: matched.ChannelID, Name: name,
		AccountType: accountType, Enabled: &enabled, CapConcurrency: item.Template.CapConcurrency,
		Priority: item.Template.Priority, StaticWeight: item.Template.StaticWeight,
		ProbeModel: item.Template.ProbeModel, Tags: append([]string(nil), item.Template.Tags...),
		Extra: append([]byte(nil), item.Template.Extra...), ModelAllowList: append([]string(nil), item.Template.ModelAllowList...),
		CapabilityFlags: append([]string(nil), item.Template.CapabilityFlags...),
	}, nil
}

func baseItemPlan(index int, item Item) ItemPlan {
	return ItemPlan{Index: index, SourceName: item.Template.Name, SourceProvider: item.Template.SourceProvider,
		Vendor: item.Candidate.Vendor, AuthMode: item.Candidate.AuthMode}
}

func itemCommitment(sessionCommitment string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", sessionCommitment, index)))
	return hex.EncodeToString(sum[:])
}

func shortCommitment(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}
