package gatewayhttp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
)

func TestRuntimeCredentialCompatibilityRejectsWrongVendorAndUsesNextAccount(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 8101, 8102)
	innerSettler := &recordingSettler{}
	quotaFinalizer := &claudeSessionQuotaFinalizer{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{successText: "compatible account wins"}},
	}
	deps := pr5NonStreamDeps(
		t,
		selector,
		&pr5ClaimGate{claimID: 98101},
		quotaenforce.NewSettler(innerSettler, quotaFinalizer),
		dispatcher,
	)
	deps.CredentialVault = runtimeCompatibilityVault(t,
		runtimeCompatibilityAccount{
			id: 8101, vendor: credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeAIStudioAPIKey,
			secret:   "gemini-secret-must-not-dispatch",
		},
		runtimeCompatibilityAccount{
			id: 8102, vendor: credentialstore.VendorOpenAI,
			authMode: credentialstore.AuthModeAPIKey,
			secret:   "openai-compatible-secret",
		},
	)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want compatible account success", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2", selector.calls)
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[8101]; !excluded {
		t.Fatalf("second exclusions=%v want incompatible account 8101", selector.requests[1].ExcludedAccounts)
	}
	if dispatcher.calls != 1 || len(dispatcher.accounts) != 1 || dispatcher.accounts[0] != 8102 {
		t.Fatalf("dispatcher calls/accounts=%d/%v want only account 8102", dispatcher.calls, dispatcher.accounts)
	}
	if len(innerSettler.aborts) != 1 || innerSettler.aborts[0].reason != "credential_protocol_incompatible" {
		t.Fatalf("aborts=%+v want one credential_protocol_incompatible", innerSettler.aborts)
	}
	if len(innerSettler.calls) != 1 || innerSettler.calls[0].AccountID != 8102 {
		t.Fatalf("settles=%+v want only account 8102", innerSettler.calls)
	}
	if len(quotaFinalizer.releases) != 1 || len(quotaFinalizer.settles) != 1 {
		t.Fatalf("quota releases/settles=%d/%d want 1/1", len(quotaFinalizer.releases), len(quotaFinalizer.settles))
	}
	if strings.Contains(rec.Body.String(), "gemini-secret-must-not-dispatch") {
		t.Fatal("response leaked incompatible credential")
	}
}

func TestRuntimeCredentialCompatibilityOnlyWrongAccountFailsWithoutDispatch(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 8201)
	innerSettler := &recordingSettler{}
	quotaFinalizer := &claudeSessionQuotaFinalizer{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{successText: "must not dispatch"}},
	}
	deps := pr5NonStreamDeps(
		t,
		selector,
		&pr5ClaimGate{claimID: 98201},
		quotaenforce.NewSettler(innerSettler, quotaFinalizer),
		dispatcher,
	)
	deps.CredentialVault = runtimeCompatibilityVault(t, runtimeCompatibilityAccount{
		id: 8201, vendor: credentialstore.VendorAnthropic,
		authMode: credentialstore.AuthModeAPIKey,
		secret:   "anthropic-secret-must-not-dispatch",
	})
	deps.RetryBudget = retrybudget.New(1, time.Minute)
	if !deps.RetryBudget.Allow(validIdentity().TenantID) {
		t.Fatal("预占 retry budget 失败")
	}

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 || dispatcher.calls != 0 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 1/0", selector.calls, dispatcher.calls)
	}
	if len(innerSettler.aborts) != 1 || innerSettler.aborts[0].reason != "credential_protocol_incompatible" {
		t.Fatalf("aborts=%+v want one credential_protocol_incompatible", innerSettler.aborts)
	}
	if len(innerSettler.calls) != 0 || len(quotaFinalizer.releases) != 1 || len(quotaFinalizer.settles) != 0 {
		t.Fatalf("settles/quota releases/quota settles=%d/%d/%d want 0/1/0",
			len(innerSettler.calls), len(quotaFinalizer.releases), len(quotaFinalizer.settles))
	}
	if strings.Contains(rec.Body.String(), "anthropic-secret-must-not-dispatch") {
		t.Fatal("response leaked incompatible credential")
	}
}

type runtimeCompatibilityAccount struct {
	id       int64
	vendor   string
	authMode string
	secret   string
}

func runtimeCompatibilityVault(t *testing.T, accounts ...runtimeCompatibilityAccount) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, account := range accounts {
		if err := vault.Set(account.id, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: account.secret,
		}, provider.AccountInfo{
			AccountID:           account.id,
			TenantID:            validIdentity().TenantID,
			Platform:            account.vendor,
			AccountType:         account.authMode,
			AccountCredentialID: 19000 + account.id,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", account.id, err)
		}
	}
	return vault
}
