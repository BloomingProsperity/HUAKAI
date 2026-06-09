package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// fakeIPBlacklistQueries is a minimal apiKeyQueries stub for blacklist tests.
type fakeIPBlacklistQueries struct {
	rows []dbauth.LookupAPIKeysByPrefixRow
}

func (f *fakeIPBlacklistQueries) LookupAPIKeysByPrefix(_ context.Context, _ string) ([]dbauth.LookupAPIKeysByPrefixRow, error) {
	return f.rows, nil
}

func (f *fakeIPBlacklistQueries) TouchAPIKeyLastUsed(_ context.Context, _ int64) error {
	return nil
}

func makeBlacklistRow(t *testing.T, bearer string, ipBlacklist *string) dbauth.LookupAPIKeysByPrefixRow {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return dbauth.LookupAPIKeysByPrefixRow{
		ID:            1,
		TenantID:      10,
		UserID:        20,
		KeyHash:       string(hash),
		KeyStatus:     "active",
		ExpiresAt:     pgtype.Timestamptz{Valid: false},
		IpAllowlist:   nil, // no allowlist restriction
		IpBlacklist:   ipBlacklist,
		AllowedModels: nil,
		UserStatus:    "active",
		UserGroup:     "default",
		TenantStatus:  "active",
	}
}

func buildResolverRequest(bearer string, remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	// nil clientIPResolver falls back to RemoteAddr — set it directly.
	req.RemoteAddr = remoteAddr + ":12345"
	return req
}

// TestIPBlacklistDeny is the discriminating test for KEY-016.
//
// MUTATION: move deny check AFTER allowlist check (allowlist nil -> allow-all
// short-circuits before the deny can fire) -> 1.2.3.4 would be allowed -> RED.
func TestIPBlacklistDeny(t *testing.T) {
	const bearer = "hk_live_blacklisttest0001"
	blacklisted := "1.2.3.4/32"

	t.Run("blacklisted IP is denied", func(t *testing.T) {
		q := &fakeIPBlacklistQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{makeBlacklistRow(t, bearer, &blacklisted)}}
		r := auth.NewAPIKeyResolverWithFakeQueries(q)
		req := buildResolverRequest(bearer, "1.2.3.4")
		_, err := r.Resolve(context.Background(), req)
		if !errors.Is(err, auth.ErrForbidden) {
			t.Errorf("expected ErrForbidden for blacklisted IP, got: %v", err)
		}
	})

	t.Run("non-blacklisted IP is allowed", func(t *testing.T) {
		q := &fakeIPBlacklistQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{makeBlacklistRow(t, bearer, &blacklisted)}}
		r := auth.NewAPIKeyResolverWithFakeQueries(q)
		req := buildResolverRequest(bearer, "9.9.9.9")
		ident, err := r.Resolve(context.Background(), req)
		if err != nil {
			t.Errorf("expected success for non-blacklisted IP, got: %v", err)
		}
		if ident.APIKeyID != 1 {
			t.Errorf("expected APIKeyID=1, got %d", ident.APIKeyID)
		}
	})

	t.Run("nil blacklist never denies", func(t *testing.T) {
		q := &fakeIPBlacklistQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{makeBlacklistRow(t, bearer, nil)}}
		r := auth.NewAPIKeyResolverWithFakeQueries(q)
		req := buildResolverRequest(bearer, "1.2.3.4")
		_, err := r.Resolve(context.Background(), req)
		if err != nil {
			t.Errorf("expected success for nil blacklist, got: %v", err)
		}
	})
}
