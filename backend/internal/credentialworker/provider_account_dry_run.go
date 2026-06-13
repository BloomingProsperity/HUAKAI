package credentialworker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type ProviderAccountCredentialTestStore interface {
	LoadForProviderAccountTest(context.Context, int64, int64) (credentialstore.CredentialRecord, error)
}

type ProviderAccountCredentialTestResult struct {
	OK         bool
	ErrorClass string
	Message    string
}

func DryRunProviderAccountCredential(ctx context.Context, store ProviderAccountCredentialTestStore, registry *ModeAdapterRegistry, tenantID, accountID int64, now time.Time) (ProviderAccountCredentialTestResult, error) {
	return DryRunProviderAccountCredentialWithProbeModel(ctx, store, registry, tenantID, accountID, now, "")
}

func DryRunProviderAccountCredentialWithProbeModel(ctx context.Context, store ProviderAccountCredentialTestStore, registry *ModeAdapterRegistry, tenantID, accountID int64, now time.Time, probeModel string) (ProviderAccountCredentialTestResult, error) {
	if store == nil {
		return ProviderAccountCredentialTestResult{}, errors.New("credentialworker: provider account test store missing")
	}
	if tenantID <= 0 {
		return ProviderAccountCredentialTestResult{}, errors.New("credentialworker: tenant id must be positive")
	}
	if accountID <= 0 {
		return ProviderAccountCredentialTestResult{}, errors.New("credentialworker: provider account id must be positive")
	}
	if registry == nil {
		registry = DefaultModeAdapterRegistry()
	}
	if now.IsZero() {
		now = time.Now()
	}
	rec, err := store.LoadForProviderAccountTest(ctx, tenantID, accountID)
	if err != nil {
		if result, ok := providerAccountCredentialTestLoadFailure(err); ok {
			return result, nil
		}
		return ProviderAccountCredentialTestResult{}, err
	}
	defer privacy.Zeroize(rec.PlaintextPayload)

	adapter, ok := registry.Lookup(rec.Vendor, rec.AuthMode)
	if !ok {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrProviderAdapterMissing, rec.Vendor, rec.AuthMode, accountID)
		return providerAccountCredentialTestFailure(err), nil
	}
	if modeRequiresPersistingRefresh(rec.Vendor, rec.AuthMode) {
		return providerAccountCredentialTestUnsupported(), nil
	}
	// DRY-RUN: 不持久化 refresh 结果/健康变更。这里直接调用 adapter 验证当前凭据,
	// 不进入 AccountCredentialRefresher.Refresh,也不调用 SaveRefreshSuccess/SaveRefreshFailure。
	refreshResult, err := adapter.RefreshCredential(ctx, ModeRefreshInput{
		CredentialID: rec.ID, TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID,
		Vendor: rec.Vendor, AuthMode: rec.AuthMode, Payload: rec.PlaintextPayload, Now: now.UTC(),
		ProbeModel: strings.TrimSpace(probeModel),
	})
	if err != nil {
		if errors.Is(err, ErrNoRefreshRequired) {
			return providerAccountCredentialTestUnsupported(), nil
		}
		return providerAccountCredentialTestFailure(err), nil
	}
	privacy.Zeroize(refreshResult.Payload)
	return ProviderAccountCredentialTestResult{
		OK:      true,
		Message: "credential validation completed",
	}, nil
}

func modeRequiresPersistingRefresh(vendor, authMode string) bool {
	switch credentialstore.ModeKey(vendor, authMode) {
	case credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth),
		credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode),
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth),
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth),
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken),
		credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist),
		credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne),
		credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity),
		credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeOAuth),
		credentialstore.ModeKey(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth),
		credentialstore.ModeKey(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth):
		return true
	default:
		return false
	}
}

func providerAccountCredentialTestUnsupported() ProviderAccountCredentialTestResult {
	return ProviderAccountCredentialTestResult{
		OK:         false,
		ErrorClass: "operator_config_required",
		Message:    "non-mutating validation is not available for this credential mode; use scheduled refresh or credential rotation",
	}
}

func providerAccountCredentialTestLoadFailure(err error) (ProviderAccountCredentialTestResult, bool) {
	switch {
	case errors.Is(err, credentialstore.ErrDecryptFailed), errors.Is(err, credentialstore.ErrInvalidPayload):
		return providerAccountCredentialTestFailure(err), true
	case errors.Is(err, credentialstore.ErrCredentialAmbiguous):
		return ProviderAccountCredentialTestResult{
			OK:         false,
			ErrorClass: "operator_config_required",
			Message:    "multiple credential modes are configured; keep one testable credential mode before validation",
		}, true
	default:
		return ProviderAccountCredentialTestResult{}, false
	}
}

func providerAccountCredentialTestFailure(err error) ProviderAccountCredentialTestResult {
	class := ClassifyRefreshErrorClass(err)
	return ProviderAccountCredentialTestResult{
		OK:         false,
		ErrorClass: class,
		Message:    providerAccountCredentialTestMessage(class),
	}
}

func providerAccountCredentialTestMessage(errorClass string) string {
	switch errorClass {
	case "invalid_grant":
		return "credential authorization failed; operator re-authentication is required"
	case "rate_limit_exceeded":
		return "upstream validation is currently rate limited; retry later"
	case "risk_control_triggered":
		return "upstream risk control blocked validation; operator review is required"
	case "account_disabled":
		return "upstream account is disabled; operator review is required"
	case "payload_invalid":
		return "stored credential payload is invalid; operator correction is required"
	case "operator_config_required":
		return "operator OAuth configuration is required before validation can run"
	default:
		return "upstream validation failed temporarily; retry later"
	}
}
