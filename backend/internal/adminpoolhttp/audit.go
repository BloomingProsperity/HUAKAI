package adminpoolhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

func writeProviderAccountReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, code, err.Error())
}

func chineseReason(got, fallback string) *string {
	return adminhttpcore.Reason(got, fallback)
}

func writeProviderAccountAudit(ctx context.Context, r *http.Request, store AdminPoolAccountStore, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	return adminhttpcore.WriteAudit(ctx, r, store, ident, &tenantID, action, "provider_account", &targetID, reason, payload)
}

// writeProviderAccountAuditTx 在调用方事务内写管理审计，使建号的账号、凭据、审计同成同败。
func writeProviderAccountAuditTx(ctx context.Context, tx pgx.Tx, r *http.Request, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	actorID := ident.AuditActor()
	reqID := middleware.GetReqID(r.Context())
	_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}

// providerAccountCreateAuditPayload 生成建号审计负载：credential_id/version 取自同事务凭据、
// 不含明文凭据；高风险确认路径附带风险维度。渠道健康是提交后投影、不在审计事务内，故不记其状态。
func providerAccountCreateAuditPayload(tenantID int64, req createProviderAccountRequest, cred credentialstore.CredentialMetadata, riskReport mixedchannelrisk.Report) []byte {
	fields := map[string]any{
		"tenant_id":           tenantID,
		"provider_id":         req.ProviderID,
		"channel_id":          req.ChannelID,
		"name":                req.Name,
		"account_type":        req.AccountType,
		"vendor":              req.Vendor,
		"auth_mode":           req.AuthMode,
		"credential_id":       cred.ID,
		"credential_version":  int(cred.Version),
		"credentials_present": true,
	}
	if riskReport.HighRisk {
		fields["mixed_channel_risk_confirmed"] = true
		fields["mixed_channel_risks"] = riskReport.Items
	}
	payload, _ := json.Marshal(fields)
	return payload
}
