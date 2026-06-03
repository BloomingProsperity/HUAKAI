package apikeyipallow

import (
	"fmt"
	"net/netip"
	"strings"
)

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

func NormalizeCSV(raw string) ([]string, error) {
	return Normalize(strings.Split(raw, ","))
}

func StorageText(entries []string) *string {
	if len(entries) == 0 {
		return nil
	}
	value := strings.Join(entries, ",")
	return &value
}

func AllowsCSV(raw *string, clientIP string) (bool, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return true, nil
	}
	entries, err := NormalizeCSV(*raw)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
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
		return "", false, fmt.Errorf("invalid IP allowlist entry %q: %w", raw, err)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()).String(), true, nil
}
