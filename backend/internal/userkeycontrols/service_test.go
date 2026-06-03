package userkeycontrols

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func TestSetKeyQuota_UsesAPIKeyIDAsScopeID(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	_, err := svc.SetKeyQuota(context.Background(), SetKeyQuotaRequest{
		TenantID: 11,
		UserID:   22,
		APIKeyID: 333,
		LimitUSD: decimal.RequireFromString("10.00000000"),
	})
	if err != nil {
		t.Fatalf("SetKeyQuota: %v", err)
	}
	if store.upsertArg.ScopeID != "333" {
		t.Fatalf("scope_id must be api_key_id; got %q", store.upsertArg.ScopeID)
	}
	if store.upsertArg.Metric != quota.MetricCostUSD {
		t.Fatalf("metric must be cost_usd; got %q", store.upsertArg.Metric)
	}
}

func TestSetKeyQuota_LimitUSD_PreservesNumeric20x8Precision(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)
	want := decimal.RequireFromString("999999999999.12345678")

	_, err := svc.SetKeyQuota(context.Background(), SetKeyQuotaRequest{
		TenantID: 11,
		UserID:   22,
		APIKeyID: 333,
		LimitUSD: want,
	})
	if err != nil {
		t.Fatalf("SetKeyQuota: %v", err)
	}
	if !store.upsertArg.LimitUSD.Equal(want) {
		t.Fatalf("limit_usd lost precision: got %s want %s", store.upsertArg.LimitUSD, want)
	}
}

func TestSetKeyQuota_LinksQuotaPolicyIDOnAPIKey(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	res, err := svc.SetKeyQuota(context.Background(), SetKeyQuotaRequest{
		TenantID: 11,
		UserID:   22,
		APIKeyID: 333,
		LimitUSD: decimal.RequireFromString("1.00000000"),
	})
	if err != nil {
		t.Fatalf("SetKeyQuota: %v", err)
	}
	if !store.linkQuotaCalled {
		t.Fatalf("SetKeyQuota must link api_keys.quota_policy_id after upsert")
	}
	if store.linkQuotaArg.PolicyID != res.PolicyID {
		t.Fatalf("linked policy id drift: got %d want %d", store.linkQuotaArg.PolicyID, res.PolicyID)
	}
}

func TestGetKeyQuota_ReturnsNotFoundWhenPolicyMissing(t *testing.T) {
	store := newFakeStore()
	store.getQuotaErr = errNoRows
	svc := newServiceForTest(store, fixedNow)

	_, err := svc.GetKeyQuota(context.Background(), 11, 22, 333)
	if !errors.Is(err, ErrQuotaPolicyNotFound) {
		t.Fatalf("GetKeyQuota err=%v want ErrQuotaPolicyNotFound", err)
	}
}

func TestSetKeyGroup_RejectsWrongTenantGroup(t *testing.T) {
	store := newFakeStore()
	store.validateGroupErr = errNoRows
	svc := newServiceForTest(store, fixedNow)
	groupID := int64(444)

	_, err := svc.SetKeyGroup(context.Background(), SetKeyGroupRequest{
		TenantID: 11,
		UserID:   22,
		APIKeyID: 333,
		GroupID:  &groupID,
	})
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("SetKeyGroup err=%v want ErrGroupNotFound for wrong-tenant group", err)
	}
	if store.setGroupCalled {
		t.Fatalf("wrong-tenant group must be rejected before api_keys update")
	}
}

func TestSetKeyGroup_ClearsWithNilGroupID(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	_, err := svc.SetKeyGroup(context.Background(), SetKeyGroupRequest{
		TenantID: 11,
		UserID:   22,
		APIKeyID: 333,
		GroupID:  nil,
	})
	if err != nil {
		t.Fatalf("SetKeyGroup clear: %v", err)
	}
	if !store.setGroupCalled {
		t.Fatalf("clear must update api_keys.key_group_id")
	}
	if store.setGroupArg.GroupID != nil {
		t.Fatalf("clear must bind NULL group_id; got %v", *store.setGroupArg.GroupID)
	}
}

func TestSetKeyIPAllowlist_NormalizesCIDRsAndBareIPs(t *testing.T) {
	// Mutation check: store raw entries without parsing and this test sees the
	// unmasked CIDR / bare IP; skip the store update and setIPAllowlistCalled is false.
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	res, err := svc.SetKeyIPAllowlist(context.Background(), SetKeyIPAllowlistRequest{
		TenantID:    11,
		UserID:      22,
		APIKeyID:    333,
		IPAllowlist: []string{" 10.1.2.3/8 ", "203.0.113.7"},
	})
	if err != nil {
		t.Fatalf("SetKeyIPAllowlist: %v", err)
	}
	if !store.setIPAllowlistCalled {
		t.Fatalf("SetKeyIPAllowlist must update api_keys.ip_allowlist")
	}
	if store.setIPAllowlistArg.IPAllowlist == nil {
		t.Fatalf("non-empty allowlist must store non-null text")
	}
	if got, want := *store.setIPAllowlistArg.IPAllowlist, "10.0.0.0/8,203.0.113.7/32"; got != want {
		t.Fatalf("stored ip_allowlist=%q want %q", got, want)
	}
	if got, want := strings.Join(res.IPAllowlist, ","), "10.0.0.0/8,203.0.113.7/32"; got != want {
		t.Fatalf("result ip_allowlist=%q want %q", got, want)
	}
}

func TestSetKeyIPAllowlist_EmptyClearsRestriction(t *testing.T) {
	// Mutation check: persist an empty string instead of NULL and the store arg differs.
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	res, err := svc.SetKeyIPAllowlist(context.Background(), SetKeyIPAllowlistRequest{
		TenantID:    11,
		UserID:      22,
		APIKeyID:    333,
		IPAllowlist: []string{" ", ""},
	})
	if err != nil {
		t.Fatalf("SetKeyIPAllowlist clear: %v", err)
	}
	if !store.setIPAllowlistCalled {
		t.Fatalf("clear must update api_keys.ip_allowlist")
	}
	if store.setIPAllowlistArg.IPAllowlist != nil {
		t.Fatalf("clear must bind NULL ip_allowlist, got %q", *store.setIPAllowlistArg.IPAllowlist)
	}
	if len(res.IPAllowlist) != 0 {
		t.Fatalf("cleared result must be empty, got %+v", res.IPAllowlist)
	}
}

func TestSetKeyIPAllowlist_RejectsInvalidCIDRBeforeStore(t *testing.T) {
	// Mutation check: swallow parse errors and the invalid entry reaches the store.
	store := newFakeStore()
	svc := newServiceForTest(store, fixedNow)

	_, err := svc.SetKeyIPAllowlist(context.Background(), SetKeyIPAllowlistRequest{
		TenantID:    11,
		UserID:      22,
		APIKeyID:    333,
		IPAllowlist: []string{"10.0.0.0/8", "not-an-ip"},
	})
	if !errors.Is(err, ErrInvalidIPAllowlist) {
		t.Fatalf("SetKeyIPAllowlist err=%v want ErrInvalidIPAllowlist", err)
	}
	if store.setIPAllowlistCalled {
		t.Fatalf("invalid CIDR must not update api_keys.ip_allowlist")
	}
}

func TestSQLQueries_QuotaPolicyIsAPIKeyScopedAndIdempotent(t *testing.T) {
	sql := readControlsSQL(t)
	queries := namedQueryBodies(sql)
	upsert := queries["UpsertAPIKeyQuotaPolicy"]
	get := queries["GetAPIKeyQuotaPolicy"]
	mustContain(t, upsert, "'api_key'", "quota policy upsert must hard-code api_key scope")
	mustContain(t, upsert, "ON CONFLICT (", "quota upsert must be idempotent")
	mustContain(t, upsert, "WHERE enabled = true AND valid_until IS NULL", "quota upsert must target the live partial unique index")
	mustContain(t, get, "qp.scope_id = ak.id::text", "quota get must bind policy scope to the owned api_key id")
}

func TestSQLQueries_ScopeEveryKeyReadWriteByTenantAndUser(t *testing.T) {
	sql := readControlsSQL(t)
	for name, body := range namedQueryBodies(sql) {
		if !strings.Contains(name, "APIKey") {
			continue
		}
		if !strings.Contains(body, "tenant_id = sqlc.arg(tenant_id)::bigint") &&
			!strings.Contains(body, "ak.tenant_id = sqlc.arg(tenant_id)::bigint") {
			t.Fatalf("%s must scope by tenant_id; body:\n%s", name, body)
		}
		if !strings.Contains(body, "user_id = sqlc.arg(user_id)::bigint") &&
			!strings.Contains(body, "ak.user_id = sqlc.arg(user_id)::bigint") {
			t.Fatalf("%s must scope by user_id; body:\n%s", name, body)
		}
	}
}

func TestSQLQueries_DoNotSelectBearerSecrets(t *testing.T) {
	sql := readControlsSQL(t)
	for _, forbidden := range []string{"key_hash", "key_prefix"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("userkeycontrols SQL must not reference bearer material %q", forbidden)
		}
	}
}

func TestSQLQueries_IPAllowlistControlIsScopedAndReturnsOnlyPolicyText(t *testing.T) {
	sql := readControlsSQL(t)
	queries := namedQueryBodies(sql)
	setBody := queries["SetAPIKeyIPAllowlist"]
	getBody := queries["GetAPIKeyIPAllowlist"]
	mustContain(t, setBody, "SET ip_allowlist = sqlc.narg(ip_allowlist)::text", "set IP allowlist must write only the policy column")
	mustContain(t, setBody, "ak.tenant_id = sqlc.arg(tenant_id)::bigint", "set IP allowlist must scope by tenant")
	mustContain(t, setBody, "ak.user_id = sqlc.arg(user_id)::bigint", "set IP allowlist must scope by user")
	mustContain(t, getBody, "ak.ip_allowlist", "get IP allowlist must return policy text")
	mustContain(t, getBody, "ak.user_id = sqlc.arg(user_id)::bigint", "get IP allowlist must scope by user")
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
}

func readControlsSQL(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "sql", "queries", "userkey_controls.sql"))
	if err != nil {
		t.Fatalf("read userkey_controls.sql: %v", err)
	}
	return string(data)
}

func mustContain(t *testing.T, body, needle, reason string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Fatalf("%s: missing %q", reason, needle)
	}
}

func namedQueryBodies(sql string) map[string]string {
	out := map[string]string{}
	const marker = "-- name: "
	parts := strings.Split(sql, marker)
	for _, part := range parts[1:] {
		lineEnd := strings.IndexByte(part, '\n')
		if lineEnd < 0 {
			continue
		}
		nameLine := strings.TrimSpace(part[:lineEnd])
		name := strings.Fields(nameLine)[0]
		next := strings.Index(part[lineEnd+1:], marker)
		body := part[lineEnd+1:]
		if next >= 0 {
			body = part[lineEnd+1 : lineEnd+1+next]
		}
		out[name] = body
	}
	return out
}

var errNoRows = pgx.ErrNoRows

type fakeStore struct {
	upsertArg            quotaPolicyWrite
	linkQuotaArg         quotaPolicyLink
	setGroupArg          groupAssignment
	setIPAllowlistArg    ipAllowlistAssignment
	getQuotaErr          error
	validateGroupErr     error
	getIPAllowlistErr    error
	linkQuotaCalled      bool
	setGroupCalled       bool
	setIPAllowlistCalled bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{}
}

func (s *fakeStore) WithTx(ctx context.Context, fn func(context.Context, controlsStore) error) error {
	return fn(ctx, s)
}

func (s *fakeStore) UpsertKeyQuotaPolicy(_ context.Context, arg quotaPolicyWrite) (quotaPolicyRow, error) {
	s.upsertArg = arg
	return quotaPolicyRow{
		TenantID:      arg.TenantID,
		ID:            777,
		ScopeKind:     quota.ScopeAPIKey,
		ScopeID:       arg.ScopeID,
		Metric:        arg.Metric,
		WindowKind:    arg.WindowKind,
		WindowSeconds: arg.WindowSeconds,
		LimitUSD:      arg.LimitUSD,
		Mode:          arg.Mode,
		Priority:      200,
		ValidFrom:     arg.ValidFrom,
	}, nil
}

func (s *fakeStore) GetAPIKeyQuotaPolicy(context.Context, int64, int64, int64) (quotaPolicyRow, error) {
	if s.getQuotaErr != nil {
		return quotaPolicyRow{}, s.getQuotaErr
	}
	return quotaPolicyRow{TenantID: 11, ID: 777, ScopeKind: quota.ScopeAPIKey, ScopeID: "333", Metric: quota.MetricCostUSD, WindowKind: quota.WindowCalendarDay, LimitUSD: decimal.NewFromInt(1), Mode: quota.ModeEnforce}, nil
}

func (s *fakeStore) SetAPIKeyQuotaPolicyID(_ context.Context, arg quotaPolicyLink) (int64, error) {
	s.linkQuotaCalled = true
	s.linkQuotaArg = arg
	return 1, nil
}

func (s *fakeStore) ValidateGroupBelongsToTenant(context.Context, int64, int64) (groupRow, error) {
	if s.validateGroupErr != nil {
		return groupRow{}, s.validateGroupErr
	}
	return groupRow{ID: 444, Name: "prod", Enabled: true}, nil
}

func (s *fakeStore) SetAPIKeyGroupID(_ context.Context, arg groupAssignment) (int64, error) {
	s.setGroupCalled = true
	s.setGroupArg = arg
	return 1, nil
}

func (s *fakeStore) GetAPIKeyGroup(context.Context, int64, int64, int64) (keyGroupRow, error) {
	return keyGroupRow{APIKeyID: 333}, nil
}

func (s *fakeStore) SetAPIKeyIPAllowlist(_ context.Context, arg ipAllowlistAssignment) (int64, error) {
	s.setIPAllowlistCalled = true
	s.setIPAllowlistArg = arg
	return 1, nil
}

func (s *fakeStore) GetAPIKeyIPAllowlist(context.Context, int64, int64, int64) (keyIPAllowlistRow, error) {
	if s.getIPAllowlistErr != nil {
		return keyIPAllowlistRow{}, s.getIPAllowlistErr
	}
	value := "10.0.0.0/8"
	return keyIPAllowlistRow{APIKeyID: 333, IPAllowlist: &value}, nil
}
