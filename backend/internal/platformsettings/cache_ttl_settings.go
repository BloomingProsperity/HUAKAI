package platformsettings

import "context"

const KeyCacheAnthropicTTL1hRewrite SettingKey = "cache.anthropic_ttl_1h_rewrite"

func (s *Service) AnthropicTTL1hRewriteEnabled(ctx context.Context) (bool, error) {
	setting, err := s.Get(ctx, KeyCacheAnthropicTTL1hRewrite)
	if err != nil {
		return false, err
	}
	return setting.Value == "true", nil
}
