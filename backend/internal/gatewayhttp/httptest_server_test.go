package gatewayhttp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newGatewayHTTPTestServer 在禁用本地监听的 sandbox 中跳过网络 smoke。
func newGatewayHTTPTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				msg := fmt.Sprint(recovered)
				if strings.Contains(msg, "httptest: failed to listen") || strings.Contains(msg, "operation not permitted") {
					t.Skipf("local httptest listener unavailable: %v", recovered)
				}
				panic(recovered)
			}
		}()
		server = httptest.NewServer(h)
	}()
	return server
}
