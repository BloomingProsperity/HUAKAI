package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

// gatewayProxyProber 组合 proxyadmin.DialTarget(解密拨号 URL)+ proxyhealth.ProbeThrough
// (SSRF 守卫 + 经代理隧道 + 到 canary 的 TLS 握手),实现 proxyadminhttp 的探测依赖。
// 探测目标固定为服务端常量 proxyhealth.DefaultProbeCanary,绝不来自请求(杜绝双跳 SSRF)。
// ⚠ DialTarget 返回的拨号 URL 含明文凭据,全程留在本结构内 → 绝不日志、绝不回传客户端。
type gatewayProxyProber struct {
	svc    *proxyadmin.Service
	policy ssrfpolicy.Policy
	canary string
}

// proxyAdminRouteDeps 集中装配代理 CRUD、主动质检和租户默认出口，避免新增能力
// 只挂路由却漏注入数据库 store。nil 输入保持全空，由 handler 统一 fail-closed。
func proxyAdminRouteDeps(d *deps) proxyadminhttp.Deps {
	if d == nil {
		return proxyadminhttp.Deps{}
	}
	return proxyadminhttp.Deps{
		Auth:           d.adminAuth,
		Service:        proxyadmin.New(d.adminQueries, d.credentialKeys),
		Prober:         buildProxyProber(d),
		TenantDefaults: proxyadmin.NewPostgresTenantDefaultStore(d.pgPool),
	}
}

func (p *gatewayProxyProber) Probe(ctx context.Context, tenantID, id int64) (proxyadminhttp.ProbeOutcome, error) {
	dialURL, err := p.svc.DialTarget(ctx, tenantID, id)
	if err != nil {
		// 代理 host 不安全(内网/metadata)→ 作为"探测失败"结果返回(200 + 分类),不当 5xx。
		if errors.Is(err, proxyadmin.ErrUnsafeHost) {
			return proxyadminhttp.ProbeOutcome{OK: false, ErrorClass: proxyhealth.ErrClassUnsafeProxyHost}, nil
		}
		// ErrNotFound / ErrInvalidInput / ErrBackend → 上抛由 handler 映射状态码。
		return proxyadminhttp.ProbeOutcome{}, err
	}
	res := proxyhealth.ProbeThrough(ctx, p.policy, dialURL, p.canary)
	return proxyadminhttp.ProbeOutcome{OK: res.OK, LatencyMS: res.LatencyMS, ErrorClass: res.ErrorClass}, nil
}

// buildProxyProber 构造主动质检依赖。SSRF 策略走 LoadFromEnv(失败则用零值 Policy{},仍允许常量
// canary);svc 用与 admin CRUD 同一份 proxyadmin.Service(共享解密 keys)。pgPool/keys 缺失时返回 nil
// → handler 见 nil Prober 返回 503(fail-closed,不裸奔)。
func buildProxyProber(d *deps) proxyadminhttp.Prober {
	if d == nil || d.adminQueries == nil || d.credentialKeys == nil {
		return nil
	}
	policy, err := ssrfpolicy.LoadFromEnv()
	if err != nil {
		slog.Warn("proxy probe: ssrfpolicy.LoadFromEnv 失败,退回零值策略", "err", err)
		policy = ssrfpolicy.Policy{}
	}
	return &gatewayProxyProber{
		svc:    proxyadmin.New(d.adminQueries, d.credentialKeys),
		policy: policy,
		canary: proxyhealth.DefaultProbeCanary,
	}
}
