package config

import "testing"

// TestLoadDefaultKeyQuota_FactoryDefaults 守:无 env → 出厂保守默认(启用 / RPM 60 / 并发 5)。
func TestLoadDefaultKeyQuota_FactoryDefaults(t *testing.T) {
	t.Setenv(EnvDefaultKeyQuotaEnabled, "")
	t.Setenv(EnvDefaultKeyQuotaRPM, "")
	t.Setenv(EnvDefaultKeyQuotaConcurrency, "")
	cfg := LoadDefaultKeyQuota()
	if !cfg.Enabled || cfg.RPM != 60 || cfg.ConcurrencyMax != 5 {
		t.Fatalf("出厂默认不对: %+v", cfg)
	}
}

// TestLoadDefaultKeyQuota_EnvOverride 守:运维可经 env 调整默认值。
func TestLoadDefaultKeyQuota_EnvOverride(t *testing.T) {
	t.Setenv(EnvDefaultKeyQuotaEnabled, "true")
	t.Setenv(EnvDefaultKeyQuotaRPM, "120")
	t.Setenv(EnvDefaultKeyQuotaConcurrency, "8")
	cfg := LoadDefaultKeyQuota()
	if !cfg.Enabled || cfg.RPM != 120 || cfg.ConcurrencyMax != 8 {
		t.Fatalf("env 覆盖不对: %+v", cfg)
	}
}

// TestLoadDefaultKeyQuota_DisableSwitch 守:运维可整体关闭默认配额兜底。
func TestLoadDefaultKeyQuota_DisableSwitch(t *testing.T) {
	t.Setenv(EnvDefaultKeyQuotaEnabled, "false")
	cfg := LoadDefaultKeyQuota()
	if cfg.Enabled {
		t.Fatalf("Enabled=false 开关未生效: %+v", cfg)
	}
}

// TestLoadDefaultKeyQuota_InvalidFallsBackToFactory 守:配置解析失败一律回退出厂默认,
// 绝不因 env 写错把保护静默关掉(把无效值当 0 会导致防滥用失效)。
// MUTATION:若把无效值落成 0/关闭而非回退默认,本断言 RED。
func TestLoadDefaultKeyQuota_InvalidFallsBackToFactory(t *testing.T) {
	t.Setenv(EnvDefaultKeyQuotaEnabled, "notabool")
	t.Setenv(EnvDefaultKeyQuotaRPM, "abc")
	t.Setenv(EnvDefaultKeyQuotaConcurrency, "-")
	cfg := LoadDefaultKeyQuota()
	if !cfg.Enabled || cfg.RPM != 60 || cfg.ConcurrencyMax != 5 {
		t.Fatalf("无效 env 应回退出厂默认; got %+v", cfg)
	}
}
