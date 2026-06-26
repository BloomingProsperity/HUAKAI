package apikeyipdeny

import (
	"fmt"
	"net/netip"
	"strings"
)

// Normalize 对一批 IP/CIDR 黑名单条目去重并归一化。
// 每个条目要么是裸 IP(展开为 /32 或 /128),要么是 CIDR 前缀。
// 空条目会被静默跳过。遇到第一个非法条目时返回错误。
func Normalize(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		normalized, ok, err := normalizeEntry(raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// NormalizeCSV 拆分逗号分隔的字符串,并对每个条目做归一化。
func NormalizeCSV(raw string) ([]string, error) {
	return Normalize(strings.Split(raw, ","))
}

// StorageText 把归一化后的切片重新拼回可空的逗号分隔字符串以便持久化。
// 当切片为空时返回 nil(清空该列)。
func StorageText(entries []string) *string {
	if len(entries) == 0 {
		return nil
	}
	value := strings.Join(entries, ",")
	return &value
}

// DeniesCSV 检查给定的 clientIP 是否被 raw(逗号分隔的 ip_blacklist 列值)
// 中任一条目覆盖。当 raw 为 nil 或空白时返回 (false, nil)——对未设置
// 黑名单的 key 行为零变化。
func DeniesCSV(raw *string, clientIP string) (bool, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return false, nil
	}
	entries, err := NormalizeCSV(*raw)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(clientIP))
	if err != nil {
		return false, fmt.Errorf("invalid client IP %q: %w", clientIP, err)
	}
	addr = addr.Unmap()
	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return false, err
		}
		if prefix.Contains(addr) {
			return true, nil
		}
	}
	return false, nil
}

func normalizeEntry(raw string) (string, bool, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false, nil
	}
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix.Masked().String(), true, nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", false, fmt.Errorf("invalid IP blacklist entry %q: %w", raw, err)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()).String(), true, nil
}
