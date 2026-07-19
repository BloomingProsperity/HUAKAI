package crssource

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// NewRustClient 构造只能经 Rust sidecar 访问白名单来源的客户端。
// 域名在每次真正拨号前重新解析并校验，Rust 只连接本次获准的地址。
func NewRustClient(socketPath string, policy Policy) (*Client, error) {
	policy = normalizePolicy(policy)
	if strings.TrimSpace(socketPath) == "" || len(policy.AllowedHosts) == 0 {
		return nil, ErrNotConfigured
	}
	if policy.AllowInsecureHTTP {
		return nil, fmt.Errorf("%w: Rust 出口仅允许 HTTPS 来源", ErrInvalidInput)
	}
	resolver := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return resolveAllowedHost(ctx, policy, lookupHost, host)
	}
	roundTripper, err := mimicry.NewPinnedSidecarRoundTripper(
		mimicry.NewSidecarClient(socketPath),
		mimicry.SidecarProfileOperatorSourceSafeV1,
		resolver,
	)
	if err != nil {
		return nil, fmt.Errorf("构造 CRS Rust 出口: %w", err)
	}
	return New(&http.Client{Transport: roundTripper}, policy), nil
}
