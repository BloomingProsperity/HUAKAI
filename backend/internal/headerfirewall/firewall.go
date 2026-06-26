package headerfirewall

import (
	"context"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type PlatformSettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type Policy struct {
	ExtraDeny     []string
	AllowOverride []string
}

type headerRule struct {
	value  string
	prefix bool
}

var builtInDenyRules = []headerRule{
	exactRule("Set-Cookie"),
	exactRule("Set-Cookie2"),
	exactRule("Authorization"),
	exactRule("Proxy-Authenticate"),
	exactRule("Proxy-Authorization"),
	exactRule("WWW-Authenticate"),
	exactRule("X-Real-IP"),
	exactRule("X-Forwarded-For"),
	exactRule("X-Forwarded-Host"),
	exactRule("X-Forwarded-Proto"),
	exactRule("X-Forwarded-Port"),
	exactRule("X-Cloud-Trace-Context"),
	exactRule("Server"),
	prefixRule("CF-"),
	prefixRule("X-Amz-"),
	prefixRule("X-Amzn-"),
}

var hopByHopRequestHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func StripHopByHopRequestHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if name := strings.TrimSpace(token); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopRequestHeaders {
		h.Del(name)
	}
}

func FilterResponseHeaders(h http.Header, extraDeny []string, allowOverride []string) http.Header {
	filtered := make(http.Header, len(h))
	for name, values := range h {
		if denyBuiltIn(name) {
			continue
		}
		if matchesDynamic(name, extraDeny) && !matchesDynamic(name, allowOverride) {
			continue
		}
		for _, value := range values {
			filtered.Add(name, value)
		}
	}
	return filtered
}

func PolicyFromPlatformSettings(ctx context.Context, settings PlatformSettings) Policy {
	if settings == nil {
		return Policy{}
	}
	return Policy{
		ExtraDeny:     settingList(ctx, settings, platformsettings.KeyResponseHeaderDenyExtra),
		AllowOverride: settingList(ctx, settings, platformsettings.KeyResponseHeaderAllowOverride),
	}
}

func ParseList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func settingList(ctx context.Context, settings PlatformSettings, key platformsettings.SettingKey) []string {
	setting, err := settings.Get(ctx, key)
	if err != nil {
		return nil
	}
	return ParseList(setting.Value)
}

func denyBuiltIn(name string) bool {
	normalized := normalize(name)
	for _, rule := range builtInDenyRules {
		if rule.matches(normalized) {
			return true
		}
	}
	return false
}

func matchesDynamic(name string, patterns []string) bool {
	normalized := normalize(name)
	for _, pattern := range patterns {
		rule := dynamicRule(pattern)
		if rule.value != "" && rule.matches(normalized) {
			return true
		}
	}
	return false
}

func exactRule(value string) headerRule {
	return headerRule{value: normalize(value)}
}

func prefixRule(value string) headerRule {
	return headerRule{value: normalize(value), prefix: true}
}

func dynamicRule(value string) headerRule {
	value = strings.TrimSpace(value)
	if value == "" {
		return headerRule{}
	}
	return headerRule{value: normalize(value), prefix: strings.HasSuffix(value, "-")}
}

func (r headerRule) matches(normalizedName string) bool {
	if r.prefix {
		return strings.HasPrefix(normalizedName, r.value)
	}
	return normalizedName == r.value
}

func normalize(value string) string {
	return strings.ToLower(http.CanonicalHeaderKey(strings.TrimSpace(value)))
}

// egressLeakRequestRules 是一批 proxy/forwarding/CDN 头,在 OUTBOUND(出站)请求上
// 绝不能泄露给上游——它们会轻易地向上游 WAF(Cloudflare/Akamai)暴露本网关是一个
// 中继/代理(relay/proxy)。鉴权头(Authorization、X-API-Key、Anthropic-*)是
// 故意不纳入的。
var egressLeakRequestRules = []headerRule{
	exactRule("X-Forwarded-For"),
	exactRule("X-Forwarded-Host"),
	exactRule("X-Forwarded-Proto"),
	exactRule("X-Forwarded-Port"),
	exactRule("X-Real-IP"),
	exactRule("Forwarded"),
	exactRule("Via"),
	exactRule("Proxy-Connection"),
	exactRule("CF-Connecting-IP"),
	exactRule("True-Client-IP"),
	exactRule("X-Cloud-Trace-Context"),
	prefixRule("CF-"),
	prefixRule("X-Amzn-"),
	prefixRule("X-Amz-Cf-"),
}

// NormalizeEgressRequestHeaders 从 OUTBOUND(出站)上游请求中剥离 proxy/forwarding/CDN
// 泄露头,使本网关不会被上游 WAF 轻易地指纹识别为中继(relay)。这是出站反检测的卫生措施;
// 对所有路径都安全(这些头上游从不需要,也从不携带鉴权)。与 CLIProxyAPI / sub2api /
// AIClient-2-API 的出站头规范化保持对齐。
func NormalizeEgressRequestHeaders(h http.Header) {
	if h == nil {
		return
	}
	for name := range h {
		lk := strings.ToLower(name)
		for _, rule := range egressLeakRequestRules {
			rv := strings.ToLower(rule.value)
			if (rule.prefix && strings.HasPrefix(lk, rv)) || (!rule.prefix && lk == rv) {
				h.Del(name)
				break
			}
		}
	}
}
