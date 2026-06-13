package credentialacq

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
)

// RedactedContext keys carrying the auto-extracted upstream account identity. These
// hold ONLY the non-secret account id / email / provenance — never the raw id_token
// or any bearer material, so they survive ValidateRedactedContext's secret scrubber.
const (
	RedactedKeyUpstreamAccountID    = "upstream_account_id"
	RedactedKeyUpstreamAccountEmail = "upstream_account_email"
	RedactedKeyAccountIDSource      = "account_id_source"
)

// AttachIdentity threads an auto-extracted upstream account identity onto a credential
// candidate: it sets the durable candidate fields (persisted as queryable columns) and
// mirrors the non-secret values into RedactedContext (immediate audit/UI surface, zero
// schema change). It is a no-op for an empty identity so manual/operator binding wins.
// The raw id_token is never placed here — only the extracted, non-secret values.
func AttachIdentity(candidate *CredentialCandidate, identity accountident.Identity) {
	if candidate == nil {
		return
	}
	accountID := strings.TrimSpace(identity.AccountID)
	candidate.AccountIDSource = strings.TrimSpace(identity.Source)
	if identity.Empty() {
		// Record provenance even when nothing was extracted so the UI can show the
		// binding fell back to manual, but add no id/email keys.
		if candidate.AccountIDSource != "" {
			candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeyAccountIDSource, candidate.AccountIDSource)
		}
		return
	}
	candidate.ExternalAccountID = accountID
	candidate.ExternalAccountEmail = strings.TrimSpace(identity.Email)

	ctx := candidate.RedactedContext
	ctx = setRedactedKey(ctx, RedactedKeyUpstreamAccountID, accountID)
	if candidate.ExternalAccountEmail != "" {
		ctx = setRedactedKey(ctx, RedactedKeyUpstreamAccountEmail, candidate.ExternalAccountEmail)
	}
	if candidate.AccountIDSource != "" {
		ctx = setRedactedKey(ctx, RedactedKeyAccountIDSource, candidate.AccountIDSource)
	}
	candidate.RedactedContext = ctx
}

func setRedactedKey(ctx map[string]any, key, value string) map[string]any {
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx[key] = value
	return ctx
}
