// 包 output — 输出内容净化逻辑。
// 确保可提交的输出文件中不含操作员 IP、MAC 地址、主机名等隐私信息。
package output

import (
	"net"
	"regexp"
	"strings"
)

// 用于检测 IPv4 地址的正则（宽松匹配，用于告警用途）
var ipv4Regex = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// SanitizeString 对字符串执行以下净化操作：
//   - 将所有检测到的 IPv4 地址替换为 <ip-redacted>
//   - 将所有检测到的 MAC 地址替换为 <mac-redacted>
//
// 注意：此函数仅作为辅助防线；主要防护在于工具设计上不收集此类数据。
func SanitizeString(s string) string {
	// 替换 IPv4 地址
	s = ipv4Regex.ReplaceAllStringFunc(s, func(m string) string {
		// 验证是否是合法 IP（排除如版本号 "1.2.3.4" 的误报需人工判断）
		ip := net.ParseIP(m)
		if ip != nil && ip.To4() != nil {
			return "<ip-redacted>"
		}
		return m
	})
	// 替换 MAC 地址（常见格式：xx:xx:xx:xx:xx:xx 或 xx-xx-xx-xx-xx-xx）
	s = sanitizeMAC(s)
	return s
}

// macColonRegex 匹配以冒号分隔的 MAC 地址（如 aa:bb:cc:dd:ee:ff）
var macColonRegex = regexp.MustCompile(`(?i)\b([0-9a-f]{2}:){5}[0-9a-f]{2}\b`)

// macHyphenRegex 匹配以连字符分隔的 MAC 地址（如 aa-bb-cc-dd-ee-ff）
var macHyphenRegex = regexp.MustCompile(`(?i)\b([0-9a-f]{2}-){5}[0-9a-f]{2}\b`)

// sanitizeMAC 将字符串中所有 MAC 地址替换为占位符。
func sanitizeMAC(s string) string {
	s = macColonRegex.ReplaceAllString(s, "<mac-redacted>")
	s = macHyphenRegex.ReplaceAllString(s, "<mac-redacted>")
	return s
}

// RedactSNI 将 ClientHello 中的 SNI 主机名替换为通用占位符。
// 用于在未启用 -include-sni 时净化模板中的主机名字段。
func RedactSNI(hostname string) string {
	if hostname == "" {
		return ""
	}
	// 保留顶级域和一级域（如 "api.anthropic.com" → "<redacted>.anthropic.com"）
	// 这样在模板中仍能看出是哪个服务，但不泄露具体子域名
	parts := strings.Split(hostname, ".")
	if len(parts) >= 2 {
		parts[0] = "<redacted>"
		return strings.Join(parts, ".")
	}
	return "<redacted>"
}
