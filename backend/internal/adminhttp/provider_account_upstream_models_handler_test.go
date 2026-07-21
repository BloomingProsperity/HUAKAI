package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmodeldiscovery"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type stubUpstreamModelsAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s stubUpstreamModelsAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type stubUpstreamModelsAccountStore struct{ err error }

func (s stubUpstreamModelsAccountStore) GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return admindb.AdminProviderAccountRow{ID: 7, TenantID: 42}, s.err
}

type stubUpstreamModelsDiscovery struct {
	result     accountmodeldiscovery.Result
	syncResult accountmodeldiscovery.SyncResult
	err        error
	syncInput  accountmodeldiscovery.SyncInput
}

func (s *stubUpstreamModelsDiscovery) Discover(context.Context, int64, int64) (accountmodeldiscovery.Result, error) {
	return s.result, s.err
}

func (s *stubUpstreamModelsDiscovery) Sync(_ context.Context, in accountmodeldiscovery.SyncInput) (accountmodeldiscovery.SyncResult, error) {
	s.syncInput = in
	return s.syncResult, s.err
}

func modelsRouter(d UpstreamModelsDeps) *chi.Mux {
	router := chi.NewRouter()
	MountProviderAccountUpstreamModelsRoutes(router, d)
	return router
}

func scopedPlatformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{Role: admin.RolePlatformAdmin, ScopeTenantID: 42, TokenID: 8}
}

func TestUpstreamModelsSyncAllowsSessionAdminWrite(t *testing.T) {
	router := modelsRouter(UpstreamModelsDeps{
		Auth: adminsessionauthtest.Resolver(), Accounts: stubUpstreamModelsAccountStore{},
		Discovery: &stubUpstreamModelsDiscovery{},
	})
	if status := adminsessionauthtest.Status(
		router, http.MethodPost, "/7/upstream-models/sync", adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatalf("租户管理员 session 调用账号模型同步不应在 handler 前被 401 拒绝")
	}
}

func TestUpstreamModelsHandlerDiscoversAllAccountFamiliesThroughService(t *testing.T) {
	discovery := &stubUpstreamModelsDiscovery{result: accountmodeldiscovery.Result{
		AccountID: 7, AccountCredentialID: 9, CredentialVersion: 3, Vendor: "anthropic", AuthMode: "api_key",
		ProtocolFamily: "anthropic_messages", DiscoveredAt: time.Unix(123, 0).UTC(),
		Models: []accountmodeldiscovery.Model{{ID: "claude-a", DisplayName: "Claude A", Capabilities: []string{"messages"}}},
	}}
	router := modelsRouter(UpstreamModelsDeps{Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{}, Discovery: discovery})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response upstreamModelsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 1 || len(response.Models) != 1 || response.Models[0] != "claude-a" || response.Items[0].DisplayName != "Claude A" {
		t.Fatalf("响应未同时保留兼容 ID 与模型详情: %+v", response)
	}
}

func TestUpstreamModelsSyncUsesAuthenticatedActor(t *testing.T) {
	discovery := &stubUpstreamModelsDiscovery{syncResult: accountmodeldiscovery.SyncResult{
		Result:  accountmodeldiscovery.Result{AccountID: 7, Vendor: "openai", AuthMode: "api_key", Models: []accountmodeldiscovery.Model{{ID: "gpt-4o"}}, DiscoveredAt: time.Now().UTC()},
		Changed: true, PreviousCount: 2,
	}}
	router := modelsRouter(UpstreamModelsDeps{Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{}, Discovery: discovery})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/7/upstream-models/sync", strings.NewReader(`{"reason":"人工刷新"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if discovery.syncInput.TenantID != 42 || discovery.syncInput.AccountID != 7 || discovery.syncInput.ActorID != "admin_token:8" || discovery.syncInput.ActorRole != admin.RolePlatformAdmin || discovery.syncInput.Reason != "人工刷新" {
		t.Fatalf("同步没有使用认证身份和服务端租户: %+v", discovery.syncInput)
	}
}

func TestUpstreamModelsHandlerMapsCredentialRotationConflict(t *testing.T) {
	discovery := &stubUpstreamModelsDiscovery{err: &accountmodeldiscovery.DiscoveryError{Kind: accountmodeldiscovery.ErrorCredentialChanged}}
	router := modelsRouter(UpstreamModelsDeps{Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{}, Discovery: discovery})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/7/upstream-models/sync", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "credential_changed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamModelsFailureEmitsDiscriminatingLog(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		path       string
		err        error
		eventClass string
		errorClass string
		authMode   string
	}{
		{
			name: "发现被上游拒绝", method: http.MethodGet, path: "/7/upstream-models",
			err: &accountmodeldiscovery.DiscoveryError{
				Kind: accountmodeldiscovery.ErrorCredentialRejected, StatusCode: http.StatusUnauthorized,
				Vendor: "anthropic", AuthMode: "claude_code",
			},
			eventClass: "upstream_models_discover_failed", errorClass: "upstream_auth_rejected", authMode: "claude_code",
		},
		{
			name: "同步撞凭据轮换", method: http.MethodPost, path: "/7/upstream-models/sync",
			err: &accountmodeldiscovery.DiscoveryError{
				Kind: accountmodeldiscovery.ErrorCredentialChanged, Vendor: "openai", AuthMode: "codex_cli_oauth",
			},
			eventClass: "upstream_models_sync_failed", errorClass: "auth_rotation_conflict", authMode: "codex_cli_oauth",
		},
		{
			// refresh_token 词根撞 privacy 值位禁写,必须以等义分类落日志。
			name: "刷新凭据模式换等义分类", method: http.MethodGet, path: "/7/upstream-models",
			err: &accountmodeldiscovery.DiscoveryError{
				Kind: accountmodeldiscovery.ErrorRateLimited, StatusCode: http.StatusTooManyRequests,
				Vendor: "openai", AuthMode: "refresh_token",
			},
			eventClass: "upstream_models_discover_failed", errorClass: "rate_limited", authMode: "oauth_refresh",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs strings.Builder
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			router := modelsRouter(UpstreamModelsDeps{
				Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{},
				Discovery: &stubUpstreamModelsDiscovery{err: test.err},
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))

			var discoveryErr *accountmodeldiscovery.DiscoveryError
			errors.As(test.err, &discoveryErr)
			logged := logs.String()
			if strings.Contains(logged, "privacy_guard_hit") || strings.Contains(logged, `"redaction_result":"blocked"`) {
				t.Fatalf("失败日志被 privacy 禁写拦截,可辨识字段全丢: %s", logged)
			}
			for field, want := range map[string]string{
				"component":   "adminhttp.provider_account_upstream_models",
				"error_class": test.errorClass,
				"event_class": test.eventClass,
				"outcome":     "failed",
				"vendor":      discoveryErr.Vendor,
				"auth_mode":   test.authMode,
			} {
				if !strings.Contains(logged, `"`+field+`":"`+want+`"`) {
					t.Fatalf("失败日志缺可辨识字段 %s=%s: %s", field, want, logged)
				}
			}
			if !strings.Contains(logged, `"tenant_id":42`) || !strings.Contains(logged, `"provider_account_id":7`) {
				t.Fatalf("失败日志缺 tenant/account 关键 ID: %s", logged)
			}
			if !strings.Contains(logged, `"upstream_status":`+strconv.Itoa(discoveryErr.StatusCode)) {
				t.Fatalf("失败日志缺上游状态码: %s", logged)
			}
		})
	}
}

func TestUpstreamModelsSuccessDoesNotEmitFailureLog(t *testing.T) {
	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	discovery := &stubUpstreamModelsDiscovery{result: accountmodeldiscovery.Result{
		AccountID: 7, Vendor: "openai", AuthMode: "api_key",
		Models: []accountmodeldiscovery.Model{{ID: "gpt-4o"}}, DiscoveredAt: time.Now().UTC(),
	}}
	router := modelsRouter(UpstreamModelsDeps{Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{}, Discovery: discovery})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(logs.String(), `"result":"failed"`) {
		t.Fatalf("成功路径不应产生失败日志: %s", logs.String())
	}
}

func TestUpstreamModelsHandlerDoesNotHideAccountLookupFailure(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{pgx.ErrNoRows, http.StatusNotFound},
		{errors.New("数据库不可用"), http.StatusServiceUnavailable},
	} {
		router := modelsRouter(UpstreamModelsDeps{Auth: stubUpstreamModelsAuth{ident: scopedPlatformAdmin()}, Accounts: stubUpstreamModelsAccountStore{err: test.err}, Discovery: &stubUpstreamModelsDiscovery{}})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/7/upstream-models", nil))
		if recorder.Code != test.status {
			t.Fatalf("err=%v status=%d，期望 %d", test.err, recorder.Code, test.status)
		}
	}
}
