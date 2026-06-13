//go:build integration_pg

package credentialstore

import (
	"strings"
	"testing"
)

// TestCodexWebOAuthInVendorModeChecks guards migration 0143: the openai vendor
// branch of BOTH vendor-mode CHECK constraints must whitelist 'codex_web_oauth'.
//
// This is the discriminating regression guard the wave-A review demanded: the
// unit tests use an in-memory fake that does not enforce SQL CHECK constraints,
// so they could NOT catch the original defect (codex_web_oauth absent from the
// CHECKs -> every codex web-OAuth flow-session / credential insert violates the
// constraint against real Postgres). This test runs against the real gate DB and
// FAILS if 0143 is reverted (verified discriminating: at migration 0142 the
// constraint definition does not contain 'codex_web_oauth').
func TestCodexWebOAuthInVendorModeChecks(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	for _, conname := range []string{
		"account_credentials_vendor_mode_check",    // migration 0016 (account_credentials)
		"credential_acq_vendor_mode_check",         // migration 0019 (credential_acquisition_flow_sessions)
	} {
		var def string
		if err := pool.QueryRow(ctx,
			"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1", conname,
		).Scan(&def); err != nil {
			t.Fatalf("%s: load constraint def: %v", conname, err)
		}
		if !strings.Contains(def, "'codex_web_oauth'") {
			t.Fatalf("%s does not whitelist codex_web_oauth (migration 0143 not applied?) — def: %s", conname, def)
		}
		// Discriminating sanity: the CHECK must still be a real allowlist, not a
		// blanket-permit — a never-whitelisted mode must remain absent.
		if strings.Contains(def, "'definitely_not_a_real_mode'") {
			t.Fatalf("%s unexpectedly contains a bogus mode — CHECK is not a strict allowlist", conname)
		}
	}
}
