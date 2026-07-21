package sunoclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

func TestSunoFetchRoutesUseMediaTaskStatus(t *testing.T) {
	// 变异:fetch 读错了 path id 或忽略了 query id;下面记录到的 service 调用就会对不上。
	service := &serviceStub{
		statusResult: taskFixture(603, "suno_generate", json.RawMessage(`{"prompt":"x"}`)),
	}
	mux := mountWithSession(service)

	pathRec := httptest.NewRecorder()
	mux.ServeHTTP(pathRec, httptest.NewRequest(http.MethodGet, "/suno/fetch/603", nil))
	if pathRec.Code != http.StatusOK {
		t.Fatalf("path fetch status=%d body=%s want 200", pathRec.Code, pathRec.Body.String())
	}
	if service.statusTenant != 7 || service.statusUser != 42 || service.statusID != 603 {
		t.Fatalf("path fetch service scope tenant/user/id=%d/%d/%d want 7/42/603", service.statusTenant, service.statusUser, service.statusID)
	}

	queryRec := httptest.NewRecorder()
	mux.ServeHTTP(queryRec, httptest.NewRequest(http.MethodGet, "/suno/fetch?id=604", nil))
	if queryRec.Code != http.StatusOK {
		t.Fatalf("query fetch status=%d body=%s want 200", queryRec.Code, queryRec.Body.String())
	}
	if service.statusID != 604 {
		t.Fatalf("query fetch service id=%d want 604", service.statusID)
	}
}

func TestSunoMultipleKeysRequireExplicitSelection(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceError(rec, mediatask.ErrAPIKeyAmbiguous)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"media_task_api_key_ambiguous"`) {
		t.Fatalf("status=%d body=%s want 409 media_task_api_key_ambiguous", rec.Code, rec.Body.String())
	}
}
