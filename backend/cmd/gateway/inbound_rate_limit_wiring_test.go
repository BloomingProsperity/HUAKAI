package main

import (
	"context"
	"strings"
	"testing"
)

func TestBuildInboundRateLimitAllowsLocalOnlyInDevelopment(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "test")
	runtime, err := buildSharedRateLimits(context.Background(), &Config{})
	if err != nil || runtime.inbound != nil || runtime.login != nil || runtime.windows != nil || runtime.storm != nil || runtime.ping != nil || runtime.close != nil {
		t.Fatalf("开发单实例空配置 inbound=%v login=%v windows=%v storm=%v ping=%v close=%v err=%v", runtime.inbound != nil, runtime.login != nil, runtime.windows != nil, runtime.storm != nil, runtime.ping != nil, runtime.close != nil, err)
	}
}

func TestBuildInboundRateLimitRequiresRedisInProduction(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv(envRateLimitDisable, "")
	runtime, err := buildSharedRateLimits(context.Background(), &Config{})
	if err == nil || runtime.inbound != nil || runtime.login != nil || runtime.windows != nil || runtime.storm != nil || runtime.ping != nil || runtime.close != nil {
		t.Fatalf("生产空配置必须拒绝 inbound=%v login=%v windows=%v storm=%v ping=%v close=%v err=%v", runtime.inbound != nil, runtime.login != nil, runtime.windows != nil, runtime.storm != nil, runtime.ping != nil, runtime.close != nil, err)
	}
	if !strings.Contains(err.Error(), "HUAKAI_RATE_LIMIT_REDIS_URL") {
		t.Fatalf("错误必须给出可执行配置提示: %v", err)
	}
}

func TestBuildInboundRateLimitRejectsDisableInProduction(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv(envRateLimitDisable, "true")
	runtime, err := buildSharedRateLimits(context.Background(), &Config{RateLimitRedisURL: "redis://redis:6379/0"})
	if err == nil || runtime.inbound != nil || runtime.login != nil || runtime.windows != nil || runtime.storm != nil || runtime.ping != nil || runtime.close != nil {
		t.Fatalf("生产关闭限流必须拒绝 inbound=%v login=%v windows=%v storm=%v ping=%v close=%v err=%v", runtime.inbound != nil, runtime.login != nil, runtime.windows != nil, runtime.storm != nil, runtime.ping != nil, runtime.close != nil, err)
	}
	if !strings.Contains(err.Error(), envRateLimitDisable) {
		t.Fatalf("错误必须指出禁用开关: %v", err)
	}
}

func TestBuildInboundRateLimitRejectsInvalidRedisURL(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "test")
	runtime, err := buildSharedRateLimits(context.Background(), &Config{RateLimitRedisURL: "not-a-redis-url"})
	if err == nil || runtime.inbound != nil || runtime.login != nil || runtime.windows != nil || runtime.storm != nil || runtime.ping != nil || runtime.close != nil {
		t.Fatalf("非法 URL 必须拒绝 inbound=%v login=%v windows=%v storm=%v ping=%v close=%v err=%v", runtime.inbound != nil, runtime.login != nil, runtime.windows != nil, runtime.storm != nil, runtime.ping != nil, runtime.close != nil, err)
	}
}
