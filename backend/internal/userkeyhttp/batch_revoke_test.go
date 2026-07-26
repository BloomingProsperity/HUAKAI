package userkeyhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

func TestBatchRevokeContinuesAfterTransientFailureAndReportsEveryItem(t *testing.T) {
	secretMarker := "database-secret-marker"
	svc := &stubService{revokeErrByID: map[int64]error{2: errors.New(secretMarker)}}
	router := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/batch-revoke",
		strings.NewReader(`{"ids":[1,2,3],"reason":"rotation"}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var out batchRevokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Outcome != "partial" || len(out.Revoked) != 2 || len(out.Failed) != 1 ||
		out.Failed[0] != 2 || len(out.NotExecuted) != 0 || len(out.Results) != 3 {
		t.Fatalf("response=%+v", out)
	}
	if out.Results[1].Status != "failed" || out.Results[1].ErrorCode != "userkey_backend_error" ||
		!out.Results[1].Retryable || out.Results[2].Status != "revoked" {
		t.Fatalf("results=%+v", out.Results)
	}
	if len(svc.revokeCalls) != 3 {
		t.Fatalf("普通单项失败不应中断后续项: calls=%d", len(svc.revokeCalls))
	}
	if strings.Contains(rec.Body.String(), secretMarker) {
		t.Fatalf("批量结果泄露内部错误: %s", rec.Body.String())
	}
}

func TestBatchRevokeFatalFailureMarksRemainingNotExecuted(t *testing.T) {
	svc := &stubService{revokeErrByID: map[int64]error{
		2: userkey.ErrServiceMisconfig,
	}}
	router := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/batch-revoke",
		strings.NewReader(`{"ids":[1,2,3,4]}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var out batchRevokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if out.Outcome != "partial" || len(out.Failed) != 1 || out.Failed[0] != 2 ||
		len(out.NotExecuted) != 2 || out.NotExecuted[0] != 3 || out.NotExecuted[1] != 4 {
		t.Fatalf("response=%+v", out)
	}
	if len(svc.revokeCalls) != 2 {
		t.Fatalf("致命依赖错误后仍调用了后续项: calls=%d", len(svc.revokeCalls))
	}
	for _, item := range out.Results[2:] {
		if item.Status != "not_executed" || item.ErrorCode != "userkey_service_unavailable" || !item.Retryable {
			t.Fatalf("not_executed=%+v", item)
		}
	}
}

func TestBatchRevokeRejectsInvalidSetBeforeAnyMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{name: "非正数", body: `{"ids":[1,0,2]}`, code: "invalid_ids"},
		{name: "重复", body: `{"ids":[1,2,1]}`, code: "duplicate_ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubService{}
			router := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
			req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/batch-revoke", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(svc.revokeCalls) != 0 {
				t.Fatalf("输入整体校验失败后仍发生撤销: calls=%d", len(svc.revokeCalls))
			}
		})
	}
}

func TestBatchRevokeReportsIdempotentAlreadyRevoked(t *testing.T) {
	svc := &stubService{revokeReturn: userkey.RevokeResult{AlreadyRevoked: true}}
	router := mountWithSession(t, svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/api-keys/batch-revoke",
		strings.NewReader(`{"ids":[9]}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var out batchRevokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Outcome != "success" || len(out.Revoked) != 1 || len(out.Results) != 1 ||
		out.Results[0].Status != "already_revoked" {
		t.Fatalf("response=%+v", out)
	}
}
