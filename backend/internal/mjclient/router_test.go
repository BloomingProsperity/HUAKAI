package mjclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

func TestMJFetchImageSeedAndListUseMediaTaskQueries(t *testing.T) {
	// 变异: fetch 用了错误的 path id，或 list 忽略了受限的 condition limit；
	// 下面捕获的 service 调用就不应再匹配。
	service := &serviceStub{
		statusResult: taskFixture(303, "mj_imagine", json.RawMessage(`{"prompt":"x"}`)),
		listResult:   []mediatask.Task{taskFixture(303, "mj_imagine", json.RawMessage(`{"prompt":"x"}`))},
	}
	mux := mountWithSession(service)

	fetchRec := httptest.NewRecorder()
	mux.ServeHTTP(fetchRec, httptest.NewRequest(http.MethodGet, "/mj/task/303/fetch", nil))
	if fetchRec.Code != http.StatusOK {
		t.Fatalf("fetch status=%d body=%s want 200", fetchRec.Code, fetchRec.Body.String())
	}
	if service.statusTenant != 7 || service.statusUser != 42 || service.statusID != 303 {
		t.Fatalf("fetch service scope tenant/user/id=%d/%d/%d", service.statusTenant, service.statusUser, service.statusID)
	}

	seedRec := httptest.NewRecorder()
	mux.ServeHTTP(seedRec, httptest.NewRequest(http.MethodGet, "/mj/task/303/image-seed", nil))
	if seedRec.Code != http.StatusOK {
		t.Fatalf("image-seed status=%d body=%s want 200", seedRec.Code, seedRec.Body.String())
	}
	if service.statusID != 303 {
		t.Fatalf("image-seed service id=%d want 303", service.statusID)
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodPost, "/mj/task/list-by-condition", strings.NewReader(`{"limit":5}`))
	listReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s want 200", listRec.Code, listRec.Body.String())
	}
	if service.listTenant != 7 || service.listUser != 42 || service.listLimit != 5 {
		t.Fatalf("list service scope/limit=%d/%d/%d want 7/42/5", service.listTenant, service.listUser, service.listLimit)
	}
}

func TestMJMultipleKeysRequireExplicitSelection(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceError(rec, mediatask.ErrAPIKeyAmbiguous)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"media_task_api_key_ambiguous"`) {
		t.Fatalf("status=%d body=%s want 409 media_task_api_key_ambiguous", rec.Code, rec.Body.String())
	}
}
