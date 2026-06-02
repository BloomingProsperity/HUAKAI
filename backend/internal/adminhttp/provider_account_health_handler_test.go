package adminhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderAccountHealthUnauthorized(t *testing.T) {
	store := newProviderAccountHealthStoreStub()

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{err: admin.ErrAdminUnauthorized},
		Store: store,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.getArgs) != 0 {
		t.Fatalf("unauthorized request touched store: %+v", store.getArgs)
	}
}

func TestProviderAccountHealthTenantScopeIgnoresQueryTenantID(t *testing.T) {
	// 判别防串租户:query tenant_id=8 必须被忽略,查询只能使用 admin identity 的 tenant 7。
	// Mutation:从 query/body 收 tenant_id 或漏 tenant predicate 时会命中 tenant 8 row 并返回 200。
	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(8, 200))

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/200/health?tenant_id=8")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.getArgs) != 1 || store.getArgs[0].TenantID != 7 || store.getArgs[0].ID != 200 {
		t.Fatalf("GetAdminProviderAccountHealth args=%+v, want tenant scoped lookup tenant=7 id=200", store.getArgs)
	}
	if strings.Contains(rec.Body.String(), "tenant-8") || strings.Contains(rec.Body.String(), "8") {
		t.Fatalf("cross-tenant response leaked target tenant detail: %s", rec.Body.String())
	}
}

func TestProviderAccountHealthResponseContainsOnlySafeSnapshotFields(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 99)
	row.HealthStateUntil = pgTimestamp(time.Date(2026, 6, 2, 12, 10, 0, 0, time.UTC))
	row.LastRefreshAt = pgTimestamp(time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	outcome := "auth_expired"
	failureClass := "invalid_grant"
	row.LastRefreshOutcome = &outcome
	row.FailureClass = &failureClass
	row.FailureCount = 4
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	assertProviderAccountHealthKeys(t, body, []string{
		"enabled",
		"failure_class",
		"failure_count",
		"health_state",
		"health_state_until",
		"id",
		"last_refresh_at",
		"last_refresh_outcome",
		"requires_action",
		"updated_at",
	})
	forbiddenFragments := []string{"credential", "credentials", "encrypted", "payload", "secret", "token", "nonce", "key_id"}
	lowerBody := strings.ToLower(rec.Body.String())
	for _, fragment := range forbiddenFragments {
		if strings.Contains(lowerBody, fragment) {
			t.Fatalf("response leaked forbidden fragment %q: %s", fragment, rec.Body.String())
		}
	}
}

func TestProviderAccountHealthJoinsLatestRefreshMetadata(t *testing.T) {
	// 判别 refresh join:health 来自 provider_accounts,refresh outcome/failure 来自最新凭据。
	// Mutation:漏掉 account_credentials join 或选旧 credential_version,这些断言会红。
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 101)
	row.HealthState = "throttled"
	row.HealthStateUntil = pgTimestamp(time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC))
	row.Enabled = false
	row.LastRefreshAt = pgTimestamp(time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC))
	outcome := "auth_expired"
	failureClass := "invalid_grant"
	row.LastRefreshOutcome = &outcome
	row.FailureClass = &failureClass
	row.FailureCount = 4
	row.UpdatedAt = pgTimestamp(time.Date(2026, 6, 2, 12, 2, 0, 0, time.UTC))
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/101/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountHealthResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if body.ID != 101 || body.HealthState != "throttled" || body.HealthStateUntil == nil || *body.HealthStateUntil != "2026-06-02T12:30:00Z" {
		t.Fatalf("health fields=%+v, want throttled snapshot with deadline", body)
	}
	if body.Enabled {
		t.Fatalf("enabled=%v want false from provider account metadata", body.Enabled)
	}
	if body.LastRefreshAt == nil || *body.LastRefreshAt != "2026-06-02T12:01:00Z" {
		t.Fatalf("last_refresh_at=%v want latest credential refresh timestamp", body.LastRefreshAt)
	}
	if body.LastRefreshOutcome == nil || *body.LastRefreshOutcome != "auth_expired" {
		t.Fatalf("last_refresh_outcome=%v want auth_expired from latest credential", body.LastRefreshOutcome)
	}
	if body.FailureClass == nil || *body.FailureClass != "invalid_grant" || body.FailureCount != 4 {
		t.Fatalf("failure fields class=%v count=%d want invalid_grant/4", body.FailureClass, body.FailureCount)
	}
	if !body.RequiresAction {
		t.Fatalf("requires_action=false want true when failure_count > 3")
	}
}

func invokeProviderAccountHealth(t *testing.T, deps ProviderAccountHealthDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountHealthRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertProviderAccountHealthKeys(t *testing.T, body map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(body))
	for key := range body {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("response keys=%v want exactly %v", got, want)
	}
}

type providerAccountHealthAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s providerAccountHealthAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type providerAccountHealthStoreStub struct {
	rows    map[string]admindb.GetAdminProviderAccountHealthRow
	getArgs []admindb.GetAdminProviderAccountHealthParams
	err     error
}

func newProviderAccountHealthStoreStub() *providerAccountHealthStoreStub {
	return &providerAccountHealthStoreStub{rows: map[string]admindb.GetAdminProviderAccountHealthRow{}}
}

func (s *providerAccountHealthStoreStub) put(row admindb.GetAdminProviderAccountHealthRow) {
	s.rows[providerAccountHealthKey(row.TenantID, row.ID)] = row
}

func (s *providerAccountHealthStoreStub) GetAdminProviderAccountHealth(_ context.Context, arg admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
	s.getArgs = append(s.getArgs, arg)
	if s.err != nil {
		return admindb.GetAdminProviderAccountHealthRow{}, s.err
	}
	row, ok := s.rows[providerAccountHealthKey(arg.TenantID, arg.ID)]
	if !ok {
		return admindb.GetAdminProviderAccountHealthRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func providerAccountHealthKey(tenantID, accountID int64) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strconv.FormatInt(accountID, 10)
}

func providerAccountHealthRow(tenantID, id int64) admindb.GetAdminProviderAccountHealthRow {
	return admindb.GetAdminProviderAccountHealthRow{
		ID:          id,
		TenantID:    tenantID,
		HealthState: "healthy",
		Enabled:     true,
		UpdatedAt:   pgTimestamp(time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)),
	}
}
