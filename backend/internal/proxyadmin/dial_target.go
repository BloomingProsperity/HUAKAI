package proxyadmin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5"
)

// DialTarget 返回某租户代理的**拨号 URL(含解密后凭据)**,仅供内部主动质检 probe 经该代理建隧道使用。
//
// ⚠ 返回的 URL 含明文凭据 → 调用方**绝不能记录或回传给客户端**,只能喂给 dialer。
//
// 安全:
//   - 按 (tenant_id, id) 查,跨租户/不存在 → ErrNotFound(查询本身按 tenant_id 收敛);
//   - SSRF 守卫:复跑 proxyHostSafe 挡 loopback/内网/link-local/metadata 代理 host(写时已校,此处 defense in depth,
//     防历史数据或绕过写校验留下的不安全 host 被 probe 主动拨号);
//   - url.UserPassword 自动 percent-encode 凭据,escape-safe(同 provider 解析路径范式)。
func (s *Service) DialTarget(ctx context.Context, tenantID, id int64) (*url.URL, error) {
	if tenantID <= 0 || id <= 0 {
		return nil, ErrInvalidInput
	}
	row, err := s.q.GetProxy(ctx, admindb.GetProxyParams{TenantID: tenantID, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, mapErr(err)
	}
	if row.Protocol == "" || row.Host == "" || row.Port <= 0 {
		return nil, ErrInvalidInput
	}
	if !proxyHostSafe(row.Host) {
		return nil, ErrUnsafeHost
	}
	u := &url.URL{
		Scheme: row.Protocol,
		Host:   net.JoinHostPort(row.Host, strconv.Itoa(int(row.Port))),
	}
	if row.AuthUsername != nil && *row.AuthUsername != "" {
		secret := ""
		if row.AuthSecret != nil && *row.AuthSecret != "" {
			plain, derr := proxysecret.Decode(ctx, s.keys, tenantID, *row.AuthSecret)
			if derr != nil {
				return nil, fmt.Errorf("%w: decrypt proxy auth_secret: %v", ErrBackend, derr)
			}
			secret = plain
		}
		u.User = url.UserPassword(*row.AuthUsername, secret)
	}
	return u, nil
}
