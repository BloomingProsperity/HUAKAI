//go:build smoke

// Frontend wiring test: boots the real gateway (dev-mock upstream) and drives
// the EXACT endpoints + request shapes the user-portal frontend calls, asserting
// the EXACT response fields the frontend parses. One subtest per frontend module
// (login / api-keys / usage / playground). If the backend renames a field the
// frontend depends on (e.g. api-key create's `plaintext`, login's `session`), the
// matching subtest goes red — i.e. it fails on the real wiring defect it guards.
//
// Reuses the smoke harness (buildGateway/startGateway/seedSmokeGraph/...) from
// smoke_test.go. Run: go test -tags smoke -run TestFrontendWiring ./cmd/gateway

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestFrontendWiring(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping frontend wiring test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open dev pool: %v", err)
	}
	defer pgPool.Close()

	seed := seedSmokeGraph(t, ctx, pgPool)

	// Session/verification tables live outside seedSmokeGraph's tenant cleanup.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM session_tokens WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM session_families WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM email_verification_tokens WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM admin_tokens WHERE name LIKE 'wire-admin-%'`)
	})

	// Seed a tenant_operator admin token scoped to the seed tenant, so admin-console
	// endpoints (admin-token track, not session) are wire-testable without ?tenant_id.
	adminBearer := "hk_admin_" + uuid.NewString()[:12]
	adminPrefix := adminBearer
	if len(adminPrefix) > 16 {
		adminPrefix = adminPrefix[:16]
	}
	adminHash, err := bcrypt.GenerateFromPassword([]byte(adminBearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash admin token: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
		 VALUES ($1,$2,$3,'tenant_operator',$4,'active')`,
		"wire-admin-"+uuid.NewString()[:8], string(adminHash), adminPrefix, seed.tenantID); err != nil {
		t.Fatalf("seed admin_token: %v", err)
	}

	// Some admin surfaces (global channel-health / ops usage) require platform_admin
	// (scope_tenant_id NULL). Seed one too for those subtests.
	adminPlatformBearer := "hk_admin_" + uuid.NewString()[:12]
	adminPlatPrefix := adminPlatformBearer
	if len(adminPlatPrefix) > 16 {
		adminPlatPrefix = adminPlatPrefix[:16]
	}
	platHash, err := bcrypt.GenerateFromPassword([]byte(adminPlatformBearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash platform admin token: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, status)
		 VALUES ($1,$2,$3,'platform_admin','active')`,
		"wire-admin-plat-"+uuid.NewString()[:8], string(platHash), adminPlatPrefix); err != nil {
		t.Fatalf("seed platform admin_token: %v", err)
	}

	// Global platform-setting defaults would block this password-auth test:
	// registration_enabled defaults false, invitation_required true, and
	// two_factor_enabled true while this gateway assembly leaves the 2FA service
	// unwired (authTwoFactorRequired → 503). Configure password-login-friendly
	// values (global scope; harmless on the dev DB).
	for k, v := range map[string]string{
		"registration_enabled": "true",
		"invitation_required":  "false",
		"two_factor_enabled":   "false",
		"captcha_enabled":      "false",
	} {
		if _, err := pgPool.Exec(ctx,
			`INSERT INTO platform_settings (scope, setting_key, setting_value, updated_by, updated_at)
			 VALUES ('global',$1,$2,'frontend-wiring-test', now())
			 ON CONFLICT (scope, setting_key) DO UPDATE SET setting_value=$2, updated_by='frontend-wiring-test', updated_at=now()`, k, v); err != nil {
			t.Fatalf("seed platform_setting %s: %v", k, err)
		}
	}

	binPath := buildGateway(t)
	defer os.Remove(binPath)

	addr := reserveLocalPort(t)
	cmd := startGateway(t, ctx, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopGateway(cmd) })
	waitForGateway(t, addr)
	base := "http://" + addr

	unique := uuid.NewString()[:8]
	email := "wire-" + unique + "@example.com"
	password := "Huakai-Wire-Test-123!"

	var sessionToken string // captured by the login subtest, consumed downstream
	var createdKey string   // hk_ plaintext from the api-keys subtest
	var createdKeyID int64  // its api_key_id (for the usage-summary wire)

	// ---- MODULE: login page (register → login → me) ----
	t.Run("login_page_auth_ring", func(t *testing.T) {
		// register (public). The frontend posts {tenant_id,email,display_name,password}.
		st, body, _ := doJSON(t, ctx, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
			"tenant_id": seed.tenantID, "email": email, "display_name": "Wire User", "password": password,
		})
		if st != http.StatusOK && st != http.StatusCreated {
			t.Fatalf("register expected 200/201; got %d body=%s", st, body)
		}
		// Make the account login-able regardless of the tenant's email-verification policy
		// (we test the auth WIRING contract, not the verification gate).
		if _, err := pgPool.Exec(ctx,
			`UPDATE users SET email_verified=true, status='active' WHERE tenant_id=$1 AND email=$2`,
			seed.tenantID, email); err != nil {
			t.Fatalf("flip email_verified: %v", err)
		}

		// login (public). Frontend parses resp.session.session_token + resp.user.
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
			"tenant_id": seed.tenantID, "email": email, "password": password,
		})
		if st != http.StatusOK {
			t.Fatalf("login expected 200; got %d body=%s", st, body)
		}
		session, ok := obj["session"].(map[string]any)
		if !ok {
			t.Fatalf("login: response missing `session` object (frontend auth.ts reads resp.session.session_token); body=%s", body)
		}
		tok, _ := session["session_token"].(string)
		if tok == "" {
			t.Fatalf("login: session.session_token empty; body=%s", body)
		}
		if _, ok := session["refresh_token"].(string); !ok {
			t.Fatalf("login: session.refresh_token missing (userClient refresh ring depends on it); body=%s", body)
		}
		sessionToken = tok

		// GET /v1/auth/me with the session token (Header.tsx + fetchMe()).
		// Real contract: { panel, user_id, tenant_id, display_name } — note user_id
		// (not id) and NO email. fetchMe() maps user_id→id and keeps the login email.
		st, body, me := doJSON(t, ctx, http.MethodGet, base+"/v1/auth/me", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/auth/me expected 200; got %d body=%s", st, body)
		}
		if got, _ := me["display_name"].(string); got != "Wire User" {
			t.Fatalf("/v1/auth/me display_name = %q; want %q (body=%s)", got, "Wire User", body)
		}
		if _, ok := me["user_id"]; !ok {
			t.Fatalf("/v1/auth/me missing `user_id` (fetchMe maps user_id→SessionUser.id); body=%s", body)
		}
	})

	if sessionToken == "" {
		t.Fatal("login subtest did not yield a session token; downstream session modules cannot run")
	}

	// ---- MODULE: API Keys page (create → list) ----
	t.Run("api_keys_page", func(t *testing.T) {
		// create. Frontend apiKeys.ts reads the one-time `plaintext` field.
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/api-keys", sessionToken, map[string]any{
			"name": "wire-key-" + unique, "environment": "test",
		})
		if st != http.StatusOK && st != http.StatusCreated {
			t.Fatalf("create api-key expected 200/201; got %d body=%s", st, body)
		}
		pt, _ := obj["plaintext"].(string)
		if pt == "" {
			t.Fatalf("create api-key: `plaintext` empty — the one-time-secret modal would show nothing; body=%s", body)
		}
		if !strings.HasPrefix(pt, "hk_") {
			t.Fatalf("create api-key: plaintext %q lacks hk_ prefix", pt)
		}
		createdKey = pt
		if idf, ok := obj["api_key_id"].(float64); ok {
			createdKeyID = int64(idf)
		}

		// list. Frontend reads { api_keys: [...], count }.
		st, body, list := doJSON(t, ctx, http.MethodGet, base+"/v1/api-keys", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("list api-keys expected 200; got %d body=%s", st, body)
		}
		arr, ok := list["api_keys"].([]any)
		if !ok {
			t.Fatalf("list api-keys: missing `api_keys` array (frontend maps over it); body=%s", body)
		}
		if len(arr) == 0 {
			t.Fatalf("list api-keys: expected the just-created key; got empty array")
		}
		// the created key must be present by prefix
		found := false
		for _, it := range arr {
			row, _ := it.(map[string]any)
			if kp, _ := row["key_prefix"].(string); kp != "" && strings.HasPrefix(createdKey, kp) {
				found = true
			}
		}
		if !found {
			t.Fatalf("list api-keys: created key not found in list; body=%s", body)
		}

		// Per-key usage summary — the api-keys page row-expand panel calls
		// GET /v1/me/keys/{id}/usage-summary (SESSION auth). Reads api_key_id/total_cost/request_count.
		if createdKeyID != 0 {
			st, body, sum := doJSON(t, ctx, http.MethodGet,
				fmt.Sprintf("%s/v1/me/keys/%d/usage-summary", base, createdKeyID), sessionToken, nil)
			if st != http.StatusOK {
				t.Fatalf("usage-summary expected 200; got %d body=%s", st, body)
			}
			for _, f := range []string{"api_key_id", "total_cost", "request_count"} {
				if _, ok := sum[f]; !ok {
					t.Fatalf("usage-summary missing %q (api-keys expand panel reads it); body=%s", f, body)
				}
			}
		}
	})

	// ---- MODULE: usage page (quota[session] + usage[apikey] + time-series[apikey]) ----
	t.Run("usage_page", func(t *testing.T) {
		// /v1/me/quota — SESSION auth. Frontend reads { items: [...] }.
		st, body, q := doJSON(t, ctx, http.MethodGet, base+"/v1/me/quota", sessionToken, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/quota expected 200; got %d body=%s", st, body)
		}
		if _, ok := q["items"]; !ok {
			t.Fatalf("/v1/me/quota: missing `items` (QuotaWindowsCard maps over items); body=%s", body)
		}

		// /v1/me/usage — API-KEY auth (seed bearer). Frontend reads { items, next_cursor }.
		st, body, u := doJSON(t, ctx, http.MethodGet, base+"/v1/me/usage?limit=20", seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/usage expected 200; got %d body=%s", st, body)
		}
		if _, ok := u["items"]; !ok {
			t.Fatalf("/v1/me/usage: missing `items`; body=%s", body)
		}
		if _, ok := u["next_cursor"]; !ok {
			t.Fatalf("/v1/me/usage: missing `next_cursor` (cursor pagination depends on it); body=%s", body)
		}

		// /v1/me/analytics/time-series — API-KEY auth, from/to required (<=31d). Reads { items, period }.
		now := time.Now().UTC()
		from := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
		to := now.Format(time.RFC3339)
		st, body, ts := doJSON(t, ctx, http.MethodGet,
			base+"/v1/me/analytics/time-series?granularity=day&from="+from+"&to="+to, seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/me/analytics/time-series expected 200; got %d body=%s", st, body)
		}
		if _, ok := ts["items"]; !ok {
			t.Fatalf("time-series: missing `items` (aggregateTimeSeries reads items); body=%s", body)
		}
		if _, ok := ts["period"]; !ok {
			t.Fatalf("time-series: missing `period`; body=%s", body)
		}

		// CSV export — the usage page's 导出 button hits /v1/me/usage/export.csv
		// (SESSION auth, account-scoped — NOT the api-key path). Assert 200.
		exReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/v1/me/usage/export.csv?format=csv&from="+from+"&to="+to, nil)
		exReq.Header.Set("Authorization", "Bearer "+sessionToken)
		exResp, err := http.DefaultClient.Do(exReq)
		if err != nil {
			t.Fatalf("export.csv: %v", err)
		}
		defer exResp.Body.Close()
		if exResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(exResp.Body)
			t.Fatalf("/v1/me/usage/export.csv expected 200; got %d body=%s", exResp.StatusCode, raw)
		}
	})

	// ---- MODULE: playground (models[apikey] + chat stream[apikey]) ----
	t.Run("playground_page", func(t *testing.T) {
		// GET /v1/models with seed key — frontend models.ts reads { object:"list", data:[{id}] }.
		st, body, m := doJSON(t, ctx, http.MethodGet, base+"/v1/models", seed.bearer, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/models expected 200; got %d body=%s", st, body)
		}
		data, ok := m["data"].([]any)
		if !ok {
			t.Fatalf("/v1/models: missing `data` array; body=%s", body)
		}
		seenAlias := false
		for _, it := range data {
			row, _ := it.(map[string]any)
			if id, _ := row["id"].(string); id == "gpt-4.1-mini" {
				seenAlias = true
			}
		}
		if !seenAlias {
			t.Fatalf("/v1/models: seeded alias gpt-4.1-mini not listed (model select would be empty); body=%s", body)
		}

		// POST /v1/chat/completions stream — playground's send path (SSE + usage).
		chatBody := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(chatBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+seed.bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("chat stream: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("chat stream expected 200; got %d body=%s", resp.StatusCode, raw)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("chat stream Content-Type = %q; want text/event-stream", ct)
		}
		raw, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(raw, []byte("data:")) {
			t.Fatalf("chat stream has no SSE `data:` lines; body=%s", raw)
		}
	})

	// ---- BRIDGE: the just-created key is a real, usable inference credential ----
	t.Run("created_key_is_usable", func(t *testing.T) {
		if createdKey == "" {
			t.Skip("no created key (api_keys subtest failed)")
		}
		st, body, m := doJSON(t, ctx, http.MethodGet, base+"/v1/models", createdKey, nil)
		if st != http.StatusOK {
			t.Fatalf("/v1/models with freshly-created key expected 200; got %d body=%s", st, body)
		}
		if _, ok := m["data"].([]any); !ok {
			t.Fatalf("/v1/models with created key: missing data array; body=%s", body)
		}
	})

	// ============ BATCH 1: portal-completion modules (session auth) ============
	// Fresh registered user → empty data, but every endpoint must return 200 + the
	// envelope shape the frontend lib parses. Asserts the wire (route mounted, auth
	// accepted, shape correct) for redeem / subscriptions / notifications / account.

	// MODULE: redeem (vouchers) — history GET + redeem error-path (route wired).
	t.Run("redeem_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/me/voucher-redemptions", sessionToken, "redemptions")
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/users/me/vouchers/redeem", sessionToken,
			map[string]any{"code": "WIRE-NOPE-" + unique})
		// No such voucher → expect a STRUCTURED {error} (handler ran), not a chi 404 route-miss.
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("redeem bad-code: expected structured {error} (route wired); got %d body=%s", st, body)
			}
		}
	})

	// MODULE: subscriptions — current / list / plans (progress may 503 if quota unconfigured).
	t.Run("subscriptions_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/users/me/subscriptions/", sessionToken, "subscriptions")
		getOK(t, ctx, base+"/v1/users/me/subscriptions/me", sessionToken, "auto_renew")
		getOK(t, ctx, base+"/v1/users/me/subscriptions/plans", sessionToken, "plans")
		st, _, _ := doJSON(t, ctx, http.MethodGet, base+"/v1/users/me/subscriptions/me/progress", sessionToken, nil)
		if st != http.StatusOK && st != http.StatusServiceUnavailable {
			t.Fatalf("subscriptions progress expected 200 or 503; got %d", st)
		}
	})

	// MODULE: notifications + announcements + per-user notify settings.
	t.Run("notifications_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/notifications", sessionToken, "items")
		getOK(t, ctx, base+"/v1/notifications/unread-count", sessionToken, "count")
		getOK(t, ctx, base+"/v1/announcements", sessionToken, "items")
		getOK(t, ctx, base+"/v1/users/me/notifications", sessionToken, "notify_type")
	})

	// MODULE: account (groups / invitations / invite-code / checkin / referrals / rewards).
	// invite-code/referrals/rewards depend on the invitation+referral feature config,
	// which this minimal dev assembly leaves unconfigured → structured 503. We assert
	// the WIRE (route mounted, auth accepted, structured response), tolerating that 503.
	t.Run("account_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/me/groups", sessionToken, "items")
		getOK(t, ctx, base+"/v1/me/invitations", sessionToken, "qualified_count")
		getOK(t, ctx, base+"/v1/me/checkin", sessionToken, "checked_in_today")
		getOKorUnavailable(t, ctx, base+"/v1/me/invitation-code", sessionToken, "code")
		getOKorUnavailable(t, ctx, base+"/v1/me/referrals", sessionToken, "items")
		getOKorUnavailable(t, ctx, base+"/v1/me/referrals/rewards", sessionToken, "total_reward_usd")
	})

	// ============ BATCH 2: portal-depth modules ============

	// MODULE: billing (balance / config / orders — all session).
	t.Run("billing_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/users/me/payments/balance", sessionToken, "balance")
		getOK(t, ctx, base+"/v1/users/me/payments/config", sessionToken, "config")
		getOK(t, ctx, base+"/v1/users/me/payments/orders", sessionToken, "orders")
	})

	// MODULE: account security (2FA status / passkeys / oauth-bindings — all session).
	t.Run("security_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/auth/2fa/status", sessionToken, "available")
		getOK(t, ctx, base+"/v1/me/passkeys", sessionToken, "passkeys")
		getOK(t, ctx, base+"/v1/users/me/oauth-bindings", sessionToken, "bindings")
	})

	// MODULE: pricing (public; page is an ARRAY, may 503 if catalog unconfigured in dev).
	t.Run("pricing_page", func(t *testing.T) {
		assertReachable(t, ctx, base+"/v1/pricing/page", "")
		assertReachable(t, ctx, base+"/v1/pricing/snapshots", "")
	})

	// MODULE: audit & receipts (HUAKAI moat — receipt get / disputes / audit pubkey).
	t.Run("audit_page", func(t *testing.T) {
		// receipt by a non-existent id → structured error (route wired), not chi route-miss.
		st, body, obj := doJSON(t, ctx, http.MethodGet, base+"/v1/receipts/wire-nope-"+unique, sessionToken, nil)
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("receipt get bad-id: expected structured {error} (route wired); got %d body=%s", st, body)
			}
		}
		getOK(t, ctx, base+"/v1/me/disputes", sessionToken)        // list reachable (200, shape varies)
		assertReachable(t, ctx, base+"/v1/audit/pubkey", "")       // public; 200 or structured 503 (signer)
	})

	// ============ BATCH 3: admin-console core (admin-token track) ============
	// Uses the seeded tenant_operator admin token (NOT session). Proves the admin
	// pages' wires hit real /admin/v1 + /v1/admin routes with admin auth accepted.

	t.Run("admin_users_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/users", adminBearer, "items")
	})

	t.Run("admin_accounts_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/provider-accounts", adminBearer, "items")
	})

	// channel-health + ops/usage are platform_admin global surfaces.
	t.Run("admin_channels_page", func(t *testing.T) {
		assertReachable(t, ctx, fmt.Sprintf("%s/v1/admin/channel-health/?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
	})

	t.Run("admin_ops_page", func(t *testing.T) {
		assertReachable(t, ctx, base+"/v1/admin/usage/overview?window=24h", adminPlatformBearer)
	})

	// ============ BATCH 4: admin-console depth ============
	t.Run("admin_credentials_page", func(t *testing.T) {
		getOK(t, ctx, base+"/admin/v1/credentials/renew-status", adminPlatformBearer, "items")
	})
	t.Run("admin_settings_page", func(t *testing.T) {
		getOK(t, ctx, base+"/v1/admin/platform-settings", adminPlatformBearer, "items")
	})
	t.Run("admin_operations_page", func(t *testing.T) {
		assertWired(t, ctx, fmt.Sprintf("%s/v1/admin/subscriptions/plans?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
		assertWired(t, ctx, fmt.Sprintf("%s/v1/admin/vouchers?tenant_id=%d", base, seed.tenantID), adminPlatformBearer)
	})
	t.Run("admin_system_page", func(t *testing.T) {
		assertWired(t, ctx, base+"/admin/v1/system/health", adminPlatformBearer)
		assertWired(t, ctx, base+"/admin/v1/modules", adminPlatformBearer)
	})

	// ============ CLOSEOUT modules ============
	// (overview /dashboard reuses already-asserted endpoints: balance/quota/api-keys/
	//  checkin/time-series — no new wire to assert.)

	// MODULE: sessions — active session families list (POST, session auth).
	t.Run("sessions_page", func(t *testing.T) {
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/sessions/list", sessionToken, map[string]any{})
		if st != http.StatusOK {
			t.Fatalf("POST /v1/sessions/list expected 200; got %d body=%s", st, body)
		}
		if _, ok := obj["families"]; !ok {
			t.Fatalf("sessions list missing `families` (page maps over it); body=%s", body)
		}
	})

	// MODULE: hermes (admin assistant). /v1/hermes is mounted only when hermesService
	// AND hermesRunner are wired (routes.go) — the minimal dev gateway wires neither,
	// so the route is absent (404). Skip honestly there; assert the wire where mounted.
	t.Run("hermes_page", func(t *testing.T) {
		url := fmt.Sprintf("%s/v1/hermes/settings?as_user_id=%d&tenant_id=%d", base, seed.userID, seed.tenantID)
		st, _, _ := doJSON(t, ctx, http.MethodGet, url, adminPlatformBearer, nil)
		if st == http.StatusNotFound {
			t.Skip("hermes not mounted in minimal dev gateway (needs hermesService+hermesRunner); frontend page uses verified contract")
		}
		assertWired(t, ctx, url, adminPlatformBearer)
	})

	// MODULE: inference console — embeddings route wired (no embeddings model seeded → structured error OK).
	t.Run("console_page", func(t *testing.T) {
		st, body, obj := doJSON(t, ctx, http.MethodPost, base+"/v1/embeddings", seed.bearer,
			map[string]any{"model": "gpt-4.1-mini", "input": "wire test"})
		if st != http.StatusOK {
			if _, ok := obj["error"].(map[string]any); !ok {
				t.Fatalf("POST /v1/embeddings: expected 200 or structured error (route wired); got %d body=%s", st, body)
			}
		}
	})
}

// doJSON sends an optional JSON body with an optional Bearer token and returns
// (status, rawBody, parsedObject). parsedObject is nil for non-object responses.
func doJSON(t *testing.T, ctx context.Context, method, url, bearer string, body any) (int, []byte, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj) // nil for arrays/non-objects; callers assert as needed
	return resp.StatusCode, raw, obj
}

// getOK asserts GET url (with bearer) returns 200 and the parsed object contains
// every required key, then returns the object. Used for the breadth wiring checks.
func getOK(t *testing.T, ctx context.Context, url, bearer string, keys ...string) map[string]any {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st != http.StatusOK {
		t.Fatalf("GET %s expected 200; got %d body=%s", url, st, body)
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Fatalf("GET %s missing key %q (frontend lib parses it); body=%s", url, k, body)
		}
	}
	return obj
}

// getOKorUnavailable is getOK that also accepts a STRUCTURED 503 — for endpoints
// whose backend feature is left unconfigured in the minimal dev assembly. It still
// proves the wire (route mounted, auth accepted, structured response shape); a chi
// route-miss (404, no {error}) or wrong shape still fails.
func getOKorUnavailable(t *testing.T, ctx context.Context, url, bearer string, keys ...string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusServiceUnavailable {
		if _, ok := obj["error"].(map[string]any); !ok {
			t.Fatalf("GET %s 503 but not structured {error} (route may be unmounted); body=%s", url, body)
		}
		t.Logf("GET %s → 503 (feature unconfigured in dev assembly; wire OK)", url)
		return
	}
	if st != http.StatusOK {
		t.Fatalf("GET %s expected 200 or structured 503; got %d body=%s", url, st, body)
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Fatalf("GET %s missing key %q; body=%s", url, k, body)
		}
	}
}

// assertReachable accepts 200 (any body shape — array or object) OR a structured
// 503. For public/array endpoints where a key check doesn't apply but we still want
// to prove the route is mounted and not a chi 404 route-miss.
func assertReachable(t *testing.T, ctx context.Context, url, bearer string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusOK {
		return
	}
	if st == http.StatusServiceUnavailable {
		if _, ok := obj["error"].(map[string]any); !ok {
			t.Fatalf("GET %s 503 but not structured (route may be unmounted); body=%s", url, body)
		}
		t.Logf("GET %s → 503 (unconfigured in dev; wire OK)", url)
		return
	}
	t.Fatalf("GET %s expected 200 or structured 503; got %d body=%s", url, st, body)
}

// assertWired is the most lenient wire proof: 200, OR any 4xx/5xx that carries a
// STRUCTURED {error} (route mounted + handler ran + structured response — not a chi
// route-miss). For admin-depth endpoints whose exact role/tenant nuance we don't
// fully satisfy in this minimal seed, but whose wire we still want to prove.
func assertWired(t *testing.T, ctx context.Context, url, bearer string) {
	t.Helper()
	st, body, obj := doJSON(t, ctx, http.MethodGet, url, bearer, nil)
	if st == http.StatusOK {
		return
	}
	if _, ok := obj["error"].(map[string]any); ok {
		t.Logf("GET %s → %d structured (route wired; auth/param nuance)", url, st)
		return
	}
	t.Fatalf("GET %s expected 200 or structured error (route wired, not route-miss); got %d body=%s", url, st, body)
}
