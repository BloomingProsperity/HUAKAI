package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
)

type AdminEmailSettingsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminEmailSettingsDeps struct {
	Auth         AdminEmailSettingsAuth
	Store        mailinfra.SettingsStore
	Keys         mailinfra.SecretKeyProvider
	TestDispatch mailinfra.SMTPDispatch
}

type adminEmailSettingsRequest struct {
	TenantID          int64   `json:"tenant_id"`
	SMTPHost          string  `json:"smtp_host,omitempty"`
	SMTPPort          int     `json:"smtp_port,omitempty"`
	SMTPUsername      string  `json:"smtp_username,omitempty"`
	SMTPPassword      *string `json:"smtp_password,omitempty"`
	SMTPFrom          string  `json:"smtp_from,omitempty"`
	SMTPFromName      string  `json:"smtp_from_name,omitempty"`
	SMTPUseTLS        *bool   `json:"smtp_use_tls,omitempty"`
	EmailVerifyEnable *bool   `json:"email_verify_enabled,omitempty"`
	// 按 kind 的鉴权邮件模板覆盖:字段 nil=不修改,空串=清除覆盖(渲染时回退内置默认)。
	Templates map[string]adminEmailTemplateInput `json:"templates,omitempty"`
}

type adminEmailTemplateInput struct {
	Subject *string `json:"subject,omitempty"`
	Body    *string `json:"body,omitempty"`
}

type adminEmailTemplatePreviewRequest struct {
	TenantID int64  `json:"tenant_id"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject,omitempty"`
	Body     string `json:"body,omitempty"`
}

type adminEmailTestRequest struct {
	TenantID int64  `json:"tenant_id"`
	To       string `json:"to"`
}

func MountAdminEmailSettingsRoutes(r chi.Router, d AdminEmailSettingsDeps) {
	r.Get("/settings", newAdminEmailSettingsGetHandler(d))
	r.Put("/settings", newAdminEmailSettingsPutHandler(d))
	r.Post("/test", newAdminEmailTestHandler(d))
	r.Post("/templates/preview", newAdminEmailTemplatePreviewHandler(d))
}

func newAdminEmailSettingsGetHandler(d AdminEmailSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminEmailSettings(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminEmailTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		rows, err := d.Store.List(r.Context(), tenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "email_settings_read_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"tenant_id": tenantID,
			"settings":  maskEmailSettings(rows),
		})
	}
}

func newAdminEmailSettingsPutHandler(d AdminEmailSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminEmailSettings(w, r, d)
		if !ok {
			return
		}
		var req adminEmailSettingsRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !adminCanAccessTenant(ident, req.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		values, err := adminEmailSettingsValues(r.Context(), d.Keys, req)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "email_settings_invalid", err.Error())
			return
		}
		if len(values) == 0 {
			writeJSONError(w, http.StatusBadRequest, "email_settings_empty", "at least one setting is required")
			return
		}
		if err := d.Store.Save(r.Context(), req.TenantID, values, ident.AuditActor()); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "email_settings_update_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"tenant_id": req.TenantID, "updated": len(values)})
	}
}

func newAdminEmailTestHandler(d AdminEmailSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminEmailSettings(w, r, d)
		if !ok {
			return
		}
		var req adminEmailTestRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.To) == "" {
			writeJSONError(w, http.StatusBadRequest, "email_test_recipient_required", "to is required")
			return
		}
		if !adminCanAccessTenant(ident, req.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		settings, err := mailinfra.LoadSMTPSettings(r.Context(), d.Store, d.Keys, req.TenantID)
		if err != nil {
			writeAuthEmailError(w, err, "email settings are not usable")
			return
		}
		dispatch := d.TestDispatch
		if dispatch == nil {
			dispatch = func(ctx context.Context, settings mailinfra.SMTPSettings, msg mailinfra.Message) error {
				return mailinfra.NewSMTPSender(settings).Send(ctx, msg)
			}
		}
		if err := dispatch(r.Context(), settings, mailinfra.Message{
			TenantID: req.TenantID,
			To:       req.To,
			Subject:  "HUAKAI SMTP test",
			HTMLBody: "<html><body><p>HUAKAI SMTP settings test succeeded.</p></body></html>",
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "email_test_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"tenant_id": req.TenantID, "sent": true})
	}
}

func resolveAdminEmailSettings(w http.ResponseWriter, r *http.Request, d AdminEmailSettingsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil || d.Keys == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin email settings dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func resolveAdminEmailTenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return ident.ScopeTenantID, true
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, false
	}
	if !adminCanAccessTenant(ident, tenantID) {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, false
	}
	return tenantID, true
}

func adminEmailSettingsValues(ctx context.Context, keys mailinfra.SecretKeyProvider, req adminEmailSettingsRequest) (map[string]string, error) {
	if req.TenantID <= 0 {
		return nil, fmt.Errorf("tenant_id must be positive")
	}
	values := make(map[string]string)
	if v := strings.TrimSpace(req.SMTPHost); v != "" {
		values[mailinfra.SettingMailHost] = v
	}
	if req.SMTPPort != 0 {
		if req.SMTPPort < 1 || req.SMTPPort > 65535 {
			return nil, fmt.Errorf("smtp_port must be between 1 and 65535")
		}
		values[mailinfra.SettingMailPort] = strconv.Itoa(req.SMTPPort)
	}
	if v := strings.TrimSpace(req.SMTPUsername); v != "" {
		values[mailinfra.SettingMailUsername] = v
	}
	if req.SMTPPassword != nil {
		encrypted, err := mailinfra.EncodeSecret(ctx, keys, req.TenantID, *req.SMTPPassword)
		if err != nil {
			return nil, err
		}
		values[mailinfra.SettingMailPassword] = encrypted
	}
	if v := strings.TrimSpace(req.SMTPFrom); v != "" {
		values[mailinfra.SettingMailFrom] = v
	}
	if v := strings.TrimSpace(req.SMTPFromName); v != "" {
		values[mailinfra.SettingMailFromName] = v
	}
	if req.SMTPUseTLS != nil {
		values[mailinfra.SettingMailTLS] = strconv.FormatBool(*req.SMTPUseTLS)
	}
	if req.EmailVerifyEnable != nil {
		values[mailinfra.SettingVerifyRequirement] = strconv.FormatBool(*req.EmailVerifyEnable)
	}
	for kind, tpl := range req.Templates {
		subjectKey, bodyKey := mailinfra.TemplateSettingKeys(kind)
		if subjectKey == "" {
			return nil, fmt.Errorf("unknown template kind %q", kind)
		}
		subject, body := "", ""
		if tpl.Subject != nil {
			subject = strings.TrimSpace(*tpl.Subject)
		}
		if tpl.Body != nil {
			body = strings.TrimSpace(*tpl.Body)
		}
		if err := mailinfra.ValidateTemplateOverride(kind, subject, body); err != nil {
			return nil, err
		}
		if tpl.Subject != nil {
			values[subjectKey] = subject
		}
		if tpl.Body != nil {
			values[bodyKey] = body
		}
	}
	return values, nil
}

// newAdminEmailTemplatePreviewHandler 用样例值纯渲染模板供前端预览,不发信、不落库。
// 主题/正文传空则预览内置默认形态(占位符原样展示)。
func newAdminEmailTemplatePreviewHandler(d AdminEmailSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminEmailSettings(w, r, d)
		if !ok {
			return
		}
		var req adminEmailTemplatePreviewRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !adminCanAccessTenant(ident, req.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		if err := mailinfra.ValidateTemplateOverride(req.Kind, req.Subject, req.Body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "email_template_invalid", err.Error())
			return
		}
		vars := sampleTemplateVars(req.Kind)
		subject, body := strings.TrimSpace(req.Subject), strings.TrimSpace(req.Body)
		if subject != "" {
			rendered, err := mailinfra.RenderTemplate(subject, vars)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "email_template_invalid", err.Error())
				return
			}
			subject = rendered
		}
		if body != "" {
			rendered, err := mailinfra.RenderTemplate(body, vars)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "email_template_invalid", err.Error())
				return
			}
			body = rendered
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"kind":    req.Kind,
			"subject": subject,
			"html":    body,
		})
	}
}

// sampleTemplateVars 给预览用的占位样例值(与真实发送时的变量集合一一对应)。
func sampleTemplateVars(kind string) map[string]string {
	switch kind {
	case mailinfra.TemplateKindOAuthCode:
		return map[string]string{"code": "836195"}
	case mailinfra.TemplateKindPasswordReset:
		return map[string]string{"link": "https://example.com/reset-password?tenant_id=1&token=SAMPLETOKEN", "token": "SAMPLETOKEN", "email": "user@example.com"}
	default:
		return map[string]string{"link": "https://example.com/email-verify?tenant_id=1&token=SAMPLETOKEN", "token": "SAMPLETOKEN"}
	}
}

func maskEmailSettings(rows []mailinfra.StoredSetting) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"key":        row.Key,
			"updated_at": row.UpdatedAt,
			"updated_by": row.UpdatedBy,
		}
		if row.Key == mailinfra.SettingMailPassword {
			item["value"] = ""
			item["configured"] = strings.TrimSpace(row.Value) != ""
		} else {
			item["value"] = row.Value
		}
		out = append(out, item)
	}
	return out
}
