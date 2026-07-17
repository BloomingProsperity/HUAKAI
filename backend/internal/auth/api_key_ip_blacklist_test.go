package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// fakeIPBlacklistQueries 是用于黑名单测试的最小化 apiKeyQueries 桩。
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
		IpAllowlist:   nil, // 无 allowlist 限制
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
	// clientIPResolver 为 nil 时回退到 RemoteAddr —— 这里直接设置它。
	req.RemoteAddr = remoteAddr + ":12345"
	return req
}

// TestIPBlacklistDeny 是 KEY-016 的判别性测试,同时守住源 SQL 投影与
// resolver 的 deny 优先语义。
//
// 变异:删除源 SQL 的 ip_blacklist 投影会让投影子测试变红;让返回行的
// IpBlacklist 恒空或在 allowlist 命中后提前放行,会让拒绝断言变红。
func TestIPBlacklistDeny(t *testing.T) {
	const bearer = "hk_live_blacklisttest0001"
	blacklisted := "1.2.3.4/32"
	allowlisted := "1.2.3.4/32"

	t.Run("source query projects blacklist", func(t *testing.T) {
		raw, err := os.ReadFile("../../sql/queries/auth_inbound.sql")
		if err != nil {
			t.Fatalf("读取 auth_inbound.sql: %v", err)
		}
		const marker = "-- name: LookupAPIKeysByPrefix :many"
		_, queryTail, found := strings.Cut(string(raw), marker)
		if !found {
			t.Fatalf("auth_inbound.sql 缺少 LookupAPIKeysByPrefix 查询")
		}
		queryBody, _, _ := strings.Cut(queryTail, "\n-- name:")
		normalized := strings.Join(strings.Fields(queryBody), " ")
		if !strings.Contains(normalized, "ak.ip_allowlist, ak.ip_blacklist, ak.allowed_models") {
			t.Fatalf("LookupAPIKeysByPrefix 必须按顺序投影 ip_allowlist、ip_blacklist、allowed_models: %s", normalized)
		}
	})

	t.Run("blacklisted IP is denied", func(t *testing.T) {
		row := makeBlacklistRow(t, bearer, &blacklisted)
		row.IpAllowlist = &allowlisted
		q := &fakeIPBlacklistQueries{rows: []dbauth.LookupAPIKeysByPrefixRow{row}}
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
