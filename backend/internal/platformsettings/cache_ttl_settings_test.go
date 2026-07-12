package platformsettings

import (
	"context"
	"errors"
	"testing"
)

func TestAnthropicTTL1hRewriteSettingDefaultsOffAndReadsRuntimeValue(t *testing.T) {
	if value, ok := DefaultValue(KeyCacheAnthropicTTL1hRewrite); !ok || value != "false" {
		t.Fatalf("默认值=(%q,%v)，期望 false 且已注册", value, ok)
	}
	if _, err := ValidateValue(KeyCacheAnthropicTTL1hRewrite, "not-bool"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("非 bool 值应被拒绝，实际 err=%v", err)
	}

	ctx := context.Background()
	service := NewService(NewMemoryStore(), nil)
	enabled, err := service.AnthropicTTL1hRewriteEnabled(ctx)
	if err != nil || enabled {
		t.Fatalf("默认 getter=(%v,%v)，期望 false,nil", enabled, err)
	}
	if _, err := service.Upsert(ctx, UpsertInput{Key: KeyCacheAnthropicTTL1hRewrite, Value: "true", UpdatedBy: "operator"}); err != nil {
		t.Fatalf("写入运行时设置: %v", err)
	}
	enabled, err = service.AnthropicTTL1hRewriteEnabled(ctx)
	if err != nil || !enabled {
		t.Fatalf("开启后 getter=(%v,%v)，期望 true,nil", enabled, err)
	}
}
