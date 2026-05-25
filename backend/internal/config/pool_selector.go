// pool_selector.go — PASR-lite main-wire M1 atomic: typed config + ENV parse。
//
// 五个 mode 字面量与 PASR-lite synthesis §3.5 + D6 决策一致:
//
//	default        — 现状基线, 全走 DefaultSelector, 不构造 PASR
//	shadow         — DefaultSelector 主路径, PASR 异步只读对比 (不写 claim/slot/段表)
//	canary         — 按 SessionHash hash bucket 分流, hit 桶走 PASR actual, miss 走 default
//	pasr-primary   — 全量走 PASR; PASR pre-mutation 错误 fallback default
//	pasr-strict    — 全量 PASR, 任何错误 fail closed 不 fallback (验收终态)
//
// 渐进切换 SOP (synthesis §9 rollback contract):
//
//	default → shadow 5% → shadow 25% → shadow 100% → canary 5% → canary 25%
//	→ pasr-primary → pasr-strict
//
// shadow / canary 各自独立 percent ENV, 不烧死在 mode 名内 — Owner 调流量百分比
// 不需要改代码 + 重新编译。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PoolSelectorMode 是 selector dispatcher 的 5 种合法运行模式之一。
// 任何 ENV 落空或非法字符串均触发 typed error, 启动期 fail-fast — 不允许 silent 退化。
type PoolSelectorMode string

const (
	PoolSelectorModeDefault     PoolSelectorMode = "default"
	PoolSelectorModeShadow      PoolSelectorMode = "shadow"
	PoolSelectorModeCanary      PoolSelectorMode = "canary"
	PoolSelectorModePASRPrimary PoolSelectorMode = "pasr-primary"
	PoolSelectorModePASRStrict  PoolSelectorMode = "pasr-strict"
)

// 所有合法 mode 字面量, 用于解析失败时的错误消息提示。
var validPoolSelectorModes = []PoolSelectorMode{
	PoolSelectorModeDefault,
	PoolSelectorModeShadow,
	PoolSelectorModeCanary,
	PoolSelectorModePASRPrimary,
	PoolSelectorModePASRStrict,
}

// ErrInvalidPoolSelectorMode 指示 ENV 取到的 mode 字符串不在合法集。
var ErrInvalidPoolSelectorMode = errors.New("config: invalid pool selector mode")

// ErrInvalidPercent 指示 percent 类 ENV 解析失败或越界 (合法范围 [0, 100])。
var ErrInvalidPercent = errors.New("config: invalid percent (must be integer in [0, 100])")

// PoolSelectorConfig 是 selector dispatcher 启动期需要的全部配置快照。
//
// 字段说明:
//   - Mode:           主模式 (5 选 1), 决定 dispatcher 主路径
//   - ShadowPercent:  shadow 模式下采样比例 [0,100]; 对 default/canary/pasr-* 无效
//   - CanaryPercent:  canary 模式下走 PASR 的桶比例 [0,100]; 对 default/shadow/pasr-* 无效
//   - SamplingSalt:   FNV-1a + SessionHash 分桶时的盐, 防同一 hash 永远落同侧;
//     ops 改 salt 即等价 reshuffle bucket 分配
//
// 不变量: Mode == default 时其他字段被忽略, 但仍需通过 percent 校验, 配置错误一律
// 启动期暴露不延后到 hot path。
type PoolSelectorConfig struct {
	Mode          PoolSelectorMode
	ShadowPercent int
	CanaryPercent int
	SamplingSalt  string
}

// 默认值 — 与现状等价, 即不开 PASR。
const (
	defaultPoolSelectorMode = PoolSelectorModeDefault
	defaultShadowPercent    = 0
	defaultCanaryPercent    = 0
	defaultPoolSelectorSalt = "huakai-pasr-v1"
)

// LoadPoolSelector 从 ENV 加载 selector dispatcher 配置。 ENV key 命名空间:
//
//	HUAKAI_POOL_SELECTOR_MODE       (default 时全部 PASR 字段被忽略)
//	HUAKAI_POOL_SELECTOR_SHADOW_PCT (shadow mode 采样, 0-100)
//	HUAKAI_POOL_SELECTOR_CANARY_PCT (canary mode PASR 桶, 0-100)
//	HUAKAI_POOL_SELECTOR_SALT       (FNV salt, 默认 "huakai-pasr-v1")
//
// 任意 ENV 解析失败 → typed error 包装上下文, caller (run) 直接 return 让 main
// 退出。不做 silent fallback — synthesis §3.7 + D5 已锁定 fail-fast 不退化。
func LoadPoolSelector() (*PoolSelectorConfig, error) {
	cfg := &PoolSelectorConfig{
		Mode:          defaultPoolSelectorMode,
		ShadowPercent: defaultShadowPercent,
		CanaryPercent: defaultCanaryPercent,
		SamplingSalt:  defaultPoolSelectorSalt,
	}

	if raw := os.Getenv("HUAKAI_POOL_SELECTOR_MODE"); raw != "" {
		mode, err := ParsePoolSelectorMode(raw)
		if err != nil {
			return nil, err
		}
		cfg.Mode = mode
	}

	if raw := os.Getenv("HUAKAI_POOL_SELECTOR_SHADOW_PCT"); raw != "" {
		pct, err := parsePercent(raw, "HUAKAI_POOL_SELECTOR_SHADOW_PCT")
		if err != nil {
			return nil, err
		}
		cfg.ShadowPercent = pct
	}

	if raw := os.Getenv("HUAKAI_POOL_SELECTOR_CANARY_PCT"); raw != "" {
		pct, err := parsePercent(raw, "HUAKAI_POOL_SELECTOR_CANARY_PCT")
		if err != nil {
			return nil, err
		}
		cfg.CanaryPercent = pct
	}

	if raw := os.Getenv("HUAKAI_POOL_SELECTOR_SALT"); raw != "" {
		cfg.SamplingSalt = raw
	}

	// 兜底: 即使 ENV 解析全过, 仍跑一次 Validate, 让"非 ENV 注入路径"
	// (未来 YAML 加载 / 测试构造 / dispatcher 复检) 与 ENV 路径走同一校验通道。
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 检查 PoolSelectorConfig 自身一致性。 LoadPoolSelector 末尾调一次,
// dispatcher 构造时也应再调一次 — 防止经由非 ENV 路径 (测试 struct literal /
// 未来 YAML reload) 注入非法值后绕过启动校验。
//
// 错误用 errors.Is 可识别为 ErrInvalidPoolSelectorMode 或 ErrInvalidPercent,
// 与 ENV 解析路径返回的错误同源。
func (c *PoolSelectorConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: nil config", ErrInvalidPoolSelectorMode)
	}
	modeOK := false
	for _, m := range validPoolSelectorModes {
		if c.Mode == m {
			modeOK = true
			break
		}
	}
	if !modeOK {
		return fmt.Errorf("%w: %q (expected one of %v)",
			ErrInvalidPoolSelectorMode, c.Mode, validPoolSelectorModes)
	}
	if c.ShadowPercent < 0 || c.ShadowPercent > 100 {
		return fmt.Errorf("%w: ShadowPercent=%d", ErrInvalidPercent, c.ShadowPercent)
	}
	if c.CanaryPercent < 0 || c.CanaryPercent > 100 {
		return fmt.Errorf("%w: CanaryPercent=%d", ErrInvalidPercent, c.CanaryPercent)
	}
	return nil
}

// ParsePoolSelectorMode 把 ENV 取到的字符串映射成 PoolSelectorMode。
// 大小写不敏感, 前后空格容错, 但内部不允许空格/typo。 不在合法集合 → typed
// error 列出全部合法值, 方便 ops 排错。
func ParsePoolSelectorMode(raw string) (PoolSelectorMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	for _, m := range validPoolSelectorModes {
		if string(m) == normalized {
			return m, nil
		}
	}
	return "", fmt.Errorf("%w: %q (expected one of %v)",
		ErrInvalidPoolSelectorMode, raw, validPoolSelectorModes)
}

// parsePercent 解析 [0, 100] 闭区间整数百分比 ENV。 任何异常 (非数字 / 负数 /
// > 100) → typed error 含 ENV 名, 启动期 fail-fast。
func parsePercent(raw, envName string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%w: %s=%q: %v", ErrInvalidPercent, envName, raw, err)
	}
	if v < 0 || v > 100 {
		return 0, fmt.Errorf("%w: %s=%d (out of range)", ErrInvalidPercent, envName, v)
	}
	return v, nil
}

// IsPASR 返回 true iff 当前 mode 会真正构造 PASRSelector 实例 (即 default 之外
// 全部模式)。 caller (main.go) 用此判断是否要启动 SegmentTable / AgingWorker /
// 注册 cache feedback observer。
func (c *PoolSelectorConfig) IsPASR() bool {
	return c.Mode != PoolSelectorModeDefault
}

// NeedsShadowInstance 返回 true iff 当前 mode 需要 dispatcher 拉一条 shadow 旁路
// (只读, 不写 claim/slot/段表)。 当前 5 mode 中 shadow 是唯一需要的 — canary /
// pasr-primary / pasr-strict 走的是 actual 路径, 不需要 shadow 实例。
func (c *PoolSelectorConfig) NeedsShadowInstance() bool {
	return c.Mode == PoolSelectorModeShadow
}

// NeedsActualPASR 返回 true iff 当前 mode 会让 PASR 真正写 claim/slot
// (canary 命中桶 / pasr-primary 全量 / pasr-strict 全量)。 caller 用此决定
// 是否要在构造 PASRSelector 时注入真 ClaimGate + SlotManager。
func (c *PoolSelectorConfig) NeedsActualPASR() bool {
	switch c.Mode {
	case PoolSelectorModeCanary, PoolSelectorModePASRPrimary, PoolSelectorModePASRStrict:
		return true
	default:
		return false
	}
}
