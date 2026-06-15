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
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
	})

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
