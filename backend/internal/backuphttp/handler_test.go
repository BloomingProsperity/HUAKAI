package backuphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeStore struct {
	data ManifestData
	err  error
}

func (f fakeStore) Manifest(context.Context) (ManifestData, error) {
	return f.data, f.err
}

func do(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/backup/manifest", nil))
	return rec
}

func TestManifestHappyPathFormatsMetadataAndRedactionPolicy(t *testing.T) {
	store := fakeStore{data: ManifestData{
		SchemaVersion: 151,
		SchemaDirty:   false,
		Tables:        []TableInfo{{Name: "api_keys", EstimatedRows: 42}, {Name: "users", EstimatedRows: 7}},
	}}
	rec := do(NewManifestHandler(store))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200;体=%s", rec.Code, rec.Body.String())
	}
	var resp manifestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if resp.Object != "backup_manifest" || resp.SchemaVersion != 151 || resp.TableCount != 2 {
		t.Fatalf("元数据未透传: %+v", resp)
	}
	if len(resp.Tables) != 2 || resp.Tables[0].Name != "api_keys" || resp.Tables[0].EstimatedRows != 42 {
		t.Fatalf("表清单未透传: %+v", resp.Tables)
	}
	// 脱敏策略必须随响应返回(声明边界),且确含真实敏感列(防"空策略"假绿)。
	if len(resp.RedactionPolicy.RedactedColumns) == 0 {
		t.Fatal("脱敏策略缺失(应声明默认脱敏的敏感列)")
	}
	found := false
	for _, c := range resp.RedactionPolicy.RedactedColumns {
		if c == "api_keys.key_hash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("脱敏声明应含 api_keys.key_hash,实得 %v", resp.RedactionPolicy.RedactedColumns)
	}
}

func TestManifestStoreNilReturns503(t *testing.T) {
	rec := do(NewManifestHandler(nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503(store 未配 fail-closed)", rec.Code)
	}
}

func TestManifestStoreErrorReturns503NotHalfBody(t *testing.T) {
	store := fakeStore{err: errors.New("boom")}
	rec := do(NewManifestHandler(store))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503(查询失败 fail-closed,不出半成品)", rec.Code)
	}
	// 错误响应不应泄露原始错误(boom)给客户端。
	if body := rec.Body.String(); contains(body, "boom") {
		t.Fatalf("原始错误不应回传客户端: %s", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
