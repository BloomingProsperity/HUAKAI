package proxyadminhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

type proberStub struct {
	out       ProbeOutcome
	err       error
	called    bool
	gotTenant int64
	gotID     int64
}

func (p *proberStub) Probe(_ context.Context, tenantID, id int64) (ProbeOutcome, error) {
	p.called = true
	p.gotTenant = tenantID
	p.gotID = id
	return p.out, p.err
}

func TestProbeHandlerHappyPathReturnsResultWithoutCredentials(t *testing.T) {
	prober := &proberStub{out: ProbeOutcome{OK: true, LatencyMS: 42}}
	d := Deps{Auth: authStub{ident: platformAdmin()}, Service: &proxyServiceStub{}, Prober: prober}

	rec := invoke(t, d, http.MethodPost, "/admin/v1/proxies/5/test?tenant_id=7", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200;体=%s", rec.Code, rec.Body.String())
	}
	var resp testResponse
	decodeBody(t, rec, &resp)
	if resp.Object != "proxy_probe" || !resp.OK || resp.LatencyMS != 42 {
		t.Fatalf("结果未透传: %+v", resp)
	}
	if !prober.called || prober.gotTenant != 7 || prober.gotID != 5 {
		t.Fatalf("Prober 应按 tenant=7 id=5 调用,实得 called=%v tenant=%d id=%d", prober.called, prober.gotTenant, prober.gotID)
	}
	// 响应体绝不能含代理 URL/凭据/密码等敏感串。
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"password", "secret", "auth_secret", "://", "@"} {
		if strings.Contains(body, leak) {
			t.Fatalf("响应疑似泄露凭据/URL(命中 %q): %s", leak, rec.Body.String())
		}
	}
}

// 跨租户隔离:租户运营者 scope=7 请求他人 tenant_id=9 → 403,且 Prober **绝不触达**。
func TestProbeHandlerTenantOperatorCannotProbeOtherTenant(t *testing.T) {
	prober := &proberStub{out: ProbeOutcome{OK: true}}
	d := Deps{Auth: authStub{ident: tenantOperator(7)}, Service: &proxyServiceStub{}, Prober: prober}

	rec := invoke(t, d, http.MethodPost, "/admin/v1/proxies/5/test?tenant_id=9", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨租户必 403,实得 %d", rec.Code)
	}
	if prober.called {
		t.Fatal("跨租户越权时 Prober 不应被调用")
	}
}

func TestProbeHandlerNilProberReturns503(t *testing.T) {
	d := Deps{Auth: authStub{ident: platformAdmin()}, Service: &proxyServiceStub{}, Prober: nil}
	rec := invoke(t, d, http.MethodPost, "/admin/v1/proxies/5/test?tenant_id=7", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Prober 未配应 503,实得 %d", rec.Code)
	}
}

func TestProbeHandlerNotFoundMapsTo404(t *testing.T) {
	prober := &proberStub{err: proxyadmin.ErrNotFound}
	d := Deps{Auth: authStub{ident: platformAdmin()}, Service: &proxyServiceStub{}, Prober: prober}
	rec := invoke(t, d, http.MethodPost, "/admin/v1/proxies/5/test?tenant_id=7", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ErrNotFound 应 404,实得 %d", rec.Code)
	}
}
