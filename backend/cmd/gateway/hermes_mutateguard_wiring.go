package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// 本文件解析并构建 S2「为 Hermes MUTATING 路径划定边界」的守卫
//(并发信号量 + tx 超时 + 按 operator-token 的限流器),使一波突发 mutation 无法耗尽
// 共享的 pgxpool / advisory-lock 槽位、把核心网关拖垮(audit B4/B5)。
//
// 每个旋钮都是「附加式」且「默认保守」的,并各自带一个 disable sentinel:未设置任何
// HUAKAI_HERMES_MUTATE_* env 时,解析出的配置就是内置默认值;显式设为 0 则禁用该单个守卫;
// 全部未设置 + 全部禁用的 orchestrator 在字节层面等同于旧的「无界、无超时」行为。

const (
	// hermesMutateMaxConcurrencyEnv 限制并发的 mutating 执行数。默认 4
	//(pool MaxConns=16 => mutation 占用 <=25%)。0/负数 禁用它。
	hermesMutateMaxConcurrencyEnv = "HUAKAI_HERMES_MUTATE_MAX_CONCURRENCY"
	// hermesMutateAcquireWaitEnv 限定等待并发槽位的时长,超时则返回干净的 429 busy。默认 2s。
	hermesMutateAcquireWaitEnv = "HUAKAI_HERMES_MUTATE_ACQUIRE_WAIT"
	// hermesMutateTxDeadlineEnv 限定单个 mutation tx 的时长(客户端 ctx deadline +
	// SET LOCAL statement_timeout)。默认 90s —— 刻意取为 dlq_replay 内层 claim lease(30s)的 3 倍,
	// 使一次合法的慢结算能够跑完,同时仍能对真正卡死的 handler 设上限。0 禁用它。
	hermesMutateTxDeadlineEnv = "HUAKAI_HERMES_MUTATE_TX_DEADLINE"
	// hermesMutateRatePerTokenEnv 限制每个 operator token 在每个窗口内已确认的 mutation 数。
	// 默认 30。0 禁用它。
	hermesMutateRatePerTokenEnv = "HUAKAI_HERMES_MUTATE_RATE_PER_TOKEN"
	// hermesMutateRateWindowEnv 是按 token 的限流窗口。默认 1m。
	hermesMutateRateWindowEnv = "HUAKAI_HERMES_MUTATE_RATE_WINDOW"
)

const (
	defaultHermesMutateMaxConcurrency = 4
	defaultHermesMutateAcquireWait    = 2 * time.Second
	// 90s 是承重的:dlq_replay 会重跑 Settler.Settle,其内层 claim
	// lease 为 30s;90s = 3 倍余量,使一次合法的慢结算不会被切断,同时
	// 卡死的 handler 仍能被设上限。默认值不要收得更紧。
	defaultHermesMutateTxDeadline   = 90 * time.Second
	defaultHermesMutateRatePerToken = 30
	defaultHermesMutateRateWindow   = time.Minute
)

// hermesMutateGuardConfig 是解析后的 S2 守卫配置。其零值
//(所有旋钮均未设置)携带内置默认值;某个旋钮显式置 0
// 则禁用对应的那一项守卫。
type hermesMutateGuardConfig struct {
	maxConcurrency int
	acquireWait    time.Duration
	txDeadline     time.Duration
	ratePerToken   int
	rateWindow     time.Duration
}

// hermesMutateGuardConfigFromEnv 解析这 5 个 S2 旋钮,遇到格式错误的值时
// fail-loud(绝不静默回退,以免给路径设错边界)。未设置的
// 旋钮取其保守默认值;显式置 0 则禁用对应守卫。
func hermesMutateGuardConfigFromEnv() (hermesMutateGuardConfig, error) {
	cfg := hermesMutateGuardConfig{}
	var err error

	if cfg.maxConcurrency, err = envIntDisable0Default(hermesMutateMaxConcurrencyEnv, defaultHermesMutateMaxConcurrency); err != nil {
		return cfg, err
	}
	if cfg.acquireWait, err = envDurationPositiveDefault(hermesMutateAcquireWaitEnv, defaultHermesMutateAcquireWait); err != nil {
		return cfg, err
	}
	if cfg.txDeadline, err = envDurationDisable0Default(hermesMutateTxDeadlineEnv, defaultHermesMutateTxDeadline); err != nil {
		return cfg, err
	}
	if cfg.ratePerToken, err = envIntDisable0Default(hermesMutateRatePerTokenEnv, defaultHermesMutateRatePerToken); err != nil {
		return cfg, err
	}
	if cfg.rateWindow, err = envDurationPositiveDefault(hermesMutateRateWindowEnv, defaultHermesMutateRateWindow); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// orchestratorOptions 返回本配置对应的、附加式的 MutateOrchestrator 选项:
// 并发守卫(信号量 + 获取等待)与 tx 超时。
// 被禁用(0)的守卫产生一个 no-op 选项,因此一个全部禁用的配置
// 得到的就是旧版 orchestrator。
func (c hermesMutateGuardConfig) orchestratorOptions() []hermesops.MutateOption {
	return []hermesops.MutateOption{
		hermesops.WithConcurrencyGuard(mutateguard.NewSemaphore(c.maxConcurrency), c.acquireWait),
		hermesops.WithTxDeadline(c.txDeadline),
	}
}

// newRateLimiter 构建按 operator-token 的滑动窗口限流器(生产时钟)。
// ratePerToken 为 0 时得到一个被禁用的限流器(Allow 恒为 true)。
func (c hermesMutateGuardConfig) newRateLimiter() *mutateguard.RateLimiter {
	return mutateguard.NewRateLimiter(c.ratePerToken, c.rateWindow, 0, nil)
}

// --- fail-loud 的 env 辅助函数(标注处 0 = 禁用哨兵值)----------------

// envIntDisable0Default 解析一个 int 旋钮:未设置 => 取 fallback;显式 0(禁用
// 哨兵值)被尊重;负数被钳为 0(禁用);
// 非整数则触发 fail-loud 的启动报错。
func envIntDisable0Default(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer (0 disables), got %q: %w", name, raw, err)
	}
	if v < 0 {
		v = 0
	}
	return v, nil
}

// envDurationDisable0Default 解析一个 duration 旋钮:未设置 => 取 fallback;
// 显式 0(禁用哨兵值)被尊重;格式错误的值触发 fail-loud。
func envDurationDisable0Default(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	d, err := parseDurationOrSeconds(name, raw)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		d = 0
	}
	return d, nil
}

// envDurationPositiveDefault 解析一个必须为正的 duration 旋钮
//(acquire wait / rate window 没有有意义的「禁用」形式——它们只在
// 配对的守卫启用时才有意义)。未设置 => 取 fallback;<=0 或格式错误
// 则 fail-loud。
func envDurationPositiveDefault(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	d, err := parseDurationOrSeconds(name, raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got %q", name, raw)
	}
	return d, nil
}

// parseDurationOrSeconds 接受 Go duration(90s、1m)或一个裸整数
// 秒数,与 config.envPositiveDurationDefault 的宽松风格保持一致。
func parseDurationOrSeconds(name, raw string) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 90s or seconds, got %q: %w", name, raw, err)
	}
	return time.Duration(seconds) * time.Second, nil
}
