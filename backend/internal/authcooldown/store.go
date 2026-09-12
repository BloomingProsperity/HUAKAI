// 包 authcooldown 实现「auth 失败降级车道」——独立于渠道健康分/错误率 FSM 的一条轻量车道。
//
// 背景:坏 key 账号在 auth 失败(401 / Grok 400-auth)后若既不 failover 也不冷却,会恒占池首优先级,
// 每个请求首选它、吃一发 401;auth-failover 子预算硬限一次,≥2 个坏号即请求直接 401 给客户端——
// 坏号黑洞整个模型流量。
//
// 设计要点:
//   - 架构:独立 auth 车道,不写健康 State/Score、不进 error-rate/ban-ramp 窗口(auth blip 不污染健康分);
//     跨副本真相是 provider_account_auth_cooldowns 表(Persistence),选号候选查询直接排除阻断账号;
//     进程内镜像只承担选号门的本地即时性与持久化写失败时的兜底保护,并定时从真相重载;
//   - 算法:封顶指数退避(base<<(strike-1),cap 封顶)替代定长冷却——常态 token 过期几秒热刷新自愈、
//     真死 key 几何增长快速止损;iron-clad 达 strike 上限升 HardDisabled,ambiguous 通用 401 永不永久禁;
//   - 恢复分档:成功请求只解除软退避(strike 归零);硬禁只能由运营 resume / ForceActive 或显式凭据
//     轮换解除,避免在途请求的迟到成功复活刚被确认永久失效的账号;
//   - 生态:凭证热刷新 worker 单向通报车道(OnRefreshResult:仅永久失效→硬禁,且带触发刷新时的凭据
//     版本,账号已轮换出更新凭据时不硬禁新凭据)+ 结构化日志把「坏号被冷却/恢复」变运营可见。
//     刷新成功刻意不解除冷却(见 OnRefreshResult 注释)。
//
// 未配置 Persistence 时(单测、显式降级)车道退化为纯进程内,行为与镜像语义逐字节一致。
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

// failureClassRefreshPermanent 是刷新永久失效写入的类别标签(与真相表 CHECK 一致)。
const failureClassRefreshPermanent = "refresh_permanent"

// Clear 的原因标签,进日志供运营区分账号是如何解除冷却的。
const (
	ClearReasonSuccess        = "request_success" // 一次成功请求(self-heal,只清软退避)
	ClearReasonOperatorResume = "operator_resume" // 运营 ForceActive/ManualResume(连硬禁一并清)
)

// Config 是退避与升级参数。零值经 normalized() 补默认。
type Config struct {
	// Base 是首次退避基数;短基数让常态 token 过期被热刷新几秒修好、自愈快。
	Base time.Duration
	// Cap 是退避封顶;几何增长撞顶后稳定在此,真死 key 每 Cap 才重试一发、不再黑洞。
	Cap time.Duration
	// HardDisableStrikeK 是 iron-clad 类升 HardDisabled 的 strike 阈值。
	HardDisableStrikeK int
	// ReloadInterval 是进程镜像从持久化真相全量重载的周期(仅配置 Persistence 时生效)。
	ReloadInterval time.Duration
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
	if c.ReloadInterval <= 0 {
		c.ReloadInterval = 5 * time.Second
	}
	return c
}

// entry 是单个账号在进程镜像中的车道状态。主键为 ProviderAccountID(active 凭证 1:1)。
type entry struct {
	strike          int       // 连续 auth 失败计数(经窗口去抖,每个退避窗口至多 +1)
	authUntil       time.Time // 此刻之前不合格(临时移出选号)
	hardDisabled    bool      // 硬禁用:iron-clad 达 strike 上限 或 热刷新拿到 invalid_grant
	credVersion     int       // 版本感知:凭证轮换(版本前进)→ 视作全新账号重置 strike
	failureClass    string    // 最近一次失败类别
	lastEscalatedAt time.Time // 最近一次真实升级时刻
	hardDisabledAt  time.Time // 进入硬禁的时刻
	// localOnly 表示该状态因持久化写失败只存在于本进程;重载时保留到过期或被成功写覆盖,
	// 硬禁条目会在重载时尝试补写回真相表。
	localOnly bool
}

// Store 是 auth 降级车道。所有方法并发安全(单 mutex 保护镜像,最后写入语义明确)。
type Store struct {
	mu      sync.Mutex
	entries map[int64]*entry
	cfg     Config
	persist Persistence
}

// Snapshot 是单个账号当前 auth 降级状态的只读副本。
// AuthUntil 过期后 Eligible 会恢复为 true,但 strike 会保留到成功、轮换或人工恢复。
type Snapshot struct {
	Found             bool
	Eligible          bool
	HardDisabled      bool
	Strike            int
	AuthUntil         *time.Time
	CredentialVersion int
	// FailureClass 是最近一次失败类别(ambiguous / iron_clad / refresh_permanent),无状态时为空。
	FailureClass string
	// LastEscalatedAt 是最近一次真实升级时刻;HardDisabledAt 是进入硬禁的时刻。
	LastEscalatedAt *time.Time
	HardDisabledAt  *time.Time
	// Persistence 报告快照来源的真相种类(postgres / process_local),供管理诊断展示。
	Persistence string
}

// NewStore 构造纯进程内车道。零值 Config → 默认(base=30s、cap=30min、K=3)。
func NewStore(cfg Config) *Store {
	return &Store{
		entries: make(map[int64]*entry),
		cfg:     cfg.normalized(),
	}
}

// NewPersistentStore 构造以 Persistence 为跨副本真相的车道;persist 为 nil 时等价 NewStore。
func NewPersistentStore(cfg Config, persist Persistence) *Store {
	s := NewStore(cfg)
	s.persist = persist
	return s
}

// PersistenceKind 报告车道真相所在:配置了持久化后端时返回其种类,否则为 process_local。
func (s *Store) PersistenceKind() string {
	if s == nil || s.persist == nil {
		return "process_local"
	}
	return s.persist.Kind()
}

// Suspend 记录一次 auth 失败:升级 strike、算 AuthUntil、按分级决定是否 HardDisabled。
// class=iron-clad 且 strike 达上限 K → HardDisabled;ambiguous 永不 HardDisabled。
// 每次真实升级(越过去抖窗口)记一条 WarnContext,HardDisabled 升级另记一条(可用性事件)。
// 配置了持久化时先写真相表,再用返回状态刷新镜像;写失败则退回进程内转换并记日志,
// 保证本副本仍然把坏号移出选号。本副本镜像已知仍在窗口内且版本未前进的重复失败直接短路,
// 401 风暴下不再为每次失败付一次数据库往返。
func (s *Store) Suspend(ctx context.Context, accountID int64, class FailureClass, credVersion int, now time.Time) {
	if s == nil || accountID == 0 {
		return
	}
	if s.persist != nil {
		if s.withinLocalWindow(accountID, credVersion, now) {
			return
		}
		pctx, cancel := persistContext(ctx)
		state, changed, err := s.persist.RecordFailure(pctx, accountID, class, credVersion, now, s.cfg)
		cancel()
		if err == nil {
			if changed {
				s.storeMirror(accountID, state, false)
				s.logSuspended(ctx, accountID, class, state, state.NewlyHardDisabled)
			}
			return
		}
		s.logPersistFailure(ctx, "suspend", accountID, err)
	}
	if state, escalated := s.suspendLocal(accountID, class, credVersion, now, s.persist != nil); escalated {
		s.logSuspended(ctx, accountID, class, state, state.NewlyHardDisabled)
	}
}

// withinLocalWindow 判断镜像是否已知该账号仍在退避窗口内(且本次事件不是版本前进):
// 此时真相表侧也必然不会升级,可直接短路。本地兜底(localOnly)条目不代表真相表状态,不短路。
func (s *Store) withinLocalWindow(accountID int64, credVersion int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[accountID]
	if e == nil || e.localOnly {
		return false
	}
	if credVersion > 0 && e.credVersion > 0 && credVersion > e.credVersion {
		return false
	}
	// 已硬禁的账号再失败不会改变任何选号结果(硬禁只增不减),同样短路。
	return e.hardDisabled || !now.After(e.authUntil)
}

// suspendLocal 在进程镜像上执行车道转换,返回转换后的状态与是否真实升级。
func (s *Store) suspendLocal(accountID int64, class FailureClass, credVersion int, now time.Time, localOnly bool) (PersistedState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[accountID]
	if e == nil {
		e = &entry{credVersion: credVersion}
		s.entries[accountID] = e
	}
	// 版本感知 strike-reset:凭证已前进到「更新」的版本 → 旧的软退避历史作废。
	// 只认版本前进(>):迟到的旧版本事件(长流式在途请求携带轮换前的 credVersion)不得
	// 反向重置新版本已积累的 strike,也不得把记录的版本退回去。
	// 版本前进只重置 strike/软退避;硬禁不因此解除(硬禁只由运营 resume / 显式轮换解除,
	// 刷新成功抬升的版本不得借一次失败把硬禁降回软退避)。
	if credVersion > 0 && e.credVersion > 0 && credVersion > e.credVersion {
		e.strike = 0
		e.authUntil = time.Time{}
	}
	// 迟到的旧版本失败(双方版本都已知且本次更低)属于已轮换掉的凭据,对新版本不构成证据。
	if credVersion > 0 && e.credVersion > 0 && credVersion < e.credVersion {
		return PersistedState{}, false
	}
	if credVersion > e.credVersion {
		e.credVersion = credVersion
	}
	// 并发去抖:仅当已越过上次退避窗口(now 严格晚于 authUntil)才升级 strike/TTL。并发爆发
	//(同一坏号瞬时多发 401)只在第一发升级,其余落在窗口内的复用现有 authUntil,不把 strike/TTL
	// 抬满——保住短 base 的快速自愈。窗口内直接返回(不升级、不刷日志,避免爆发刷屏)。
	if !now.After(e.authUntil) {
		return PersistedState{}, false
	}
	e.strike++
	e.authUntil = now.Add(s.backoffFor(e.strike))
	e.failureClass = class.label()
	e.lastEscalatedAt = now
	newlyHardDisabled := false
	if class == ClassIronClad && e.strike >= s.cfg.HardDisableStrikeK && !e.hardDisabled {
		e.hardDisabled = true
		e.hardDisabledAt = now
		newlyHardDisabled = true
	}
	e.localOnly = localOnly
	state := stateFromEntry(accountID, e)
	state.NewlyHardDisabled = newlyHardDisabled
	return state, true
}

func stateFromEntry(accountID int64, e *entry) PersistedState {
	return PersistedState{
		AccountID:         accountID,
		Strike:            e.strike,
		AuthUntil:         e.authUntil,
		HardDisabled:      e.hardDisabled,
		CredentialVersion: e.credVersion,
		FailureClass:      e.failureClass,
		LastEscalatedAt:   e.lastEscalatedAt,
		HardDisabledAt:    e.hardDisabledAt,
	}
}

func (s *Store) logSuspended(ctx context.Context, accountID int64, class FailureClass, state PersistedState, newlyHardDisabled bool) {
	slog.WarnContext(ctx, "auth cooldown lane suspended account (removed from selection)",
		slog.Int64("provider_account_id", accountID),
		slog.String("class", class.label()),
		slog.Int("strike", state.Strike),
		slog.Time("auth_until", state.AuthUntil),
		slog.Bool("hard_disabled", state.HardDisabled),
		slog.Int("credential_version", state.CredentialVersion),
		slog.String("persistence", s.PersistenceKind()))
	if newlyHardDisabled {
		slog.WarnContext(ctx, "auth cooldown lane hard-disabled account after iron-clad strikes reached ceiling (operator resume required)",
			slog.Int64("provider_account_id", accountID),
			slog.Int("strike", state.Strike))
	}
}

func (s *Store) logPersistFailure(ctx context.Context, op string, accountID int64, err error) {
	slog.ErrorContext(ctx, "auth cooldown lane persistence failed; falling back to process-local state for this replica",
		slog.String("operation", op),
		slog.Int64("provider_account_id", accountID),
		slog.String("error", err.Error()))
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
// 选号热路径,高频调用:只读进程镜像、不碰持久化,也不记日志(状态转换的可观测性由 Suspend/Clear 承担)。
// 跨副本的阻断由选号候选查询直接按真相表排除;镜像通过定时重载收敛其他副本写入的状态。
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

// Snapshot 返回与 Eligible 相同时间语义的只读状态,供管理诊断展示。
// 配置了持久化时读真相表(跨副本一致);读失败或行不存在时回退到进程镜像,
// 使持久化写失败后仅存在于本副本的状态仍对运营可见。
func (s *Store) Snapshot(ctx context.Context, accountID int64, now time.Time) Snapshot {
	if s == nil || accountID == 0 {
		return Snapshot{Eligible: true, Persistence: s.PersistenceKind()}
	}
	kind := s.PersistenceKind()
	truthUnavailable := false
	if s.persist != nil {
		pctx, cancel := persistContext(ctx)
		state, found, err := s.persist.Get(pctx, accountID)
		cancel()
		if err != nil {
			s.logPersistFailure(ctx, "snapshot", accountID, err)
			truthUnavailable = true
		} else if found {
			return snapshotFromState(state, now, kind)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[accountID]
	if e == nil {
		return Snapshot{Eligible: true, Persistence: kind}
	}
	// 真相明确无状态时,镜像里来自真相的旧条目只是尚未重载的陈旧副本,不当作状态展示;
	// 仅本副本持有的兜底条目仍要暴露。真相读不到时,镜像是唯一可用信息,标为本进程来源。
	if s.persist != nil && !e.localOnly && !truthUnavailable {
		return Snapshot{Eligible: true, Persistence: kind}
	}
	if e.localOnly || truthUnavailable {
		kind = "process_local"
	}
	return snapshotFromState(stateFromEntry(accountID, e), now, kind)
}

func snapshotFromState(state PersistedState, now time.Time, kind string) Snapshot {
	snap := Snapshot{
		Found:             true,
		Eligible:          !state.HardDisabled && !now.Before(state.AuthUntil),
		HardDisabled:      state.HardDisabled,
		Strike:            state.Strike,
		CredentialVersion: state.CredentialVersion,
		FailureClass:      state.FailureClass,
		Persistence:       kind,
	}
	snap.AuthUntil = optionalUTC(state.AuthUntil)
	snap.LastEscalatedAt = optionalUTC(state.LastEscalatedAt)
	snap.HardDisabledAt = optionalUTC(state.HardDisabledAt)
	return snap
}

func optionalUTC(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// Clear 解除账号的车道状态,按原因分档:
//   - ClearReasonSuccess(一次成功请求):只清软退避(strike 归零、AuthUntil 清),硬禁保留——
//     硬禁只能由运营或显式凭据轮换解除,否则在途请求的迟到成功会复活刚被确认永久失效的账号;
//   - 其他原因(运营 resume/ForceActive):连硬禁一并清除。
//
// 配置了持久化时删除真相行,使其他副本的候选查询与镜像重载同步解除;删除失败只记日志
// (行仍在 → 候选查询继续排除,属 fail-closed,运营可重试 resume)。
// 仅当确有条目被清(账号此前真的在冷却)才记 InfoContext——避免每次成功请求刷屏。
func (s *Store) Clear(ctx context.Context, accountID int64, reason string) {
	if s == nil || accountID == 0 {
		return
	}
	hardToo := reason != ClearReasonSuccess
	s.mu.Lock()
	e, local := s.entries[accountID]
	if local && (hardToo || !e.hardDisabled) {
		delete(s.entries, accountID)
	} else {
		local = false
	}
	s.mu.Unlock()
	// 每次成功请求都会走到这里:只有镜像里确有可清条目(本副本写过或重载时从真相表看到过)才向
	// 真相表发写语句,避免为每次成功请求付一条写。真相表里刚由其他副本写入、本副本尚未重载到
	// 的软退避行会保留到自身过期(最长一个退避窗口),这是有意接受的有界代价。
	// 运营 resume 不走热路径且必须立即跨副本生效,无论镜像是否知情都直接写真相表(留下墓碑)。
	// 只在确有状态被解除时记日志:纯进程内车道以镜像为准;配置了持久化时以真相表删除结果为准,
	// 删除失败或行本就不存在(例如成功请求遇到硬禁行)都不记"已恢复",避免误导运营。
	existed := local && s.persist == nil
	if s.persist != nil && (local || hardToo) {
		pctx, cancel := persistContext(ctx)
		removed, err := s.persist.Clear(pctx, accountID, hardToo, time.Now())
		cancel()
		if err != nil {
			s.logPersistFailure(ctx, "clear", accountID, err)
		}
		existed = removed
	}
	if existed {
		slog.InfoContext(ctx, "auth cooldown lane cleared account (eligible again)",
			slog.Int64("provider_account_id", accountID),
			slog.String("reason", reason),
			slog.Bool("hard_disable_cleared", hardToo))
	}
}

// OnRefreshResult 是凭证热刷新 worker 到车道的单向通报:
//   - permanentFailure(刷新拿到 invalid_grant/撤销)→ 即时升 HardDisabled;
//   - 其余(success / transient 失败)→ 一律不动车道状态,继续走 TTL 自愈。
//
// observedCredVersion 是触发刷新的请求所使用的凭据版本(0 = 未知):账号在此期间已轮换出更高版本
// 的可服务凭据时,这次永久失效属于旧凭据,真相表侧拒绝写入,本地也不硬禁——迟到的旧凭据结论
// 不得禁掉刚重新授权的新凭据。
//
// 刷新成功刻意不 Clear:RefreshHotPath 返回 nil ≠ 真的刷新了——去抖包装器窗口内跳过、
// storm 预算拒绝、无可刷新的静态 API-key 凭证都返回 nil;把 no-op 当成功会在并发 401 下
// 毫秒级拆掉刚建立的冷却、并复活已硬禁的死号(车道自我瓦解)。
// 真恢复路径 = 退避到期回池 → 一次请求成功 → Clear 软退避(channelhealth SignalSuccess 侧)。
func (s *Store) OnRefreshResult(ctx context.Context, accountID int64, observedCredVersion int, success bool, permanentFailure bool) {
	if s == nil || accountID == 0 || success || !permanentFailure {
		return
	}
	now := time.Now()
	if s.persist != nil {
		pctx, cancel := persistContext(ctx)
		state, applied, err := s.persist.MarkHardDisabled(pctx, accountID, observedCredVersion, failureClassRefreshPermanent, now, now)
		cancel()
		if err == nil {
			if !applied {
				// 真相表判定这次结论属于已被轮换掉的旧凭据(或早于运营 resume 墓碑):不硬禁;
				// 若本地兜底条目也是旧版本,一并作废。
				s.dropIfStale(accountID, observedCredVersion)
				return
			}
			s.storeMirror(accountID, state, false)
			if state.NewlyHardDisabled {
				s.logRefreshHardDisabled(ctx, accountID, state.Strike)
			}
			return
		}
		s.logPersistFailure(ctx, "refresh_hard_disable", accountID, err)
	}
	s.mu.Lock()
	e := s.entries[accountID]
	if e == nil {
		e = &entry{}
		s.entries[accountID] = e
	}
	// 本地也按版本守卫:镜像里已有更新版本的条目时,旧版本的刷新失败不硬禁。
	if observedCredVersion > 0 && e.credVersion > observedCredVersion {
		s.mu.Unlock()
		return
	}
	newlyHardDisabled := !e.hardDisabled
	e.hardDisabled = true
	if e.hardDisabledAt.IsZero() {
		e.hardDisabledAt = now
	}
	e.failureClass = failureClassRefreshPermanent
	if observedCredVersion > e.credVersion {
		e.credVersion = observedCredVersion
	}
	e.localOnly = s.persist != nil
	snapStrike := e.strike
	s.mu.Unlock()
	if newlyHardDisabled {
		s.logRefreshHardDisabled(ctx, accountID, snapStrike)
	}
}

// dropIfStale 删除镜像中仅本副本持有、且版本不高于 observedCredVersion 的兜底条目(真相表已判定其过时)。
// 来自真相表的条目不在这里删:它们由重载按真相收敛,避免在真相行仍存在时制造分叉。
func (s *Store) dropIfStale(accountID int64, observedCredVersion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[accountID]; e != nil && e.localOnly && e.credVersion <= observedCredVersion {
		delete(s.entries, accountID)
	}
}

func (s *Store) logRefreshHardDisabled(ctx context.Context, accountID int64, strike int) {
	slog.WarnContext(ctx, "auth cooldown lane hard-disabled account after refresh returned invalid_grant (operator resume required)",
		slog.Int64("provider_account_id", accountID),
		slog.Int("strike", strike),
		slog.String("persistence", s.PersistenceKind()))
}

// storeMirror 用持久化真相返回的状态覆盖镜像条目。
func (s *Store) storeMirror(accountID int64, state PersistedState, localOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[accountID] = entryFromState(state, localOnly)
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
