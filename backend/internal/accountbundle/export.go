package accountbundle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
)

func (s *Service) PlanExport(ctx context.Context, in ExportPlanInput) (ExportPlan, error) {
	if err := s.ready(); err != nil {
		return ExportPlan{}, err
	}
	if err := validateOperator(in.TenantID, in.ActorID, in.ActorRole); err != nil {
		return ExportPlan{}, err
	}
	snapshots, err := s.readSnapshots(ctx, in.TenantID, in.AccountIDs)
	if err != nil {
		return ExportPlan{}, err
	}
	return buildExportPlan(snapshots)
}

func buildExportPlan(snapshots []accountSnapshot) (ExportPlan, error) {
	out := ExportPlan{
		ContractVersion:      contractVersion,
		Items:                make([]ExportPlanItem, 0, len(snapshots)),
		RequiredConfirmation: exportConfirmation,
	}
	for _, snapshot := range snapshots {
		item := ExportPlanItem{AccountID: snapshot.ID, Name: snapshot.Config.Name}
		if snapshot.CredentialConflict != "" {
			item.Status = "conflict"
			item.Code = snapshot.CredentialConflict
			item.Message = exportConflictMessage(snapshot.CredentialConflict)
			out.Conflict++
		} else {
			item.Status = "ready"
			item.Code = "ready"
			item.Message = "账号公开配置、凭据和代理映射可进入加密迁移包"
			item.CredentialMode = snapshot.Credential.Vendor + "/" + snapshot.Credential.AuthMode
			item.IncludesProxy = snapshot.Proxy != nil
			out.Ready++
		}
		out.Items = append(out.Items, item)
	}
	hash, err := stableHash(struct {
		ContractVersion string            `json:"contract_version"`
		Snapshots       []accountSnapshot `json:"snapshots"`
	}{ContractVersion: contractVersion, Snapshots: snapshots})
	if err != nil {
		return ExportPlan{}, err
	}
	out.PlanHash = hash
	return out, nil
}

func exportConflictMessage(code string) string {
	switch code {
	case "account_not_found":
		return "账号不存在或不属于当前租户"
	case "multiple_portable_credentials":
		return "账号存在多条可迁移凭据，当前迁移包禁止随意选择其中一条"
	case "portable_credential_missing":
		return "账号没有可迁移的有效凭据"
	case "proxy_not_found":
		return "账号引用的代理不存在，必须先修复映射"
	default:
		return "账号当前状态不能安全导出"
	}
}

func (s *Service) ExecuteExport(ctx context.Context, in ExportExecuteInput) (ExportResult, error) {
	defer func() { in.Password = "" }()
	if err := s.ready(); err != nil {
		return ExportResult{}, err
	}
	if err := validateOperator(in.TenantID, in.ActorID, in.ActorRole); err != nil {
		return ExportResult{}, err
	}
	if strings.TrimSpace(in.Confirmation) != exportConfirmation {
		return ExportResult{}, ErrConfirmationRequired
	}
	if err := validatePassword(in.Password); err != nil {
		return ExportResult{}, err
	}
	snapshots, err := s.readSnapshots(ctx, in.TenantID, in.AccountIDs)
	if err != nil {
		return ExportResult{}, err
	}
	plan, err := buildExportPlan(snapshots)
	if err != nil {
		return ExportResult{}, err
	}
	if !compareHash(strings.TrimSpace(in.PlanHash), plan.PlanHash) {
		return ExportResult{}, ErrPlanChanged
	}
	if plan.Conflict > 0 || plan.Ready != len(plan.Items) {
		return ExportResult{}, ErrConflict
	}
	content := payloadContent{CreatedAt: s.nowTime(), Accounts: make([]PortableAccount, 0, len(snapshots))}
	defer zeroPortableContent(&content)
	proxyByRef := make(map[string]struct{})
	for _, snapshot := range snapshots {
		account, proxy, err := s.exportAccount(ctx, snapshot)
		if err != nil {
			return ExportResult{}, err
		}
		content.Accounts = append(content.Accounts, account)
		if proxy != nil {
			if _, exists := proxyByRef[proxy.Ref]; !exists {
				proxyByRef[proxy.Ref] = struct{}{}
				content.Proxies = append(content.Proxies, *proxy)
			}
		}
	}
	envelope, err := seal(content, in.Password)
	if err != nil {
		return ExportResult{}, err
	}
	if err := s.recordBundleEvent(ctx, in.TenantID, in.ActorID, in.ActorRole, "account_bundle_exported", in.RequestID, in.Reason, map[string]any{
		"account_count": len(content.Accounts), "proxy_count": len(content.Proxies),
		"plan_hash": plan.PlanHash, "encrypted": true,
	}); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Envelope: envelope, AccountCount: len(content.Accounts), ProxyCount: len(content.Proxies), ExportedAt: content.CreatedAt}, nil
}

func (s *Service) exportAccount(ctx context.Context, snapshot accountSnapshot) (PortableAccount, *PortableProxy, error) {
	credential, err := s.credentials.LoadExactForPortableExport(ctx, snapshot.TenantID, snapshot.ID, snapshot.Credential.ID, snapshot.Credential.Version)
	if err != nil {
		if errors.Is(err, credentialstore.ErrCredentialVersionConflict) {
			return PortableAccount{}, nil, ErrPlanChanged
		}
		return PortableAccount{}, nil, err
	}
	defer privacy.Zeroize(credential.PlaintextPayload)
	account := PortableAccount{
		Ref: accountRef(snapshot.TenantID, snapshot.ID), SourceProviderID: snapshot.ProviderID,
		SourceChannelID: snapshot.ChannelID, Config: snapshot.Config,
		Credential: PortableCredential{
			Vendor: credential.Vendor, AuthMode: credential.AuthMode,
			Payload:                append(json.RawMessage(nil), credential.PlaintextPayload...),
			ExternalAccountID:      credential.ExternalAccountIDValue,
			ExternalSubjectID:      credential.ExternalSubjectIDValue,
			ExternalAccountEmail:   credential.ExternalAccountEmailValue,
			ExternalIdentitySource: credential.ExternalIdentitySource,
		},
	}
	if snapshot.Proxy == nil {
		return account, nil, nil
	}
	secret := ""
	if snapshot.Proxy.AuthSecret != nil {
		secret, err = proxysecret.Decode(ctx, s.keys, snapshot.TenantID, *snapshot.Proxy.AuthSecret)
		if err != nil {
			privacy.Zeroize(account.Credential.Payload)
			return PortableAccount{}, nil, err
		}
	}
	proxy := &PortableProxy{
		Ref: proxyRef(*snapshot.Proxy), Name: snapshot.Proxy.Name,
		Protocol: snapshot.Proxy.Protocol, Host: snapshot.Proxy.Host, Port: snapshot.Proxy.Port,
		AuthUsername: valueOrEmpty(snapshot.Proxy.AuthUsername), AuthSecret: secret,
	}
	account.ProxyRef = proxy.Ref
	return account, proxy, nil
}

func (s *Service) recordBundleEvent(ctx context.Context, tenantID int64, actorID, actorRole, action, requestID, reason string, fields map[string]any) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(raw)
	_, err = admindb.New(s.pool).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: strings.TrimSpace(actorID), ActorRole: actorRole,
		Action: action, TargetType: "account_bundle",
		RequestID: optionalString(requestID), Reason: optionalString(reason), Payload: raw,
	})
	return err
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
