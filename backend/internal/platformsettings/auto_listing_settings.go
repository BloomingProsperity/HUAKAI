package platformsettings

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 自动上架管道(part 2)的两个平台设置的校验与类型化读取。auto-vendor 白名单只接受
// 账号级发现支持的 vendor,防止运营配错一个永远不会命中的 vendor。

var autoListingKnownVendors = map[string]struct{}{
	"openai":      {},
	"anthropic":   {},
	"gemini":      {},
	"grok":        {},
	"kimi":        {},
	"antigravity": {},
}

func validateAutoListingVendorsValue(key SettingKey, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "[]", nil
	}
	var vendors []string
	if err := json.Unmarshal([]byte(trimmed), &vendors); err != nil {
		return "", fmt.Errorf("%w: %s must be a JSON string array of vendors", ErrInvalidValue, key)
	}
	seen := make(map[string]struct{}, len(vendors))
	out := make([]string, 0, len(vendors))
	for _, vendor := range vendors {
		vendor = strings.ToLower(strings.TrimSpace(vendor))
		if vendor == "" {
			return "", fmt.Errorf("%w: %s contains an empty vendor", ErrInvalidValue, key)
		}
		if _, ok := autoListingKnownVendors[vendor]; !ok {
			return "", fmt.Errorf("%w: %s contains unknown vendor %q", ErrInvalidValue, key, vendor)
		}
		if _, ok := seen[vendor]; ok {
			continue
		}
		seen[vendor] = struct{}{}
		out = append(out, vendor)
	}
	sort.Strings(out)
	normalized, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("%w: %s could not be normalized", ErrInvalidValue, key)
	}
	return string(normalized), nil
}

// AutoListingConfig 是自动上架管道的运行期开关快照。
type AutoListingConfig struct {
	Enabled     bool
	AutoVendors map[string]struct{}
}

// VendorIsAuto 报告某 vendor 是否走自动挡(总闸开 + 在白名单内)。
func (c AutoListingConfig) VendorIsAuto(vendor string) bool {
	if !c.Enabled {
		return false
	}
	_, ok := c.AutoVendors[strings.ToLower(strings.TrimSpace(vendor))]
	return ok
}

// AutoListing 读取当前自动上架配置。任一键读取失败回退到该键默认值,保证调用方拿到确定结果。
func (s *Service) AutoListing(ctx context.Context) AutoListingConfig {
	cfg := AutoListingConfig{AutoVendors: map[string]struct{}{}}
	if enabled, err := s.Get(ctx, KeyAutoListingEnabled); err == nil {
		cfg.Enabled = strings.EqualFold(strings.TrimSpace(enabled.Value), "true")
	}
	vendorsRaw := defaultSettingValueMap[KeyAutoListingAutoVendors]
	if stored, err := s.Get(ctx, KeyAutoListingAutoVendors); err == nil && strings.TrimSpace(stored.Value) != "" {
		vendorsRaw = stored.Value
	}
	var vendors []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(vendorsRaw)), &vendors); err == nil {
		for _, vendor := range vendors {
			vendor = strings.ToLower(strings.TrimSpace(vendor))
			if vendor != "" {
				cfg.AutoVendors[vendor] = struct{}{}
			}
		}
	}
	return cfg
}
