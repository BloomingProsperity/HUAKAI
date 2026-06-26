package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

func TestAPIKeyResolverIPAllowlistUsesTrustedClientIP(t *testing.T) {
	// 变异检查: 移除 bcrypt 后的 IP guard, 被拒/伪造的行就会解析成功。
	// 信任原始 X-Forwarded-For, 那么即便 socket peer 不可信, 伪造行
	// 也会解析成功。
	token := "hk_test_ip_allowlist_token"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	trustedProxy := mustClientIPResolver(t, "172.16.0.0/12")

	allow10 := "10.0.0.0/8"
	empty := ""
	blank := " , "
	cases := []struct {
		name          string
		allowlist     *string
		resolver      *clientip.Resolver
		remoteAddr    string
		xff           string
		wantErr       error
		wantTouchCall bool
	}{
		{
			name:          "direct_client_in_cidr_allows",
			allowlist:     &allow10,
			remoteAddr:    "10.1.2.3:5100",
			wantTouchCall: true,
		},
		{
			name:       "direct_client_outside_cidr_forbidden",
			allowlist:  &allow10,
			remoteAddr: "1.2.3.4:5100",
			wantErr:    ErrForbidden,
		},
		{
			name:          "empty_allowlist_does_not_restrict",
			allowlist:     &empty,
			remoteAddr:    "1.2.3.4:5100",
			wantTouchCall: true,
		},
		{
			name:          "blank_allowlist_does_not_restrict",
			allowlist:     &blank,
			remoteAddr:    "1.2.3.4:5100",
			wantTouchCall: true,
		},
		{
			name:          "trusted_proxy_forwarded_client_in_cidr_allows",
			allowlist:     &allow10,
			resolver:      trustedProxy,
			remoteAddr:    "172.16.0.10:5100",
			xff:           "10.1.2.3",
			wantTouchCall: true,
		},
		{
			name:       "forged_xff_from_untrusted_peer_cannot_bypass",
			allowlist:  &allow10,
			resolver:   trustedProxy,
			remoteAddr: "1.2.3.4:5100",
			xff:        "10.1.2.3",
			wantErr:    ErrForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := dbauth.LookupAPIKeysByPrefixRow{
				ID:           101,
				TenantID:     11,
				UserID:       22,
				KeyHash:      string(hash),
				KeyStatus:    "active",
				ExpiresAt:    pgtype.Timestamptz{},
				UserStatus:   "active",
				UserGroup:    "default",
				TenantStatus: "active",
				IpAllowlist:  tc.allowlist,
			}
			store := &fakeInboundAuthQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{row}}
			resolver := &APIKeyResolver{q: store, clientIPResolver: tc.resolver}
			req := apiKeyTestRequest("Bearer "+token, tc.remoteAddr, tc.xff)

			ident, err := resolver.Resolve(context.Background(), req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if ident.TenantID != 11 || ident.APIKeyID != 101 || ident.UserID != 22 {
					t.Fatalf("identity=%+v want tenant=11 key=101 user=22", ident)
				}
			}
			if got := store.touchCalls > 0; got != tc.wantTouchCall {
				t.Fatalf("touchCalled=%v want %v", got, tc.wantTouchCall)
			}
		})
	}
}

func TestAPIKeyResolverCarriesModelAllowlist(t *testing.T) {
	// 变异检查: 在 LookupAPIKeysByPrefix 中漏掉 ak.allowed_models, 或
	// 忘记把它拷进 Identity, dispatch 层就无法强制
	// per-key 模型限制。
	token := "hk_test_model_allowlist_token"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	allowedModels := "gpt-4o,claude-3"
	row := dbauth.LookupAPIKeysByPrefixRow{
		ID:            101,
		TenantID:      11,
		UserID:        22,
		KeyHash:       string(hash),
		KeyStatus:     "active",
		ExpiresAt:     pgtype.Timestamptz{},
		AllowedModels: &allowedModels,
		UserStatus:    "active",
		UserGroup:     "default",
		TenantStatus:  "active",
	}
	store := &fakeInboundAuthQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{row}}
	resolver := &APIKeyResolver{q: store}
	req := apiKeyTestRequest("Bearer "+token, "203.0.113.7:5100", "")

	ident, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ident.AllowedModels == nil || *ident.AllowedModels != allowedModels {
		t.Fatalf("AllowedModels=%v want %q", ident.AllowedModels, allowedModels)
	}
}

func mustClientIPResolver(t *testing.T, cidrs ...string) *clientip.Resolver {
	t.Helper()
	r, err := clientip.NewResolver(cidrs)
	if err != nil {
		t.Fatalf("clientip.NewResolver(%v): %v", cidrs, err)
	}
	return r
}

func apiKeyTestRequest(authz, remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = remoteAddr
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

type fakeInboundAuthQueries struct {
	rows       []dbauth.LookupAPIKeysByPrefixRow
	lookupErr  error
	touchErr   error
	touchCalls int
}

func (f *fakeInboundAuthQueries) LookupAPIKeysByPrefix(context.Context, string) ([]dbauth.LookupAPIKeysByPrefixRow, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.rows, nil
}

func (f *fakeInboundAuthQueries) TouchAPIKeyLastUsed(context.Context, int64) error {
	f.touchCalls++
	return f.touchErr
}
