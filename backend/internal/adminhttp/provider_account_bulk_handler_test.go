package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// stubBulkAuth 返回一个固定的 AdminIdentity。
type stubBulkAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s *stubBulkAuth) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type stubBulkStore struct {
	listRows  []admindb.AdminProviderAccountRow
	listErr   error
	listArg   admindb.ListAdminProviderAccountsParams
	itemCalls []providerAccountBulkItemParams
	outcomes  map[int64]providerAccountBulkItemOutcome
	itemErrs  map[int64]error
}

func (s *stubBulkStore) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	s.listArg = arg
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listRows, nil
}

func (s *stubBulkStore) UpdateProviderAccountByTagWithAudit(_ context.Context, arg providerAccountBulkItemParams) (providerAccountBulkItemOutcome, error) {
	s.itemCalls = append(s.itemCalls, arg)
	if err := s.itemErrs[arg.ID]; err != nil {
		return providerAccountBulkItemOutcome{}, err
	}
	if outcome, ok := s.outcomes[arg.ID]; ok {
		return outcome, nil
	}
	return providerAccountBulkItemOutcome{Status: "succeeded"}, nil
}

func buildBulkTestDeps(store *stubBulkStore, tenantID int64) ProviderAccountBulkDeps {
	return ProviderAccountBulkDeps{
		Auth: &stubBulkAuth{
			ident: admin.AdminIdentity{
				Role:          admin.RoleTenantOperator,
				ScopeTenantID: tenantID,
				TokenID:       999,
			},
		},
		Store: store,
	}
}

func doProviderAccountBulkPOST(t *testing.T, deps ProviderAccountBulkDeps, body any, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := chi.NewRouter()
	MountProviderAccountBulkRoutes(r, deps)

	req := httptest.NewRequest(http.MethodPost, "/bulk-by-tag?tenant_id="+itoa(tenantID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func itoa(i int64) string {
	return strconv.FormatInt(i, 10)
}

func TestProviderAccountBulk_HappyPath(t *testing.T) {
	const tenantID = int64(1)

	store := &stubBulkStore{
		listRows: []admindb.AdminProviderAccountRow{
			{ID: 101, TenantID: tenantID},
			{ID: 202, TenantID: tenantID},
		},
	}
	deps := buildBulkTestDeps(store, tenantID)

	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag":     "flaky",
		"enabled": false,
	}, tenantID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}

	// 校验响应体
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count=%d want 2", resp.Count)
	}
	if len(resp.AffectedIDs) != 2 {
		t.Errorf("len(affected_ids)=%d want 2", len(resp.AffectedIDs))
	}
	if resp.Total != 2 || resp.Succeeded != 2 || resp.Failed != 0 || resp.Skipped != 0 {
		t.Fatalf("summary=%+v want total=2 succeeded=2 failed=0 skipped=0", resp)
	}
	if len(resp.Results) != 2 || resp.Results[0].Status != "succeeded" || resp.Results[1].Status != "succeeded" {
		t.Fatalf("results=%+v want two succeeded results", resp.Results)
	}
	if len(store.itemCalls) != 2 {
		t.Fatalf("item transaction call count=%d want 2", len(store.itemCalls))
	}
	for i, call := range store.itemCalls {
		if call.Enabled == nil || *call.Enabled != false {
			t.Errorf("call[%d]: Enabled=%v want pointer-to-false", i, call.Enabled)
		}
		if call.Tag != "flaky" || call.TenantID != tenantID || call.ActorID != "admin_token:999" {
			t.Errorf("call[%d] identity drift: %+v", i, call)
		}
	}
}

func TestProviderAccountBulk_EmptyTag_Returns400(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{}
	deps := buildBulkTestDeps(store, tenantID)

	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag":     "",
		"enabled": true,
	}, tenantID)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 for empty tag", rec.Code, rec.Body.String())
	}
	if len(store.itemCalls) != 0 {
		t.Errorf("store must not be called on validation failure")
	}
}

func TestProviderAccountBulk_NoFieldToSet_Returns400(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{}
	deps := buildBulkTestDeps(store, tenantID)

	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag": "flaky",
	}, tenantID)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400 for no field to set", rec.Code, rec.Body.String())
	}
	if len(store.itemCalls) != 0 {
		t.Errorf("store must not be called on validation failure")
	}
}

func TestProviderAccountBulk_TagFilterPassedThrough(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{
		listRows: []admindb.AdminProviderAccountRow{{ID: 101, TenantID: tenantID}},
	}
	deps := buildBulkTestDeps(store, tenantID)

	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag":     "flaky",
		"enabled": true,
	}, tenantID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("count=%d want 1", resp.Count)
	}
	if store.listArg.TagFilter != "flaky" || store.listArg.TenantID != tenantID {
		t.Fatalf("list arg=%+v want tenant=%d tag=flaky", store.listArg, tenantID)
	}
	if store.listArg.LimitCount != providerAccountBulkMaxTargets+1 {
		t.Fatalf("list limit=%d want %d", store.listArg.LimitCount, providerAccountBulkMaxTargets+1)
	}
}

func TestProviderAccountBulk_ItemFailureContinuesAndReturnsMultiStatus(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{
		listRows: []admindb.AdminProviderAccountRow{
			{ID: 101, TenantID: tenantID},
			{ID: 202, TenantID: tenantID},
		},
		itemErrs: map[int64]error{101: errors.New("audit rejected")},
	}
	rec := doProviderAccountBulkPOST(t, buildBulkTestDeps(store, tenantID), map[string]any{
		"tag":      "flaky",
		"priority": 8,
	}, tenantID)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status=%d body=%s want 207", rec.Code, rec.Body.String())
	}
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 2 || resp.Succeeded != 1 || resp.Failed != 1 || resp.Count != 1 {
		t.Fatalf("summary=%+v want one failed and one succeeded", resp)
	}
	if len(store.itemCalls) != 2 {
		t.Fatalf("second item must still run; calls=%d", len(store.itemCalls))
	}
	if len(resp.Results) != 2 || resp.Results[0].Status != "failed" || resp.Results[1].Status != "succeeded" {
		t.Fatalf("results=%+v", resp.Results)
	}
	if resp.Results[0].Message == "audit rejected" {
		t.Fatal("response must not leak raw database error")
	}
}

func TestProviderAccountBulk_SkippedResultIsExplicit(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{
		listRows: []admindb.AdminProviderAccountRow{{ID: 101, TenantID: tenantID}},
		outcomes: map[int64]providerAccountBulkItemOutcome{
			101: {Status: "skipped", Code: "already_in_desired_state"},
		},
	}
	rec := doProviderAccountBulkPOST(t, buildBulkTestDeps(store, tenantID), map[string]any{
		"tag":     "flaky",
		"enabled": true,
	}, tenantID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Skipped != 1 || resp.Succeeded != 0 || resp.Count != 0 {
		t.Fatalf("summary=%+v want one skipped", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Code != "already_in_desired_state" {
		t.Fatalf("results=%+v", resp.Results)
	}
}

func TestProviderAccountBulk_TooManyTargetsRejectsBeforeWriting(t *testing.T) {
	const tenantID = int64(1)
	rows := make([]admindb.AdminProviderAccountRow, providerAccountBulkMaxTargets+1)
	for i := range rows {
		rows[i] = admindb.AdminProviderAccountRow{ID: int64(i + 1), TenantID: tenantID}
	}
	store := &stubBulkStore{listRows: rows}
	rec := doProviderAccountBulkPOST(t, buildBulkTestDeps(store, tenantID), map[string]any{
		"tag":     "flaky",
		"enabled": true,
	}, tenantID)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	if len(store.itemCalls) != 0 {
		t.Fatalf("must reject before item transactions; calls=%d", len(store.itemCalls))
	}
}

func TestProviderAccountBulk_UnknownFieldReturns400(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{}
	rec := doProviderAccountBulkPOST(t, buildBulkTestDeps(store, tenantID), map[string]any{
		"tag":     "flaky",
		"enabled": true,
		"typo":    true,
	}, tenantID)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(store.itemCalls) != 0 {
		t.Fatal("unknown field must fail before store writes")
	}
}
