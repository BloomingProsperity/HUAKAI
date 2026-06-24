package main

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
)

// noopRefresher 满足 credentialworker.Refresher,仅供构造测试用 Scheduler。
type noopRefresher struct{}

func (noopRefresher) Refresh(_ context.Context, _ int64) error { return nil }

// CRED-288 凭据轮换扫描的生产 wiring gating 测试。
//
// 背景:ScanRotationDue / PostgresRotationStore / scheduler 钩子早已建好,但
// loadCredentialRotationScanFromEnv 这条 wiring 决策曾经缺失,导致 wiring 从不
// 注入 WithRotationScan、rotationStore=nil、maxAge=0,扫描恒早退返回 0(死开关)。
// 这些测试守住的正是"env 设置 => 扫描启用并用正确的 maxAge;env 留空/0 => 关"这条
// 决策,否则死开关复活。

// TestLoadCredentialRotationScan_EnabledByEnv 证明:配置一个正的 maxAge 后
// loadCredentialRotationScanFromEnv 返回 Enabled()==true 且 MaxAge/Limit 与 env 一致。
// 变异:把 wiring 改成忽略 env 直接返回 0(死开关原貌),Enabled() 变 false → 红。
func TestLoadCredentialRotationScan_EnabledByEnv(t *testing.T) {
	t.Setenv(credentialRotationMaxAgeEnv, "2160h") // 90 天
	t.Setenv(credentialRotationLimitEnv, "25")

	cfg, err := loadCredentialRotationScanFromEnv()
	if err != nil {
		t.Fatalf("loadCredentialRotationScanFromEnv: %v", err)
	}
	if !cfg.Enabled() {
		t.Fatal("配置了正的 maxAge,扫描必须被启用 (Enabled()==true)")
	}
	if cfg.MaxAge != 90*24*time.Hour {
		t.Fatalf("MaxAge 必须等于 env 的 2160h=90 天,got %v", cfg.MaxAge)
	}
	if cfg.Limit != 25 {
		t.Fatalf("Limit 必须等于 env 的 25,got %d", cfg.Limit)
	}
}

// TestLoadCredentialRotationScan_DisabledByDefault 证明:env 留空时扫描保持关闭
// (Enabled()==false),即 wiring 不注入 WithRotationScan、生产行为不翻转。
// 变异:把默认 fallback 从 0 改成一个正值(默认翻转开启),Enabled() 变 true → 红。
func TestLoadCredentialRotationScan_DisabledByDefault(t *testing.T) {
	t.Setenv(credentialRotationMaxAgeEnv, "")
	t.Setenv(credentialRotationLimitEnv, "")

	cfg, err := loadCredentialRotationScanFromEnv()
	if err != nil {
		t.Fatalf("loadCredentialRotationScanFromEnv: %v", err)
	}
	if cfg.Enabled() {
		t.Fatalf("env 未设时扫描必须默认关闭 (Enabled()==false),got MaxAge=%v", cfg.MaxAge)
	}
}

// TestLoadCredentialRotationScan_ExplicitZeroDisables 证明:显式置 0 也是关闭语义,
// 与 ScanRotationDue 的 maxAge<=0 => OFF 契约一致。
// 变异:把 envDurationDisable0Default 的 0 当成"用默认正值",Enabled() 变 true → 红。
func TestLoadCredentialRotationScan_ExplicitZeroDisables(t *testing.T) {
	t.Setenv(credentialRotationMaxAgeEnv, "0")

	cfg, err := loadCredentialRotationScanFromEnv()
	if err != nil {
		t.Fatalf("loadCredentialRotationScanFromEnv: %v", err)
	}
	if cfg.Enabled() {
		t.Fatal("显式 maxAge=0 必须关闭扫描 (与 ScanRotationDue 的 maxAge<=0 OFF 契约一致)")
	}
}

// TestLoadCredentialRotationScan_MalformedFailsLoud 证明:非法 duration 是 fail-loud
// 启动错误,而不是被静默吞掉退回默认(否则运维以为开了实际没开)。
// 变异:把 envDurationDisable0Default 的 parse 错误吞掉返回 fallback,err 变 nil → 红。
func TestLoadCredentialRotationScan_MalformedFailsLoud(t *testing.T) {
	t.Setenv(credentialRotationMaxAgeEnv, "ninety-days")

	if _, err := loadCredentialRotationScanFromEnv(); err == nil {
		t.Fatal("非法 maxAge 必须 fail-loud,got nil error")
	}
}

// TestBuildRotationScanOptions_GatesOnConfig 测的是 wiring 真正用到的注入决策:
// buildRotationScanOptions 在 enabled 时产出恰好 1 个 WithRotationScan option、
// 且该 option 应用到真实 credentialworker.Scheduler 后会把 store 与 maxAge/limit 装上;
// disabled 时产出 0 个 option、Scheduler 拿不到 store。这就是把死开关救活的那条决策。
//
// 变异 1:wiring 改回不据 cfg.Enabled() gate(总是注入或总是不注入)→ 两个 case 之一的
// option 数断言变红。
// 变异 2:loader 默认 fallback 改成正值(默认翻转开启)→ disabled 分支 len(opts)!=0 → 红。
//
// 注:扫描运行时真正置标的行为(RunOnce -> ScanRotationDue -> Flag)已由
// credentialworker 包内的 rotation_scheduler_test.go 用全套 fake 端到端覆盖;本测试
// 专注于 cmd/gateway 这层曾经缺失的 env->option gating。
func TestBuildRotationScanOptions_GatesOnConfig(t *testing.T) {
	store := credentialworker.NewPostgresRotationStore(nil)

	t.Run("enabled 配置产出 WithRotationScan 并装上 store+maxAge", func(t *testing.T) {
		cfg := credentialRotationScanConfig{MaxAge: 90 * 24 * time.Hour, Limit: 25}
		opts := buildRotationScanOptions(cfg, store)
		if len(opts) != 1 {
			t.Fatalf("enabled 配置必须产出 1 个 WithRotationScan option,got %d", len(opts))
		}
		// 把 option 应用到真实 Scheduler,确认 store/maxAge/limit 真被装上(否则 scan 仍恒 0)。
		s := credentialworker.NewScheduler(nil, nil, nil, &noopRefresher{}, opts...)
		gotStore, gotMaxAge, gotLimit := s.RotationScanConfigForTest()
		if gotStore == nil {
			t.Fatal("enabled 后 Scheduler 必须拿到非 nil 的 rotationStore(否则扫描早退恒 0)")
		}
		if gotMaxAge != 90*24*time.Hour || gotLimit != 25 {
			t.Fatalf("maxAge/limit 必须透传,got maxAge=%v limit=%d", gotMaxAge, gotLimit)
		}
	})

	t.Run("disabled 配置不产出 option,Scheduler 拿不到 store", func(t *testing.T) {
		cfg := credentialRotationScanConfig{MaxAge: 0, Limit: 25}
		opts := buildRotationScanOptions(cfg, store)
		if len(opts) != 0 {
			t.Fatalf("disabled 配置必须不产出 WithRotationScan option,got %d", len(opts))
		}
		s := credentialworker.NewScheduler(nil, nil, nil, &noopRefresher{}, opts...)
		gotStore, gotMaxAge, _ := s.RotationScanConfigForTest()
		if gotStore != nil || gotMaxAge != 0 {
			t.Fatalf("disabled 后 rotationStore 必须为 nil 且 maxAge=0,got store!=nil=%v maxAge=%v", gotStore != nil, gotMaxAge)
		}
	})
}
