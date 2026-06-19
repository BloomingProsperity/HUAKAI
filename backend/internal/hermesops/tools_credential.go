package hermesops

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
)

// CredentialDiagnoseDeps are the read-only dependencies the credential_diagnose
// tool wraps. Each is an EXISTING gateway read; the tool reimplements none of
// the logic.
//
//   - DryRun wraps credentialworker.DryRunProviderAccountCredential — a
//     NON-PERSISTENT credential validation (it explicitly zeroizes plaintext and
//     never calls SaveRefreshSuccess/SaveRefreshFailure).
//   - RenewStore wraps credentialstore.Store.ListRenewStatus — a SELECT-only
//     read of credential renew metadata (states, failure classes, counts).
type CredentialDiagnoseDeps struct {
	// DryRun is injected (rather than the concrete func) so the tool fails
	// closed when unwired and is unit-testable with a fake.
	DryRun func(ctx context.Context, store credentialworker.ProviderAccountCredentialTestStore, registry *credentialworker.ModeAdapterRegistry, tenantID, accountID int64, now time.Time) (credentialworker.ProviderAccountCredentialTestResult, error)
	// TestStore is the credential test store the dry-run reads from.
	TestStore credentialworker.ProviderAccountCredentialTestStore
	// Registry is the mode-adapter registry; nil is tolerated by the underlying
	// function (it falls back to the default), so it is not a wiring failure.
	Registry *credentialworker.ModeAdapterRegistry
	// RenewStatus wraps the SELECT-only renew-status read.
	RenewStatus func(ctx context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error)
}

// CredentialDiagnoseSpec builds the read-only credential_diagnose tool. It
// validates a provider account's stored credential without persisting anything
// (dry-run) and folds in the SELECT-only renew status for that tenant.
//
// Args: { "account_id": <int> }  (required)
//
// Result summary (system-diagnostic only): the dry-run ok/error_class, and for
// the named account its renew state / failure class / failure count — NO secrets,
// NO credential bytes, NO refresh tokens.
func CredentialDiagnoseSpec(deps CredentialDiagnoseDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolCredentialDiagnose,
		Category:     CategoryDiagnostic,
		Description:  "Validate a provider account's stored credential (non-persistent dry-run) and report its renew status.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{"account_id": "provider account id (positive integer, required)"},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.DryRun == nil || deps.TestStore == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return ToolResult{}, err
			}

			dr, err := deps.DryRun(ctx, deps.TestStore, deps.Registry, req.TenantID, accountID, time.Time{})
			if err != nil {
				return ToolResult{}, err
			}

			summary := map[string]any{
				"account_id":             accountID,
				"credential_ok":          dr.OK,
				"credential_error_class": emptyToNil(dr.ErrorClass),
				// dr.Message is a fixed operator-guidance string keyed off the
				// error class (no user data), so it is safe to surface.
				"credential_detail": dr.Message,
			}

			errorClass := ""
			if !dr.OK {
				errorClass = dr.ErrorClass
			}

			// Optional: fold in the renew status for the named account when the
			// SELECT-only read is wired. A missing renew dep is not fatal — the
			// dry-run is the primary diagnostic.
			if deps.RenewStatus != nil {
				tenant := req.TenantID
				rows, rerr := deps.RenewStatus(ctx, credentialstore.ListRenewStatusParams{
					TenantID: &tenant,
					Limit:    200,
				})
				if rerr != nil {
					summary["renew_status_error"] = "renew_status_read_failed"
				} else {
					summary["renew_status"] = renewStatusForAccount(rows, accountID)
				}
			}

			return ToolResult{Summary: summary, ErrorClass: errorClass}, nil
		},
	}
}

// renewStatusForAccount projects the renew-status rows for one account into a
// diagnostic-only shape (states / classes / counts / ids). It DROPS every
// free-form / identity field that is not strictly diagnostic. Returns nil when
// the account has no matching credential row.
func renewStatusForAccount(rows []credentialstore.RenewStatusMetadata, accountID int64) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r.AccountID != accountID {
			continue
		}
		out = append(out, map[string]any{
			"credential_id":        r.CredentialID,
			"vendor":               r.Vendor,
			"auth_mode":            r.AuthMode,
			"state":                r.State,
			"credential_version":   r.CredentialVersion,
			"access_expires_at":    timePtrAny(r.AccessExpiresAt),
			"refresh_before_at":    timePtrAny(r.RefreshBeforeAt),
			"last_refresh_at":      timePtrAny(r.LastRefreshAt),
			"last_refresh_outcome": deref(r.LastRefreshOutcome),
			"failure_class":        deref(r.FailureClass),
			"failure_count":        r.FailureCount,
		})
	}
	return out
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// timePtrAny nil-guards a *time.Time for diagnostic projection: nil stays nil,
// otherwise the instant is normalized to UTC. These are credential-timing
// timestamps (expiry / refresh deadline / last refresh) — never secret material.
func timePtrAny(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
