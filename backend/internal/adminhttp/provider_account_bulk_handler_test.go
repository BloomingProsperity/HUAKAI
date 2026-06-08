package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// stubBulkAuth resolves a fixed AdminIdentity.
type stubBulkAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (s *stubBulkAuth) Resolve(_ context.Context, _ *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

// stubBulkStore is a discriminating stub: it only returns accounts when TagFilter matches "flaky".
type stubBulkStore struct {
	// recorded calls to UpdateAdminProviderAccount
	updateCalls []admindb.UpdateAdminProviderAccountParams
	// count of InsertAdminAuditEvent calls
	auditCount int
	// simulate an update error on request
	updateErr error
}

func (s *stubBulkStore) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	// DISCRIMINATING: only return results when TagFilter is exactly "flaky".
	if arg.TagFilter != "flaky" {
		return nil, nil
	}
	return []admindb.AdminProviderAccountRow{
		{ID: 101, TenantID: arg.TenantID},
		{ID: 202, TenantID: arg.TenantID},
	}, nil
}

func (s *stubBulkStore) UpdateAdminProviderAccount(_ context.Context, arg admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	if s.updateErr != nil {
		return admindb.AdminProviderAccountRow{}, s.updateErr
	}
	s.updateCalls = append(s.updateCalls, arg)
	return admindb.AdminProviderAccountRow{ID: arg.ID}, nil
}

func (s *stubBulkStore) InsertAdminAuditEvent(_ context.Context, _ admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.auditCount++
	return admindb.InsertAdminAuditEventRow{}, nil
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
	falseVal := false

	store := &stubBulkStore{}
	deps := buildBulkTestDeps(store, tenantID)

	rec := doProviderAccountBulkPOST(t, deps, map[string]any{
		"tag":     "flaky",
		"enabled": false,
	}, tenantID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}

	// Verify response body
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
	// Verify the IDs are the ones the stub returned
	idSet := map[int64]bool{101: true, 202: true}
	for _, id := range resp.AffectedIDs {
		if !idSet[id] {
			t.Errorf("unexpected id %d in affected_ids", id)
		}
	}

	// Discriminating: exactly 2 UpdateAdminProviderAccount calls
	if len(store.updateCalls) != 2 {
		t.Fatalf("UpdateAdminProviderAccount call count=%d want 2", len(store.updateCalls))
	}
	_ = falseVal
	for i, call := range store.updateCalls {
		if call.Enabled == nil || *call.Enabled != false {
			t.Errorf("call[%d]: Enabled=%v want pointer-to-false", i, call.Enabled)
		}
	}

	// Discriminating: exactly 2 audit events
	if store.auditCount != 2 {
		t.Errorf("InsertAdminAuditEvent call count=%d want 2", store.auditCount)
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
	if store.auditCount != 0 || len(store.updateCalls) != 0 {
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
	if store.auditCount != 0 || len(store.updateCalls) != 0 {
		t.Errorf("store must not be called on validation failure")
	}
}

// TestProviderAccountBulk_TagFilterPassedThrough verifies the handler
// actually passes TagFilter to ListAdminProviderAccounts. If the handler
// ignores TagFilter, the stub returns 0 accounts and count==0 (should be 2).
func TestProviderAccountBulk_TagFilterPassedThrough(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{}
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
	// If TagFilter was not passed (empty), the stub returns 0 results.
	if resp.Count != 2 {
		t.Errorf("count=%d want 2: handler must pass TagFilter='flaky' to ListAdminProviderAccounts", resp.Count)
	}
}
