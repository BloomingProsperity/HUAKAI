package accountbundle

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

type StructurePlanInput struct {
	TenantID int64
	Manifest Manifest
	Mappings []accountsource.Mapping
}

type StructurePlanItem struct {
	Index                 int                      `json:"index"`
	SourceName            string                   `json:"source_name"`
	SourceProvider        string                   `json:"source_provider"`
	TargetName            string                   `json:"target_name,omitempty"`
	Action                string                   `json:"action"`
	Code                  string                   `json:"code"`
	Message               string                   `json:"message"`
	PlanHash              string                   `json:"plan_hash,omitempty"`
	RequiredConfirmations []string                 `json:"required_confirmations,omitempty"`
	MixedChannelRisk      *mixedchannelrisk.Report `json:"mixed_channel_risk,omitempty"`
}

type StructurePlan struct {
	BundleID string              `json:"bundle_id"`
	Items    []StructurePlanItem `json:"items"`
}

type StructureSelection struct {
	Index         int      `json:"index"`
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
}

type StructureExecuteInput struct {
	StructurePlanInput
	Selections []StructureSelection
	ActorID    string
	ActorRole  string
	RequestID  string
	Reason     string
}

type StructureExecutionItem struct {
	Index             int    `json:"index"`
	Status            string `json:"status"`
	Code              string `json:"code"`
	Message           string `json:"message"`
	ProviderAccountID int64  `json:"provider_account_id,omitempty"`
}

type StructureExecution struct {
	BundleID string                   `json:"bundle_id"`
	Items    []StructureExecutionItem `json:"items"`
}

type StructureImporter struct {
	pool *pgxpool.Pool
}

func NewStructureImporter(pool *pgxpool.Pool) *StructureImporter {
	return &StructureImporter{pool: pool}
}

func (s *StructureImporter) Plan(ctx context.Context, in StructurePlanInput) (StructurePlan, error) {
	if s == nil || s.pool == nil || in.TenantID <= 0 || in.Manifest.Mode != ModeStructure {
		return StructurePlan{}, ErrInvalidBundle
	}
	out := StructurePlan{BundleID: in.Manifest.BundleID, Items: make([]StructurePlanItem, 0, len(in.Manifest.Accounts))}
	resolved := make([]accountintake.AccountDefaults, len(in.Manifest.Accounts))
	resolveErrors := make([]error, len(in.Manifest.Accounts))
	targetCounts := make(map[string]int, len(in.Manifest.Accounts))
	for index, account := range in.Manifest.Accounts {
		resolved[index], resolveErrors[index] = accountsource.ResolveDefaults(accountsource.Item{Template: account.Template}, in.Mappings)
		if resolveErrors[index] == nil {
			targetCounts[resolved[index].Name]++
		}
	}
	for index, account := range in.Manifest.Accounts {
		item := StructurePlanItem{Index: index, SourceName: account.Template.Name, SourceProvider: account.Template.SourceProvider}
		defaults, err := resolved[index], resolveErrors[index]
		if err != nil {
			item.Action, item.Code, item.Message = "fail", "mapping_required", err.Error()
			out.Items = append(out.Items, item)
			continue
		}
		item.TargetName = defaults.Name
		if targetCounts[defaults.Name] > 1 {
			item.Action, item.Code, item.Message = "conflict", "duplicate_target_name", "结构包中多个账号映射到同一目标名称，必须调整映射后重试"
			out.Items = append(out.Items, item)
			continue
		}
		var existingID int64
		err = s.pool.QueryRow(ctx, `SELECT id FROM provider_accounts WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL`, in.TenantID, defaults.Name).Scan(&existingID)
		switch {
		case err == nil:
			item.Action, item.Code, item.Message = "conflict", "account_name_exists", "目标租户已存在同名账号，结构包禁止自动覆盖"
		case !errors.Is(err, pgx.ErrNoRows):
			return StructurePlan{}, err
		default:
			if _, err := admindb.New(s.pool).GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{TenantID: in.TenantID, ProviderID: defaults.ProviderID}); err != nil {
				item.Action, item.Code, item.Message = "fail", "target_provider_invalid", "目标 provider 不存在或不属于当前租户"
			} else {
				peers, peerErr := admindb.New(s.pool).ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{TenantID: in.TenantID, ChannelID: defaults.ChannelID})
				if peerErr != nil {
					return StructurePlan{}, peerErr
				}
				report := mixedchannelrisk.Evaluate(
					mixedchannelrisk.Account{ProviderID: defaults.ProviderID, ChannelID: defaults.ChannelID, AccountType: defaults.AccountType},
					structureRiskPeers(peers),
				)
				if report.HighRisk {
					item.MixedChannelRisk = &report
					item.RequiredConfirmations = []string{"confirm_mixed_channel_risk"}
				}
				item.Action, item.Code, item.Message = "create", "create_disabled_skeleton", "将创建默认禁用且不含凭据的账号骨架"
				item.PlanHash = structurePlanHash(in, index, defaults, report)
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *StructureImporter) Execute(ctx context.Context, in StructureExecuteInput) (StructureExecution, error) {
	if len(in.Selections) == 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" {
		return StructureExecution{}, ErrInvalidBundle
	}
	plan, err := s.Plan(ctx, in.StructurePlanInput)
	if err != nil {
		return StructureExecution{}, err
	}
	byIndex := make(map[int]StructurePlanItem, len(plan.Items))
	for _, item := range plan.Items {
		byIndex[item.Index] = item
	}
	out := StructureExecution{BundleID: in.Manifest.BundleID, Items: make([]StructureExecutionItem, 0, len(in.Selections))}
	seen := make(map[int]struct{}, len(in.Selections))
	for _, selection := range in.Selections {
		result := StructureExecutionItem{Index: selection.Index}
		planned, ok := byIndex[selection.Index]
		if !ok || planned.Action != "create" {
			result.Status, result.Code, result.Message = "failed", "item_not_creatable", "该项不是可创建计划"
			out.Items = append(out.Items, result)
			continue
		}
		if _, duplicate := seen[selection.Index]; duplicate {
			result.Status, result.Code, result.Message = "failed", "duplicate_selection", "同一结构项不能重复执行"
			out.Items = append(out.Items, result)
			continue
		}
		seen[selection.Index] = struct{}{}
		if subtle.ConstantTimeCompare([]byte(selection.PlanHash), []byte(planned.PlanHash)) != 1 {
			result.Status, result.Code, result.Message = "conflict", "plan_changed", "结构项计划已变化，请重新预检"
			out.Items = append(out.Items, result)
			continue
		}
		confirmed, confirmationErr := structureConfirmation(selection.Confirmations)
		if confirmationErr != nil {
			result.Status, result.Code, result.Message = "failed", "confirmation_invalid", "结构项包含未知或重复确认"
			out.Items = append(out.Items, result)
			continue
		}
		if len(planned.RequiredConfirmations) > 0 && !confirmed {
			result.Status, result.Code, result.Message = "conflict", "confirmation_required", "缺少混合渠道风险确认"
			out.Items = append(out.Items, result)
			continue
		}
		account := in.Manifest.Accounts[selection.Index]
		defaults, mapErr := accountsource.ResolveDefaults(accountsource.Item{Template: account.Template}, in.Mappings)
		if mapErr != nil {
			result.Status, result.Code, result.Message = "failed", "mapping_required", mapErr.Error()
			out.Items = append(out.Items, result)
			continue
		}
		accountID, createErr := s.createOne(ctx, in, defaults, confirmed)
		if createErr != nil {
			result.Status, result.Code, result.Message = "failed", "create_failed", "账号骨架创建失败"
		} else {
			result.Status, result.Code, result.Message = "created", "disabled_skeleton_created", "默认禁用的账号骨架已创建，补齐凭据后才能启用"
			result.ProviderAccountID = accountID
		}
		out.Items = append(out.Items, result)
	}
	return out, nil
}

func (s *StructureImporter) createOne(ctx context.Context, in StructureExecuteInput, defaults accountintake.AccountDefaults, confirmed bool) (int64, error) {
	var accountID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		family, err := q.GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{TenantID: in.TenantID, ProviderID: defaults.ProviderID})
		if err != nil {
			return err
		}
		disabled := false
		created, err := accountcreate.InsertTx(ctx, tx, accountcreate.Params{
			Insert: admindb.InsertProviderAccountParams{
				TenantID: in.TenantID, ProviderID: defaults.ProviderID, ChannelID: defaults.ChannelID,
				Name: defaults.Name, AccountType: defaults.AccountType, Enabled: &disabled,
				Credentials: []byte(`{}`), CapConcurrency: defaults.CapConcurrency, Priority: defaults.Priority,
				StaticWeight: defaults.StaticWeight, ProbeModel: defaults.ProbeModel, Tags: defaults.Tags,
				Extra: []byte(`{}`), ModelAllowList: defaults.ModelAllowList,
				CapabilityFlags: defaults.CapabilityFlags, ActorID: stringPtr(in.ActorID),
			},
			Candidate:      mixedchannelrisk.Account{ProviderID: defaults.ProviderID, ChannelID: defaults.ChannelID, AccountType: defaults.AccountType},
			ProviderFamily: family,
			Confirmed:      confirmed,
		})
		if err != nil {
			return err
		}
		accountID = created.ID
		payload, _ := json.Marshal(map[string]any{"bundle_id": in.Manifest.BundleID, "structure_only": true, "enabled": false})
		_, err = q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
			Action: "create_provider_account", TargetType: "provider_account", TargetID: &accountID,
			RequestID: stringPtr(in.RequestID), Reason: stringPtr(in.Reason), Payload: payload,
		})
		return err
	})
	return accountID, err
}

func structurePlanHash(in StructurePlanInput, index int, defaults interface{}, risk mixedchannelrisk.Report) string {
	raw, _ := json.Marshal(struct {
		TenantID int64                   `json:"tenant_id"`
		BundleID string                  `json:"bundle_id"`
		Index    int                     `json:"index"`
		Defaults interface{}             `json:"defaults"`
		Risk     mixedchannelrisk.Report `json:"risk"`
	}{in.TenantID, in.Manifest.BundleID, index, defaults, risk})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func structureRiskPeers(rows []admindb.ProviderAccountRiskPeerRow) []mixedchannelrisk.Account {
	out := make([]mixedchannelrisk.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedchannelrisk.Account{
			ID: row.ID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
			AccountType: row.AccountType, Vendor: row.CredentialVendor, AuthMode: row.CredentialAuthMode,
		})
	}
	return out
}

func structureConfirmation(values []string) (bool, error) {
	confirmed := false
	for _, value := range values {
		if strings.TrimSpace(value) != "confirm_mixed_channel_risk" || confirmed {
			return false, ErrInvalidBundle
		}
		confirmed = true
	}
	return confirmed, nil
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
