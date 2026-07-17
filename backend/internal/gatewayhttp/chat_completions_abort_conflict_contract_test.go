package gatewayhttp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestAbortConflictHTTPContract_ATCD3003And3005 把最终 40001 与普通 Abort
// 失败放进同一 HTTP 出口，证明状态、稳定错误体与诊断头完全相同。
func TestAbortConflictHTTPContract_ATCD3003And3005(t *testing.T) {
	invoke := func(abortErr error) (int, string, http.Header) {
		deps := clientAdapterDeps(t)
		deps.Selector = claimRaceSelector{}
		deps.Settler = &failingAbortSettler{err: abortErr}
		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		return rec.Code, rec.Body.String(), rec.Header()
	}

	baseStatus, baseBody, baseHeader := invoke(errors.New("baseline abort unavailable"))
	conflictStatus, conflictBody, conflictHeader := invoke(&pgconn.PgError{Code: "40001"})
	if conflictStatus != baseStatus || conflictBody != baseBody {
		t.Fatalf("conflict status/body=%d/%s，baseline=%d/%s", conflictStatus, conflictBody, baseStatus, baseBody)
	}
	if conflictStatus != http.StatusConflict {
		t.Fatalf("status=%d，want 409", conflictStatus)
	}
	for _, header := range []string{"Retry-After", "X-Huakai-Abort-Failed"} {
		if got, want := conflictHeader.Get(header), baseHeader.Get(header); got != want {
			t.Fatalf("%s=%q，baseline=%q", header, got, want)
		}
	}
	if got := conflictHeader.Get("X-Huakai-Abort-Failed"); got != "abort_failed" {
		t.Fatalf("X-Huakai-Abort-Failed=%q，want abort_failed", got)
	}
}
