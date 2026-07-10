// Package authaudit 提供认证安全事件的请求来源补齐规则。
package authaudit

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
)

// WithRequestSource 只补齐尚未显式设置的请求来源字段。
func WithRequestSource(r *http.Request, ipResolver *clientip.Resolver, ip, userAgent string) (string, string) {
	if r == nil {
		return ip, userAgent
	}
	if ip == "" {
		ip = ipResolver.ClientIP(r)
	}
	if userAgent == "" {
		userAgent = r.UserAgent()
	}
	return ip, userAgent
}
