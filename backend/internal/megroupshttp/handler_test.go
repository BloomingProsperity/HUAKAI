package megroupshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// --- stubs -----------------------------------------------------------------

type authStub struct {
	identity auth.Identity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return s.identity, nil
}

type userGroupStub struct {
	group string
	err   error
	calls []groupCall
}

type groupCall struct {
	tenantID int64
	userID   int64
}

func (s *userGroupStub) UserGroup(_ context.Context, tenantID, userID int64) (string, error) {
	s.calls = append(s.calls, groupCall{tenantID: tenantID, userID: userID})
	if s.err != nil {
		return "", s.err
	}
	return s.group, nil
}

// routesStub returns the allowed pool-group set only for the exact
// (tenant, userGroup) it was seeded with; anything else yields an empty set.
type routesStub struct {
	tenantID  int64
	userGroup string
	allowed   []int64
	err       error
	calls     []routesCall
}

type routesCall struct {
	tenantID  int64
	userGroup string
	model     string
}

func (s *routesStub) GroupRoutes(_ context.Context, tenantID int64, userGroup, model string) (subscriptionenforce.GroupRoutes, error) {
	s.calls = append(s.calls, routesCall{tenantID: tenantID, userGroup: userGroup, model: model})
	if s.err != nil {
		return subscriptionenforce.GroupRoutes{}, s.err
	}
	out := subscriptionenforce.GroupRoutes{Allowed: make(map[int64]struct{})}
	if tenantID == s.tenantID && userGroup == s.userGroup {
		out.Configured = true
		for _, id := range s.allowed {
			out.Allowed[id] = struct{}{}
		}
	}
	return out, nil
}

type ratioStub struct {
	rows map[int64][]pricingcatalog.GroupPricingRatio // keyed by tenant
	err  error
}

func (s *ratioStub) ListRatios(_ context.Context, tenantID int64) ([]pricingcatalog.GroupPricingRatio, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]pricingcatalog.GroupPricingRatio(nil), s.rows[tenantID]...), nil
}

type poolNameStub struct {
	names map[int64]map[int64]string // tenant -> id -> name
	err   error
}

func (s *poolNameStub) PoolNames(_ context.Context, tenantID int64) (map[int64]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.names[tenantID], nil
}

func ratio(tenantID, poolGroupID int64, text string, public bool) pricingcatalog.GroupPricingRatio {
	return pricingcatalog.GroupPricingRatio{
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
		Ratio:       decimal.RequireFromString(text),
		RatioText:   text,
		PublicRatio: public,
	}
}

func invoke(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/groups", nil))
	return rec
}

func decodeItems(t *testing.T, rec *httptest.ResponseRecorder) (string, []map[string]any) {
	t.Helper()
	var body struct {
		Object    string           `json:"object"`
		UserGroup string           `json:"user_group"`
		Items     []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, rec.Body.String())
	}
	return body.UserGroup, body.Items
}

func itemByID(items []map[string]any, id int64) map[string]any {
	for _, it := range items {
		if v, ok := it["pool_group_id"].(float64); ok && int64(v) == id {
			return it
		}
	}
	return nil
}

// --- tests -----------------------------------------------------------------

// TestPublicRatioGate guards the money/competitive-intel leak: a group whose
// ratio row is public_ratio=false must NEVER serialize its multiplier.
//
// Fixtures are discriminating — group B's hidden ratio "9.90000000" differs both
// from an omitted field and from the default 1.0, so a leak is unmistakable.
//
// MUTATION: drop the `row.PublicRatio` guard in handler.go (always emit ratio).
// Then group B serializes "9.90000000" and the leak assertion goes RED.
func TestPublicRatioGate(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7, 12}},
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {
				ratio(1, 12, "1.50000000", true), // A: public
				ratio(1, 7, "9.90000000", false), // B: hidden internal multiplier
			},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {12: "premium-pool", 7: "standard-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)

	a := itemByID(items, 12)
	if a == nil {
		t.Fatalf("public group 12 missing: %v", items)
	}
	if a["ratio"] != "1.50000000" {
		t.Fatalf("public group ratio=%v want 1.50000000", a["ratio"])
	}
	if a["has_public_ratio"] != true {
		t.Fatalf("public group has_public_ratio=%v want true", a["has_public_ratio"])
	}

	b := itemByID(items, 7)
	if b == nil {
		t.Fatalf("group 7 should still be listed: %v", items)
	}
	if _, present := b["ratio"]; present {
		t.Fatalf("non-public group 7 leaked ratio=%v (must be omitted)", b["ratio"])
	}
	if b["has_public_ratio"] != false {
		t.Fatalf("non-public group has_public_ratio=%v want false", b["has_public_ratio"])
	}
	// Defense in depth: the hidden multiplier must not appear anywhere in the wire body.
	if got := rec.Body.String(); contains(got, "9.90000000") {
		t.Fatalf("hidden internal multiplier leaked into response body: %s", got)
	}
}

// TestTenantUserScoping guards CMB-5 cross-tenant read. The session is
// (tenant=1,user=42); the routes stub only returns groups for that exact pair.
// Tenant 2 has groups {99} but the handler must derive tenant from the session,
// so tenant 2's group can never appear.
//
// MUTATION: have the handler pass a request-derived or zero tenant id into
// GroupRoutes/UserGroup. The (tenant,userGroup) match fails (or matches tenant 2),
// and either the items go empty-where-expected or tenant 2's group 99 surfaces —
// the recorded store-call tenant assertion + the "no group 99" assertion go RED.
func TestTenantUserScoping(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	ug := &userGroupStub{group: "default"}
	routes := &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: ug,
		RoutesRepo: routes,
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {ratio(1, 7, "1.00000000", true)},
			2: {ratio(2, 99, "5.00000000", true)},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "tenant1-pool"},
			2: {99: "tenant2-secret-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)

	if itemByID(items, 99) != nil {
		t.Fatalf("cross-tenant group 99 leaked into response: %v", items)
	}
	if itemByID(items, 7) == nil {
		t.Fatalf("own-tenant group 7 missing: %v", items)
	}
	// The downstream stores must have been queried with the SESSION tenant/user only.
	if len(ug.calls) != 1 || ug.calls[0].tenantID != 1 || ug.calls[0].userID != 42 {
		t.Fatalf("user_group lookup scope=%v want one call tenant:1 user:42", ug.calls)
	}
	if len(routes.calls) != 1 || routes.calls[0].tenantID != 1 {
		t.Fatalf("routes lookup tenant=%v want session tenant 1", routes.calls)
	}
	if got := rec.Body.String(); contains(got, "tenant2-secret-pool") {
		t.Fatalf("cross-tenant pool name leaked: %s", got)
	}
}

// TestTierFiltering guards functional correctness: items reflect only the
// tier's allowed pool groups, not every priced group. The tenant has a ratio
// row for group 99 (a different tier's group), but the caller's tier allows
// only {7,12}.
//
// MUTATION: skip GroupRoutes and list every ratio/pool instead. Then group 99
// appears and the "99 must be absent" assertion goes RED. 99 is a foreign-tier
// group (not an empty-set artifact), so the fixture is discriminating.
func TestTierFiltering(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7, 12}},
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {
				ratio(1, 7, "1.10000000", true),
				ratio(1, 12, "1.20000000", true),
				ratio(1, 99, "8.80000000", true), // priced but belongs to another tier
			},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "p7", 12: "p12", 99: "p99-other-tier"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)
	if len(items) != 2 {
		t.Fatalf("items len=%d want 2 (groups 7,12 only); body=%s", len(items), rec.Body.String())
	}
	if itemByID(items, 99) != nil {
		t.Fatalf("foreign-tier group 99 leaked: %v", items)
	}
	// Ordering: pool_group_id ASC.
	if got := int64(items[0]["pool_group_id"].(float64)); got != 7 {
		t.Fatalf("items[0]=%d want 7 (ascending order)", got)
	}
}

// TestUnauthenticated guards the auth gate: no/invalid session yields 401 and
// never touches downstream stores.
//
// MUTATION: drop the `ident.UserID <= 0` / error check. Then the handler would
// proceed with a zero identity and return 200, flipping this to RED. The stub
// call-count assertion further proves no store was reached.
func TestUnauthenticated(t *testing.T) {
	ug := &userGroupStub{group: "default"}
	d := Deps{
		Auth:       authStub{err: auth.ErrUnauthorized},
		UserGroups: ug,
		RoutesRepo: &routesStub{},
		Ratios:     &ratioStub{},
		Pools:      &poolNameStub{},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if len(ug.calls) != 0 {
		t.Fatalf("unauthenticated request reached user-group store: %v", ug.calls)
	}
}

// TestAllowedGroupWithoutRatioRow guards the "unconfigured = no misleading
// default" rule: an allowed group with no ratio row must report
// has_public_ratio=false and omit ratio entirely — not crash and not invent 1.0.
//
// MUTATION: have the handler default an unpriced group to ratio "1.00000000"
// /has_public_ratio=true. Then this group would carry a (misleading) ratio and
// the "ratio absent + has_public_ratio false" assertion goes RED. A nil-deref
// on the missing map entry would 500 and also fail.
func TestAllowedGroupWithoutRatioRow(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}},
		Ratios:     &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{1: {}}}, // no rows
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "unpriced-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)
	it := itemByID(items, 7)
	if it == nil {
		t.Fatalf("allowed group 7 should be listed even with no ratio: %v", items)
	}
	if _, present := it["ratio"]; present {
		t.Fatalf("unpriced group must omit ratio, got %v", it["ratio"])
	}
	if it["has_public_ratio"] != false {
		t.Fatalf("unpriced group has_public_ratio=%v want false", it["has_public_ratio"])
	}
	if it["name"] != "unpriced-pool" {
		t.Fatalf("name=%v want unpriced-pool", it["name"])
	}
}

// TestDependencyUnset guards the 503 wiring guard.
func TestDependencyUnset(t *testing.T) {
	rec := invoke(NewHandler(Deps{}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
}

// TestBackendErrorIsServiceUnavailable guards that a transient store failure
// surfaces as 503 rather than a panic/200.
func TestBackendErrorIsServiceUnavailable(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{err: errors.New("db down")},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}},
		Ratios:     &ratioStub{},
		Pools:      &poolNameStub{},
	}
	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
