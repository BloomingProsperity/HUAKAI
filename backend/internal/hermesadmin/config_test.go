package hermesadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// fakeSettings 是一个 settingGetter，针对管理员通知邮箱键返回固定的值/错误。
type fakeSettings struct {
	value string
	err   error
}

func (f fakeSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if f.err != nil {
		return platformsettings.StoredSetting{}, f.err
	}
	return platformsettings.StoredSetting{Key: key, Value: f.value, Source: platformsettings.SourceDB}, nil
}

// TestResolveRecipientSettingWins：非空的平台设置优先于环境变量兜底。
// 捕获的回归：若优先级被颠倒，环境变量的值会盖掉运维显式配置的设置。
func TestResolveRecipientSettingWins(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: "setting@huakai.test"})
	if got != "setting@huakai.test" || src != "setting" {
		t.Fatalf("expected setting to win, got %q (%s)", got, src)
	}
}

// TestResolveRecipientEnvFallback：空设置会兜底到环境变量的值。
// 捕获的回归：若空设置的分支不下沉，已配置的环境变量收件人会被忽略，worker 将
// 永远不会启动。
func TestResolveRecipientEnvFallback(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: ""})
	if got != "env@huakai.test" || src != "env" {
		t.Fatalf("expected env fallback, got %q (%s)", got, src)
	}
}

// TestResolveRecipientNeither：既无设置也无环境变量时，产出空的 "none" 结果
//——这正是 worker 用来保持关闭的信号。
// 捕获的回归：若曾经注入过某个默认收件人，未经配置的部署就会开始发送邮件。
func TestResolveRecipientNeither(t *testing.T) {
	t.Setenv(EnvRecipient, "")
	got, src := ResolveRecipient(context.Background(), fakeSettings{value: ""})
	if got != "" || src != "none" {
		t.Fatalf("expected no recipient, got %q (%s)", got, src)
	}
}

// TestResolveRecipientSettingErrorFallsToEnv：设置读取出错绝不能阻塞启动
//——它会下沉到环境变量兜底。
// 捕获的回归：若设置的短暂错误导致提前返回，环境变量收件人会被忽略，已配置的
// 部署将无法启动。
func TestResolveRecipientSettingErrorFallsToEnv(t *testing.T) {
	t.Setenv(EnvRecipient, "env@huakai.test")
	got, src := ResolveRecipient(context.Background(), fakeSettings{err: errors.New("db down")})
	if got != "env@huakai.test" || src != "env" {
		t.Fatalf("expected env fallback on settings error, got %q (%s)", got, src)
	}
}

// TestEnabledDefaultsOff：启用开关需主动开启——未设置/非真值即 OFF。
// 捕获的回归：若默认值翻转为 ON，未经配置的部署就会开始发送邮件——这正是本波次
// 守护的故障安全点。
func TestEnabledDefaultsOff(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	if EnabledFromEnv() {
		t.Fatalf("enable flag must default OFF when unset")
	}
	t.Setenv(EnvEnabled, "false")
	if EnabledFromEnv() {
		t.Fatalf("enable flag must be OFF for 'false'")
	}
	t.Setenv(EnvEnabled, "true")
	if !EnabledFromEnv() {
		t.Fatalf("enable flag must be ON for 'true'")
	}
}

// TestIntervalFromEnv：未设置时取默认值，已设置时解析，遇到乱码/非正数时报错。
// 捕获的回归：非正数间隔会让 time.NewTicker 在运行时 panic；启动时的报错可阻止
// 这种情况。
func TestIntervalFromEnv(t *testing.T) {
	t.Setenv(EnvInterval, "")
	if d, err := IntervalFromEnv(); err != nil || d != DefaultInterval {
		t.Fatalf("unset => default, got %s err=%v", d, err)
	}
	t.Setenv(EnvInterval, "6h")
	if d, err := IntervalFromEnv(); err != nil || d != 6*time.Hour {
		t.Fatalf("6h => 6h, got %s err=%v", d, err)
	}
	t.Setenv(EnvInterval, "0s")
	if _, err := IntervalFromEnv(); err == nil {
		t.Fatalf("non-positive interval must error")
	}
	t.Setenv(EnvInterval, "garbage")
	if _, err := IntervalFromEnv(); err == nil {
		t.Fatalf("garbage interval must error")
	}
}

// TestLoadConfigComposition：LoadConfig 把 enable + interval + recipient +
// tenant 串到一起；一个被禁用、未经配置的部署会产出 Enabled=false 和
// Recipient=""（worker 不会启动）。
func TestLoadConfigComposition(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvRecipient, "")
	t.Setenv(EnvInterval, "")
	cfg, err := LoadConfig(context.Background(), fakeSettings{value: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Enabled || cfg.Recipient != "" {
		t.Fatalf("unconfigured deploy must be disabled with no recipient, got %+v", cfg)
	}
	if cfg.Interval != DefaultInterval || cfg.TenantID != 1 {
		t.Fatalf("expected default interval+tenant, got %+v", cfg)
	}
}
