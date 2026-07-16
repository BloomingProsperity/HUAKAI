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

// stubBulkStore 是一个判别式 stub:只有当 TagFilter 等于 "flaky" 时才返回账号。
type stubBulkStore struct {
	// 记录原子更新与审计调用
	updateCalls []admindb.UpdateAdminProviderAccountParams
	auditCalls  []admindb.InsertAdminAuditEventParams
	auditCount  int
	// 按账号模拟原子操作错误
	updateErrors map[int64]error
}

type pagedBulkStore struct {
	rows      []admindb.AdminProviderAccountRow
	listCalls int
}

func (s *pagedBulkStore) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	s.listCalls++
	start := 0
	for start < len(s.rows) && s.rows[start].ID <= arg.AfterID {
		start++
	}
	end := start + int(arg.LimitCount)
	if end > len(s.rows) {
		end = len(s.rows)
	}
	return s.rows[start:end], nil
}

func (s *pagedBulkStore) UpdateAdminProviderAccountWithAudit(_ context.Context, _ admindb.UpdateAdminProviderAccountWithAuditParams) (admindb.AdminProviderAccountRow, error) {
	return admindb.AdminProviderAccountRow{}, nil
}

func (s *stubBulkStore) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	// 判别式:只有当 TagFilter 正好是 "flaky" 时才返回结果。
	if arg.TagFilter != "flaky" {
		return nil, nil
	}
	return []admindb.AdminProviderAccountRow{
		{ID: 101, TenantID: arg.TenantID},
		{ID: 202, TenantID: arg.TenantID},
	}, nil
}

func (s *stubBulkStore) UpdateAdminProviderAccountWithAudit(_ context.Context, arg admindb.UpdateAdminProviderAccountWithAuditParams) (admindb.AdminProviderAccountRow, error) {
	if err := s.updateErrors[arg.Update.ID]; err != nil {
		return admindb.AdminProviderAccountRow{}, err
	}
	s.updateCalls = append(s.updateCalls, arg.Update)
	s.auditCalls = append(s.auditCalls, arg.Audit)
	s.auditCount++
	return admindb.AdminProviderAccountRow{ID: arg.Update.ID}, nil
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

	// 校验响应体
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count=%d want 2", resp.Count)
	}
	if !resp.Complete || resp.FailedCount != 0 || resp.MatchedCount != 2 {
		t.Fatalf("resp=%+v", resp)
	}
	if len(resp.AffectedIDs) != 2 {
		t.Errorf("len(affected_ids)=%d want 2", len(resp.AffectedIDs))
	}
	// 校验这些 ID 正是 stub 返回的那些
	idSet := map[int64]bool{101: true, 202: true}
	for _, id := range resp.AffectedIDs {
		if !idSet[id] {
			t.Errorf("unexpected id %d in affected_ids", id)
		}
	}

	// 判别式:恰好 2 次 UpdateAdminProviderAccount 调用
	if len(store.updateCalls) != 2 {
		t.Fatalf("UpdateAdminProviderAccount call count=%d want 2", len(store.updateCalls))
	}
	_ = falseVal
	for i, call := range store.updateCalls {
		if call.Enabled == nil || *call.Enabled != false {
			t.Errorf("call[%d]: Enabled=%v want pointer-to-false", i, call.Enabled)
		}
	}

	// 判别式:恰好 2 条审计事件
	if store.auditCount != 2 {
		t.Errorf("atomic audit count=%d want 2", store.auditCount)
	}
	for _, audit := range store.auditCalls {
		if audit.Action != "update_provider_account" {
			t.Fatalf("audit action=%q，必须使用数据库现行白名单 action", audit.Action)
		}
		var payload map[string]any
		if err := json.Unmarshal(audit.Payload, &payload); err != nil || payload["source"] != "bulk_by_tag" {
			t.Fatalf("audit payload=%s err=%v", string(audit.Payload), err)
		}
	}
}

func TestProviderAccountBulk_ItemFailureContinuesAndReturnsDetails(t *testing.T) {
	const tenantID = int64(1)
	store := &stubBulkStore{updateErrors: map[int64]error{101: context.DeadlineExceeded}}
	rec := doProviderAccountBulkPOST(t, buildBulkTestDeps(store, tenantID), map[string]any{
		"tag": "flaky", "enabled": false,
	}, tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp providerAccountBulkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Complete || resp.MatchedCount != 2 || resp.Count != 1 || resp.FailedCount != 1 {
		t.Fatalf("resp=%+v", resp)
	}
	if len(resp.AffectedIDs) != 1 || resp.AffectedIDs[0] != 202 {
		t.Fatalf("affected_ids=%v，第一项失败后必须继续处理第二项", resp.AffectedIDs)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].ID != 101 || resp.Failed[0].Code != "update_and_audit_failed" {
		t.Fatalf("failed=%+v", resp.Failed)
	}
	if resp.Failed[0].Message != "account update and audit failed" {
		t.Fatalf("message=%q，不能向客户端泄露底层错误", resp.Failed[0].Message)
	}
	if len(store.updateCalls) != 1 || store.updateCalls[0].ID != 202 || store.auditCount != 1 {
		t.Fatalf("atomic calls=%+v audit=%d", store.updateCalls, store.auditCount)
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

// TestProviderAccountBulk_TagFilterPassedThrough 校验 handler
// 确实把 TagFilter 传给了 ListAdminProviderAccounts。如果 handler
// 忽略 TagFilter,stub 会返回 0 个账号,count==0(应为 2)。
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
	// 如果 TagFilter 没有被传递(为空),stub 会返回 0 条结果。
	if resp.Count != 2 {
		t.Errorf("count=%d want 2: handler must pass TagFilter='flaky' to ListAdminProviderAccounts", resp.Count)
	}
}

func TestListAllProviderAccountsByTag_PaginatesUntilShortPage(t *testing.T) {
	rows := make([]admindb.AdminProviderAccountRow, 201)
	for i := range rows {
		rows[i].ID = int64(i + 1)
		rows[i].TenantID = 7
	}
	store := &pagedBulkStore{rows: rows}

	got, err := listAllProviderAccountsByTag(context.Background(), store, 7, "flaky")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(got) != len(rows) || got[0].ID != 1 || got[len(got)-1].ID != 201 {
		t.Fatalf("rows=%d first=%d last=%d", len(got), got[0].ID, got[len(got)-1].ID)
	}
	if store.listCalls != 2 {
		t.Fatalf("list calls=%d want 2", store.listCalls)
	}
}

func TestListAllProviderAccountsByTag_RejectsOversizedScope(t *testing.T) {
	rows := make([]admindb.AdminProviderAccountRow, providerAccountBulkMaxMatched+1)
	for i := range rows {
		rows[i].ID = int64(i + 1)
		rows[i].TenantID = 7
	}
	store := &pagedBulkStore{rows: rows}

	got, err := listAllProviderAccountsByTag(context.Background(), store, 7, "flaky")
	if !errors.Is(err, errProviderAccountBulkTooLarge) {
		t.Fatalf("err=%v want errProviderAccountBulkTooLarge", err)
	}
	if got != nil {
		t.Fatalf("oversized scope must not return a partial result")
	}
}
