package accountintakehttp

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type oauthAccountIntakeServiceStub struct {
	startInput      accountintake.OAuthStartInput
	callbackActorID string
	callbackRole    string
	callbackTenant  int64
	startCalls      int
	callbackCalls   int
}

func (s *oauthAccountIntakeServiceStub) Start(_ context.Context, in accountintake.OAuthStartInput) (credentialacq.OAuthStartResult, error) {
	s.startCalls++
	s.startInput = in
	return credentialacq.OAuthStartResult{Session: credentialacq.Session{ID: "flow-1", TenantID: in.TenantID}}, nil
}

func (s *oauthAccountIntakeServiceStub) Callback(context.Context, string, string, string) (accountintake.OAuthPlanResult, error) {
	return accountintake.OAuthPlanResult{}, nil
}

func (s *oauthAccountIntakeServiceStub) CallbackForActor(_ context.Context, _, _, _ string, tenantID int64, actorID, actorRole string) (accountintake.OAuthPlanResult, error) {
	s.callbackCalls++
	s.callbackTenant = tenantID
	s.callbackActorID = actorID
	s.callbackRole = actorRole
	return accountintake.OAuthPlanResult{Flow: credentialacq.Session{TenantID: tenantID, ActorID: actorID, ActorRole: actorRole}}, nil
}

func (s *oauthAccountIntakeServiceStub) Poll(context.Context, int64, string, string, string) (accountintake.OAuthPlanResult, time.Duration, error) {
	return accountintake.OAuthPlanResult{}, 0, nil
}

func (s *oauthAccountIntakeServiceStub) Plan(context.Context, int64, string, string) (accountintake.OAuthPlanResult, error) {
	return accountintake.OAuthPlanResult{}, nil
}

func (s *oauthAccountIntakeServiceStub) Execute(context.Context, accountintake.OAuthExecuteInput) (accountintake.ExecutionResult, error) {
	return accountintake.ExecutionResult{}, nil
}

func TestOAuthAccountIntakeHTTPBindsPlatformTenantAndActorBeforeCallback(t *testing.T) {
	oauth := &oauthAccountIntakeServiceStub{}
	base := &accountIntakeServiceStub{}
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth: accountIntakeAuthStub{identity: admin.AdminIdentity{
				Source: admin.AdminSourceToken, TokenID: 11, Role: admin.RolePlatformAdmin,
			}},
			Service: base, OAuthService: oauth,
			Capabilities: allowAccountIntakeCapability{}, PlatformTenantID: 7,
		})
	})
	start := doAccountIntakeRequest(router, "/admin/v1/credentials/account-imports/oauth/start",
		`{"tenant_id":7,"vendor":"grok","auth_mode":"xai_oauth","account":{"provider_id":2,"channel_id":3,"name_prefix":"grok","account_type":"oauth"}}`)
	if start.Code != http.StatusCreated || oauth.startCalls != 1 {
		t.Fatalf("start status=%d calls=%d body=%s", start.Code, oauth.startCalls, start.Body.String())
	}
	if oauth.startInput.ActorID != "admin_token:11" || oauth.startInput.ActorRole != admin.RolePlatformAdmin || oauth.startInput.TenantID != 7 {
		t.Fatalf("start input=%+v", oauth.startInput)
	}
	callback := doAccountIntakeRequest(router, "/admin/v1/credentials/account-imports/oauth/callback",
		`{"flow_id":"flow-1","state":"state","code":"code"}`)
	if callback.Code != http.StatusOK || oauth.callbackCalls != 1 {
		t.Fatalf("callback status=%d calls=%d body=%s", callback.Code, oauth.callbackCalls, callback.Body.String())
	}
	if oauth.callbackTenant != 7 || oauth.callbackActorID != "admin_token:11" || oauth.callbackRole != admin.RolePlatformAdmin {
		t.Fatalf("callback owner=%d/%s/%s", oauth.callbackTenant, oauth.callbackActorID, oauth.callbackRole)
	}
}

func TestOAuthAccountIntakeHTTPRejectsCrossTenantBeforeService(t *testing.T) {
	oauth := &oauthAccountIntakeServiceStub{}
	router := chi.NewRouter()
	router.Route("/admin/v1/credentials", func(r chi.Router) {
		Mount(r, Deps{
			Auth:    accountIntakeAuthStub{identity: tenantTokenIdentity(8)},
			Service: &accountIntakeServiceStub{}, OAuthService: oauth,
			Capabilities: allowAccountIntakeCapability{}, PlatformTenantID: 7,
		})
	})
	rec := doAccountIntakeRequest(router, "/admin/v1/credentials/account-imports/oauth/start",
		`{"tenant_id":7,"vendor":"grok","auth_mode":"xai_oauth","account":{"provider_id":2,"channel_id":3,"name_prefix":"grok","account_type":"oauth"}}`)
	if rec.Code != http.StatusForbidden || oauth.startCalls != 0 || !strings.Contains(rec.Body.String(), "tenant scope mismatch") {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, oauth.startCalls, rec.Body.String())
	}
}
