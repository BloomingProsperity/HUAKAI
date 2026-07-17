// 包 authcooldown 实现「auth 失败降级临时车道」——独立于渠道健康分/错误率 FSM 的一条轻量车道。
//
// 背景(缺口① S1):坏 key 账号在 auth 失败(401 / Grok 400-auth)后既不 failover 也不冷却,
// 恒 StateActive、PoolGate 恒放行,占据池首优先级时每个请求首选它、吃一发 401;auth-failover
// 子预算硬限一次,≥2 个坏号即请求直接 401 给客户端——坏号黑洞整个模型流量。
//
// 设计要点：
//   - 架构:独立 auth 车道,不写健康 State/Score、不进 error-rate/ban-ramp 窗口(auth blip 不污染健康分);
//   - 算法:封顶指数退避(base<<(strike-1),cap 封顶)替代定长冷却——常态 token 过期几秒热刷新自愈、
//     真死 key 几何增长快速止损;iron-clad 达 strike 上限升 HardDisabled,ambiguous 通用 401 永不永久禁;
//   - 生态:凭证热刷新 worker 单向通报车道(OnRefreshResult:仅永久失效→硬禁)+ 结构化日志把
//     「坏号被冷却/恢复」变运营可见。刷新成功刻意不解除冷却(见 OnRefreshResult 注释)。
//
// Phase1 纯内存(重启丢失、非账号行天然可见);持久化列/表为 Phase2 Owner-gated。
package authcooldown

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// FailureClass 区分 auth 失败的确定性,决定是否允许升级到 HardDisabled(永久禁用)。
type FailureClass int

const (
	// ClassAmbiguous:通用 401(无关键词铁证)。永远停在指数退避自愈,绝不升 HardDisabled
	// 避免因瞬时 401 把正常账号永久禁用。
	ClassAmbiguous FailureClass = iota
	// ClassIronClad:invalid_grant / token_revoked / Grok 400-auth 等铁证类。达 strike 上限 K 升 HardDisabled。
	ClassIronClad
)

func (c FailureClass) label() string {
	if c == ClassIronClad {
		return "iron_clad"
	}
	return "ambiguous"
}

// Clear 的原因标签,进日志供运营区分账号是如何解除冷却的。
const (
	ClearReasonSuccess        = "request_success" // 一次成功请求(self-heal)
	ClearReasonOperatorResume = "operator_resume" // 运营 ForceActive/ManualResume
)

// Config 是退避与升级参数。零值经 normalized() 补默认。
type Config struct {
	// Base 是首次退避基数;短基数让常态 token 过期被热刷新几秒修好、自愈快。
	Base time.Duration
	// Cap 是退避封顶;几何增长撞顶后稳定在此,真死 key 每 Cap 才重试一发、不再黑洞。
	Cap time.Duration
	// HardDisableStrikeK 是 iron-clad 类升 HardDisabled 的 strike 阈值。
	HardDisableStrikeK int
}

func (c Config) normalized() Config {
	if c.Base <= 0 {
		c.Base = 30 * time.Second
	}
	if c.Cap <= 0 || c.Cap < c.Base {
		c.Cap = 30 * time.Minute
	}
	if c.HardDisableStrikeK <= 0 {
		c.HardDisableStrikeK = 3
	}
	return c
}

// entry 是单个账号的车道状态。主键为 ProviderAccountID(active 凭证 1:1)。
type entry struct {
	strike       int       // 连续 auth 失败计数(经窗口去抖,每个退避窗口至多 +1)
	authUntil    time.Time // 此刻之前不合格(临时移出选号)
	hardDisabled bool      // 硬禁用:iron-clad 达 strike 上限 或 热刷新拿到 invalid_grant
	credVersion  int       // 版本感知:凭证轮换(版本变化)→ 视作全新账号重置 strike
}

// Store 是进程内的 auth 降级车道。所有方法并发安全(单 mutex,最后写入语义明确)。
type Store struct {
	mu      sync.Mutex
	entries map[int64]*entry
	cfg     Config
}

// Snapshot 是单个账号当前 auth 降级状态的只读副本。
// AuthUntil 过期后 Eligible 会恢复为 true，但 strike 会保留到成功、轮换或人工恢复。
type Snapshot struct {
	Found             bool
	Eligible          bool
	HardDisabled      bool
	Strike            int
	AuthUntil         *time.Time
	CredentialVersion int
}

// NewStore 构造车道。零值 Config → 默认(base=30s、cap=30min、K=3)。
func NewStore(cfg Config) *Store {
	return &Store{
		entries: make(map[int64]*entry),
		cfg:     cfg.normalized(),
	}
}

// Suspend 记录一次 auth 失败:升级 strike、算 AuthUntil、按分级决定是否 HardDisabled。
// class=iron-clad 且 strike 达上限 K → HardDisabled;ambiguous 永不 HardDisabled。
// 每次真实升级(越过去抖窗口)记一条 WarnContext,HardDisabled 升级另记一条(可用性事件)。
func (s *Store) Suspend(ctx context.Context, accountID int64, class FailureClass, credVersion int, now time.Time) {
	if s == nil || accountID == 0 {
		return
	}
	s.mu.Lock()
	e := s.entries[accountID]
	if e == nil {
		e = &entry{credVersion: credVersion}
		s.entries[accountID] = e
	}
	// 版本感知 strike-reset:凭证已轮换到「更新」的版本 → 旧的失败历史作废,当作全新账号。
	// 只认版本前进(>):迟到的旧版本事件(长流式在途请求携带轮换前的 credVersion)不得
	// 反向重置新版本已积累的 strike/HardDisabled(审查 S3)。
	if credVersion > 0 && e.credVersion > 0 && credVersion > e.credVersion {
		e.strike = 0
		e.hardDisabled = false
		e.authUntil = time.Time{}
	}
	if credVersion > 0 {
		e.credVersion = credVersion
	}
	// 并发去抖:仅当已越过上次退避窗口(now 严格晚于 authUntil)才升级 strike/TTL。并发爆发
	//(同一坏号瞬时多发 401)只在第一发升级,其余落在窗口内的复用现有 authUntil,不把 strike/TTL
	// 抬满——保住短 base 的快速自愈。窗口内直接返回(不升级、不刷日志,避免爆发刷屏)。
	if !now.After(e.authUntil) {
		s.mu.Unlock()
		return
	}
	e.strike++
	e.authUntil = now.Add(s.backoffFor(e.strike))
	newlyHardDisabled := false
	if class == ClassIronClad && e.strike >= s.cfg.HardDisableStrikeK && !e.hardDisabled {
		e.hardDisabled = true
		newlyHardDisabled = true
	}
	// 快照供解锁后记日志(不持锁做 I/O)。
	snapStrike, snapUntil, snapHard, snapVer := e.strike, e.authUntil, e.hardDisabled, e.credVersion
	s.mu.Unlock()

	slog.WarnContext(ctx, "auth cooldown lane suspended account (removed from selection)",
		slog.Int64("provider_account_id", accountID),
		slog.String("class", class.label()),
		slog.Int("strike", snapStrike),
		slog.Time("auth_until", snapUntil),
		slog.Bool("hard_disabled", snapHard),
		slog.Int("credential_version", snapVer))
	if newlyHardDisabled {
		slog.WarnContext(ctx, "auth cooldown lane hard-disabled account after iron-clad strikes reached ceiling (operator resume required)",
			slog.Int64("provider_account_id", accountID),
			slog.Int("strike", snapStrike))
	}
}

// backoffFor 计算 strike 对应的封顶指数退避:min(base<<(strike-1), cap)。
// 防移位溢出:大 shift 或溢出成非正/超顶 → 一律返回 Cap。
func (s *Store) backoffFor(strike int) time.Duration {
	if strike < 1 {
		strike = 1
	}
	shift := strike - 1
	if shift >= 62 {
		return s.cfg.Cap
	}
	d := s.cfg.Base << uint(shift)
	if d <= 0 || d > s.cfg.Cap {
		return s.cfg.Cap
	}
	return d
}

// Eligible 是选号门查询:ok=false 表示此刻账号被 auth 车道移出选号。
// hardDisabled 单独返回,供逃生阀(DisableCooling)决定是否豁免——软退避可豁免、硬禁不可豁免。
// 选号热路径,高频调用:刻意不记日志(状态转换的可观测性由 Suspend/Clear 承担)。
func (s *Store) Eligible(accountID int64, now time.Time) (ok bool, hardDisabled bool) {
	if s == nil || accountID == 0 {
		return true, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[accountID]
	if e == nil {
		return true, false
	}
	if e.hardDisabled {
		return false, true
	}
	if now.Before(e.authUntil) {
		return false, false
	}
	// 已过退避窗口 → 放行,但条目(含 strike)保留:下一发 auth 失败从保留的 strike 几何升级,
	// 使真死 key 快速撞顶。条目仅在成功/轮换/运营 resume 时由 Clear 清除。
	return true, false
}

// Snapshot 返回与 Eligible 相同时间语义的只读状态，供管理诊断展示。
func (s *Store) Snapshot(accountID int64, now time.Time) Snapshot {
	if s == nil || accountID == 0 {
		return Snapshot{Eligible: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[accountID]
	if e == nil {
		return Snapshot{Eligible: true}
	}
	snap := Snapshot{
		Found:             true,
		Eligible:          !e.hardDisabled && !now.Before(e.authUntil),
		HardDisabled:      e.hardDisabled,
		Strike:            e.strike,
		CredentialVersion: e.credVersion,
	}
	if !e.authUntil.IsZero() {
		until := e.authUntil.UTC()
		snap.AuthUntil = &until
	}
	return snap
}

// Clear 彻底清除账号的车道状态(strike 归零 + AuthUntil 清 + 解除 HardDisabled)。
// 用于:一次成功请求(self-heal)、运营 resume/ForceActive。
// 仅当确有条目被清(账号此前真的在冷却)才记 InfoContext——避免每次成功请求刷屏。
func (s *Store) Clear(ctx context.Context, accountID int64, reason string) {
	if s == nil || accountID == 0 {
		return
	}
	s.mu.Lock()
	_, existed := s.entries[accountID]
	delete(s.entries, accountID)
	s.mu.Unlock()
	if existed {
		slog.InfoContext(ctx, "auth cooldown lane cleared account (eligible again)",
			slog.Int64("provider_account_id", accountID),
			slog.String("reason", reason))
	}
}

// OnRefreshResult 是凭证热刷新 worker 到车道的单向通报:
//   - permanentFailure(刷新拿到 invalid_grant/撤销)→ 即时升 HardDisabled;
//   - 其余(success / transient 失败)→ 一律不动车道状态,继续走 TTL 自愈。
//
// 刷新成功刻意不 Clear(审查 S1):RefreshHotPath 返回 nil ≠ 真的刷新了——去抖包装器
// 窗口内跳过、storm 预算拒绝、无可刷新的静态 API-key 凭证都返回 nil;把 no-op 当成功
// 会在并发 401 下毫秒级拆掉刚建立的冷却、并复活已硬禁的死号(车道自我瓦解)。
// 真恢复路径 = 退避到期回池 → 一次请求成功 → Clear(channelhealth SignalSuccess 侧)。
func (s *Store) OnRefreshResult(ctx context.Context, accountID int64, success bool, permanentFailure bool) {
	if s == nil || accountID == 0 || success || !permanentFailure {
		return
	}
	s.mu.Lock()
	e := s.entries[accountID]
	if e == nil {
		e = &entry{}
		s.entries[accountID] = e
	}
	newlyHardDisabled := !e.hardDisabled
	e.hardDisabled = true
	snapStrike := e.strike
	s.mu.Unlock()
	if newlyHardDisabled {
		slog.WarnContext(ctx, "auth cooldown lane hard-disabled account after refresh returned invalid_grant (operator resume required)",
			slog.Int64("provider_account_id", accountID),
			slog.Int("strike", snapStrike))
	}
}

// IsPermanentRefreshError 判定一次凭证热刷新错误是否代表「永久失效」(刷新拿到 invalid_grant /
// 令牌被撤销 / 授权过期),据此把账号升 HardDisabled。只匹配 OAuth 标准错误码(RFC 6749
// invalid_grant)与明确撤销/过期语义;transient(5xx/超时/限流)不命中,继续走 TTL 自愈。
func IsPermanentRefreshError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"invalid_grant",
		"invalid grant",
		"token_revoked",
		"token_invalidated",
		"refresh token revoked",
		"refresh token expired",
		"authorization expired",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
