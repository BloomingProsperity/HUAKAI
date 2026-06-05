package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/budget"
)

// 守:配置了 Redis 但 URL 非法 → 必须返回 error(让上层告警),不能静默成功退回内存
// (多副本下每副本独立限额 = 限额×副本数)。Mutation: 忽略 ParseURL 错误 → err==nil,红。
func TestBuildBudgetStore_InvalidRedisURLReturnsErrorWithMemoryFallback(t *testing.T) {
	cfg := &Config{}
	cfg.Budget.Enabled = true
	cfg.Budget.RedisURL = "::::not-a-redis-url"
	mem := budget.NewMemoryStore(nil)
	store, err := buildBudgetStore(cfg, mem)
	if err == nil {
		t.Fatal("invalid redis url must return error (silent memory fallback multiplies per-replica limits)")
	}
	if store == nil {
		t.Fatal("must still return a non-nil memory fallback store (availability over budget precision)")
	}
}

func TestBuildBudgetStore_EmptyRedisURLUsesMemoryWithoutError(t *testing.T) {
	cfg := &Config{}
	cfg.Budget.Enabled = true
	mem := budget.NewMemoryStore(nil)
	store, err := buildBudgetStore(cfg, mem)
	if err != nil || store == nil {
		t.Fatalf("empty redis url: want memory store + nil error, got store=%v err=%v", store, err)
	}
}
