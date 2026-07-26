package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/notify"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type adminSessionTokenReject struct{}

func (adminSessionTokenReject) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return admin.AdminIdentity{}, admin.ErrAdminUnauthorized
}

type fixedAdminSession struct {
	tenantID int64
	userID   int64
}

func (s fixedAdminSession) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return usersession.ValidatedSession{TenantID: s.tenantID, UserID: s.userID}, nil
}

type fixedAdminRole struct{}

func (fixedAdminRole) ActiveUserRole(context.Context, int64, int64) (string, error) {
	return "admin", nil
}

type adminSessionNotifyStore struct{}

func (adminSessionNotifyStore) GetSettings(_ context.Context, tenantID, userID int64) (notify.Settings, error) {
	return notify.DefaultSettings(tenantID, userID), nil
}

func (adminSessionNotifyStore) UpsertSettings(_ context.Context, settings notify.Settings) (notify.Settings, error) {
	return settings, nil
}

func (adminSessionNotifyStore) UpsertSettingsWithAdminLog(
	_ context.Context,
	settings notify.Settings,
	_ notify.AdminMutation,
) (notify.Settings, error) {
	return settings, nil
}

func newAdminSessionWriteRouter(t *testing.T, sessionTenantID int64) http.Handler {
	t.Helper()
	const platformTenantID int64 = 1
	d := &deps{
		cfg: &config.Config{
			BillingPolicyVersion: "test-1.0",
			RequestClass:         "standard",
		},
		platformTenantID: platformTenantID,
		adminAuth: adminsessionauth.New(
			adminSessionTokenReject{},
			fixedAdminSession{tenantID: sessionTenantID, userID: 42},
			fixedAdminRole{},
			nil,
			platformTenantID,
		),
		notificationSettings: notify.NewService(adminSessionNotifyStore{}),
	}
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())
	return r
}

func Test显式安全管理路由允许平台管理员会话写(t *testing.T) {
	h := newAdminSessionWriteRouter(t, 1)
	req := httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/users/42/notifications?tenant_id=1",
		strings.NewReader(`{"notify_type":"none"}`),
	)
	req.Header.Set("Authorization", "Bearer session-admin")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("平台管理员会话应能写平台租户的显式 SessionSafe 接口，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

func Test显式安全管理路由会话仍受租户作用域约束(t *testing.T) {
	h := newAdminSessionWriteRouter(t, 7)

	ownReq := httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/users/42/notifications?tenant_id=7",
		strings.NewReader(`{"notify_type":"none"}`),
	)
	ownReq.Header.Set("Authorization", "Bearer session-admin")
	ownReq.Header.Set("Content-Type", "application/json")
	ownRec := httptest.NewRecorder()
	h.ServeHTTP(ownRec, ownReq)
	if ownRec.Code != http.StatusOK {
		t.Fatalf("租户管理员写自身租户的通知设置应成功，得到 %d：%s", ownRec.Code, ownRec.Body.String())
	}

	otherReq := httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/users/42/notifications?tenant_id=8",
		strings.NewReader(`{"notify_type":"none"}`),
	)
	otherReq.Header.Set("Authorization", "Bearer session-admin")
	otherReq.Header.Set("Content-Type", "application/json")
	otherRec := httptest.NewRecorder()
	h.ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusForbidden {
		t.Fatalf("租户管理员跨租户写必须返回 403，得到 %d：%s", otherRec.Code, otherRec.Body.String())
	}
}

func Test具备原子日志的目录管理写允许会话(t *testing.T) {
	h := newAdminSessionWriteRouter(t, 1)
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/providers?tenant_id=1",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Authorization", "Bearer session-admin")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), `"code":"provider_code_required"`) {
		t.Fatalf("会话必须穿过写分级并精确命中目录业务校验，得到 %d：%s", rec.Code, rec.Body.String())
	}
}
