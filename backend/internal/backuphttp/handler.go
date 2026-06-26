package backuphttp

import (
	"encoding/json"
	"net/http"
)

// redactedColumns 是"未来导出 bundle 默认脱敏"的敏感列声明(策略边界,非实际数据)。
// 基于真实存在的敏感列(sql/migrations 核过):凭据/密码/令牌/2FA 秘密一律默认脱敏;
// 原文导出(若需)是独立的 Owner-gated 高危开关,绝不默认开。
var redactedColumns = []string{
	"api_keys.key_hash",
	"users.password_hash",
	"provider_accounts.auth_secret / credentials",
	"tenant_proxies.auth_secret",
	"sessions.token_hash",
	"users.totp_secret / recovery_codes",
	"webauthn_credentials (passkey)",
	"platform_settings.payment_provider_config",
}

const redactionNote = "本端点仅返回元数据,不含任何业务数据。未来的导出 bundle 默认脱敏上列敏感列;" +
	"导出上游账号凭据原文 = 等于带走整个账号池,属独立 Owner-gated 高危开关,绝不默认开。恢复(写入)为最高危,后续 Owner-gated 切片。"

type manifestResponse struct {
	Object          string          `json:"object"`
	SchemaVersion   int64           `json:"schema_version"`
	SchemaDirty     bool            `json:"schema_dirty"`
	EstimateBasis   string          `json:"estimate_basis"`
	TableCount      int             `json:"table_count"`
	Tables          []TableInfo     `json:"tables"`
	RedactionPolicy redactionPolicy `json:"redaction_policy"`
}

type redactionPolicy struct {
	Note            string   `json:"note"`
	RedactedColumns []string `json:"redacted_columns"`
}

// NewManifestHandler 返回只读 manifest handler。鉴权(platform_admin)由 cmd/gateway 的 adminGate 包裹;
// 本 handler 只聚合元数据并格式化。store 为 nil 或查询失败 → 503(fail-closed,不出半成品)。
func NewManifestHandler(store Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errBody("backup_not_configured", "backup manifest store unset"))
			return
		}
		data, err := store.Manifest(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, errBody("backup_manifest_failed", "backup manifest backend unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, manifestResponse{
			Object:        "backup_manifest",
			SchemaVersion: data.SchemaVersion,
			SchemaDirty:   data.SchemaDirty,
			EstimateBasis: "pg_class.reltuples(上次 ANALYZE/VACUUM 的近似值,非精确 COUNT)",
			TableCount:    len(data.Tables),
			Tables:        data.Tables,
			RedactionPolicy: redactionPolicy{
				Note:            redactionNote,
				RedactedColumns: redactedColumns,
			},
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func errBody(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}
