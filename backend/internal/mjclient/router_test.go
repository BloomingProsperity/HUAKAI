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
	// MUTATION: fetch uses the wrong path id or list ignores the bounded
	// condition limit; captured service calls below must no longer match.
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
