package main

import (
	"net"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

const (
	internalPathPrefix    = "/internal/"
	internalAllowCIDRsEnv = "HUAKAI_HERMES_INTERNAL_EXTRA_ALLOW_CIDRS"
)

// parseInternalAllowCIDRs 解析一个逗号分隔的额外 CIDR 列表,这些 CIDR 在「始终放行的
// loopback + 私网(RFC1918)+ link-local 段」之外,额外允许访问 /internal/* 路由。
// 无法解析的条目会被跳过(坏的 CIDR 直接不加入)。输入为空 → 返回 nil。
func parseInternalAllowCIDRs(raw string) []*net.IPNet {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(part); err == nil && ipnet != nil {
			out = append(out, ipnet)
		}
	}
	return out
}

// internalSourceGate 拒绝对 /internal/* 的请求 —— 即 Hermes 控制面(runner
// bootstrap/refresh/keys、只读的 tool-execute 回调,以及内部 OpenAI 出口)——
// 除非「真实」的 socket 对端处于可信网络:loopback、RFC1918 私网、link-local,
// 或运维配置的额外 CIDR。这增加了一道网络来源屏障,使内部控制面无法经共享 listener
// 从公网直达;应用层的 internal_token / runner HMAC 不再是唯一的门(audit B2)。
//
// 它必须装在 middleware.RealIP 之前。RealIP 会用客户端提供的
// X-Forwarded-For/X-Real-IP 覆盖 RemoteAddr 且不做 trusted-proxy 校验,因此若一个门
// 读取 RealIP 之后的 RemoteAddr,攻击者只需发送 `X-Forwarded-For: 127.0.0.1` 即可
// 伪造成 loopback 来源。本门先跑,判定的是真实的 TCP 对端(r.RemoteAddr),它无法在
// 机器外被伪造 —— 与每 IP 限流器采用的排序理由一致。
//
// 非 /internal/ 路径原样放行。被拒请求返回 404(内部路由对不可信来源不可见),
// 并打一条 WARN 日志记录真实对端,便于攻击可见性 / 配置错误诊断。
func internalSourceGate(extra []*net.IPNet, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, internalPathPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			if internalSourceAllowed(r.RemoteAddr, extra) {
				next.ServeHTTP(w, r)
				return
			}
			if logger != nil {
				logger.Warn("rejected /internal/* request from untrusted source",
					zap.String("remote_addr", r.RemoteAddr), zap.String("path", r.URL.Path))
			}
			http.NotFound(w, r)
		})
	}
}

// internalSourceAllowed 判断一个 socket 对端地址(host:port 形式,如
// http.Request.RemoteAddr)是否可以访问内部路由。无法解析的对端 fail-closed(拒绝)。
func internalSourceAllowed(remoteAddr string, extra []*net.IPNet) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	for _, n := range extra {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
