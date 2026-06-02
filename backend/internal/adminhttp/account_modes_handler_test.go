package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmode"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestAccountModesHandlerRequiresAdminAuth(t *testing.T) {
	provider := &accountModeProviderStub{catalog: safeAccountModeCatalog()}
	rec := invokeAccountModes(t, AdminAccountModesDeps{
		Auth:    accountModeAuthStub{err: admin.ErrAdminUnauthorized},
		Catalog: provider,
	}, http.MethodGet, "/admin/v1/account-modes")

	assertAccountModeStatus(t, rec, http.StatusUnauthorized)
	if provider.called {
		t.Fatalf("unauthorized request touched catalog provider")
	}
}

func TestAccountModesHandlerReturnsCatalogForPlatformAndTenantOperators(t *testing.T) {
	for _, ident := range []admin.AdminIdentity{platformAdmin(), tenantOperator(7)} {
		provider := &accountModeProviderStub{catalog: safeAccountModeCatalog()}
		rec := invokeAccountModes(t, AdminAccountModesDeps{
			Auth:    accountModeAuthStub{ident: ident},
			Catalog: provider,
		}, http.MethodGet, "/admin/v1/account-modes")

		assertAccountModeStatus(t, rec, http.StatusOK)
		var body accountmode.Catalog
		decodeAccountModeBody(t, rec, &body)
		if len(body.Modes) != 1 || body.Modes[0].Vendor != "openai" || body.Modes[0].AuthMode != "api_key" {
			t.Fatalf("catalog response mismatch: %+v", body)
		}
	}
}

func TestAccountModesHandlerDoesNotLeakSecretsOrUnavailableModes(t *testing.T) {
	catalog := accountmode.Catalog{Modes: []accountmode.Mode{{
		Vendor:         "openai",
		AuthMode:       "api_key",
		FlowKind:       credentialacq.FlowKindPaste,
		AllowedHelpers: []credentialacq.FlowKind{credentialacq.FlowKindPaste},
		RequiredFields: []credentialacq.FieldSpec{{
			Name:      "api_key",
			Kind:      credentialacq.FieldKindSecret,
			Required:  true,
			Redaction: credentialacq.RedactionSecret,
			Group:     credentialacq.FieldGroupCredential,
		}},
		IsEnabled: true,
		RiskLevel: credentialacq.RiskLevelLow,
	}}}
	rec := invokeAccountModes(t, AdminAccountModesDeps{
		Auth:    accountModeAuthStub{ident: platformAdmin()},
		Catalog: &accountModeProviderStub{catalog: catalog},
	}, http.MethodGet, "/admin/v1/account-modes")

	assertAccountModeStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, forbidden := range []string{"sk-live-secret", "plaintext"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("account mode response leaked %q: %s", forbidden, body)
		}
	}
	var parsed struct {
		Modes []accountmode.Mode `json:"modes"`
	}
	decodeAccountModeBody(t, rec, &parsed)
	if len(parsed.Modes) != 1 {
		t.Fatalf("expected one visible mode, got %+v", parsed)
	}
	if parsed.Modes[0].IsExperimental || !parsed.Modes[0].IsEnabled {
		t.Fatalf("unavailable mode leaked: %+v", parsed.Modes[0])
	}
}

func safeAccountModeCatalog() accountmode.Catalog {
	return accountmode.Catalog{Modes: []accountmode.Mode{{
		Vendor:               credentialstore.VendorOpenAI,
		AuthMode:             credentialstore.AuthModeAPIKey,
		FlowKind:             credentialacq.FlowKindPaste,
		ClientIdentitySource: credentialacq.ClientSourceNone,
		AllowedHelpers:       []credentialacq.FlowKind{credentialacq.FlowKindPaste},
		RequiredFields: []credentialacq.FieldSpec{{
			Name:      "api_key",
			Kind:      credentialacq.FieldKindSecret,
			Required:  true,
			Redaction: credentialacq.RedactionSecret,
			Group:     credentialacq.FieldGroupCredential,
		}},
		IsEnabled: true,
		RiskLevel: credentialacq.RiskLevelLow,
	}}}
}

type accountModeAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s accountModeAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type accountModeProviderStub struct {
	catalog accountmode.Catalog
	err     error
	called  bool
}

func (s *accountModeProviderStub) Catalog(context.Context) (accountmode.Catalog, error) {
	s.called = true
	if s.err != nil {
		return accountmode.Catalog{}, s.err
	}
	return s.catalog, nil
}

func invokeAccountModes(t *testing.T, deps AdminAccountModesDeps, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/admin/v1/account-modes", NewAccountModeListHandler(deps))
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeAccountModeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertAccountModeStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}
