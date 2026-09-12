package authcooldown

import (
	"context"
	"log/slog"
	"time"
)

// PersistedState 是车道真相表中单个账号的状态。AuthUntil 为零值表示没有生效中的软退避。
type PersistedState struct {
	AccountID         int64
	Strike            int
	AuthUntil         time.Time
	HardDisabled      bool
	CredentialVersion int
	// FailureClass 是最近一次写入的失败类别(ambiguous / iron_clad / refresh_permanent)。
	FailureClass string
	// LastEscalatedAt 是最近一次真实升级(越过去抖窗口)的时刻;零值表示从未升级(仅刷新硬禁)。
	LastEscalatedAt time.Time
	// HardDisabledAt 是进入硬禁的时刻;零值表示未硬禁。
	HardDisabledAt time.Time
	// ResumedAt 是运营 resume 墓碑时刻;零值表示没有墓碑。带墓碑且无其他状态的行对选号无意义。
	ResumedAt time.Time
	// NewlyHardDisabled 仅由写入操作回填:本次写入首次把账号置为硬禁。
	NewlyHardDisabled bool
}

// Persistence 是车道的跨副本真相后端。所有状态转换必须在后端原子完成(多副本并发写同一账号
// 时串行化),窗口内的重复失败不得产生写入。PostgreSQL 实现见 NewPostgresPersistence。
type Persistence interface {
	// Kind 报告后端种类(如 postgres),进管理诊断与日志。
	Kind() string
	// RecordFailure 记录一次失败并返回转换后的状态;changed=false 表示落在去抖窗口内、状态未变。
	RecordFailure(ctx context.Context, accountID int64, class FailureClass, credVersion int, now time.Time, cfg Config) (state PersistedState, changed bool, err error)
	// MarkHardDisabled 因刷新永久失效硬禁。observedCredVersion 是触发刷新的请求所用凭据版本:
	// 账号已轮换出更高版本的凭据时不得硬禁新凭据;evidenceAt 是证据产生的时刻(直写为 now,补写本地
	// 硬禁为其当初进入时刻),早于运营 resume 墓碑的证据不得写回。两种拒绝都返回 applied=false 且不改
	// 状态;observedCredVersion 为 0 表示调用方不知道版本。applied=true 时 state.NewlyHardDisabled
	// 说明是否首次硬禁。
	// failureClass 记录硬禁来源类别(refresh_permanent,或补写本地硬禁时其原始类别)。
	MarkHardDisabled(ctx context.Context, accountID int64, observedCredVersion int, failureClass string, evidenceAt, now time.Time) (state PersistedState, applied bool, err error)
	// Clear 解除账号的车道状态。hardToo=false 只清软退避(成功请求),硬禁保留;hardToo=true 连硬禁
	// 一并解除并留下 resumed_at 墓碑(运营 resume)。removed=false 表示此前没有可清除的状态。
	Clear(ctx context.Context, accountID int64, hardToo bool, now time.Time) (removed bool, err error)
	// Get 读取单个账号的状态;found=false 表示无状态。
	Get(ctx context.Context, accountID int64) (state PersistedState, found bool, err error)
	// List 列出全部有车道状态的账号(含软退避已过期但仍保留 strike 的行),以及只带 resume 墓碑的
	// 无状态行(供重载丢弃早于墓碑的本地兜底)。
	List(ctx context.Context) ([]PersistedState, error)
}

// persistTimeout 是单次持久化操作的上限。状态转换不得依赖客户端连接存活,也不能无限期拖住
// 失败切换路径,因此持久化统一使用与请求分离、带超时的上下文。
const persistTimeout = 2 * time.Second

func persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
}

// Start 启动镜像重载循环:先立即从真相表灌回一次(重启恢复),之后每 ReloadInterval 全量重载,
// 使其他副本的冷却/硬禁/解除在一个周期内进入本副本的选号门(选号候选查询本身直接读真相表,
// 镜像滞后只影响本地门与成功清除的识别)。ctx 取消后退出并关闭返回的通道。
// 未配置持久化时不启动任何 goroutine,返回已关闭的通道。
func (s *Store) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil || s.persist == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		s.Reload(ctx, time.Now())
		ticker := time.NewTicker(s.cfg.ReloadInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-ticker.C:
				s.Reload(ctx, tick)
			}
		}
	}()
	return done
}

// Reload 用真相表的全部行替换镜像,并补写此前因持久化失败只存在于本副本的硬禁:
//   - 本地硬禁条目先尝试重新写入真相表(DB 恢复后无需再等一次失败即可收敛),成功则转为普通条目;
//   - 仍写不进去的本地条目在生效期间保留(真相表里没有它们,丢掉会让本副本重新选中坏号),
//     软退避过期后丢弃,并记一条计数日志让运营知道本副本持有分叉状态;
//   - 列表读取失败时保留上次镜像并记日志。
func (s *Store) Reload(ctx context.Context, now time.Time) {
	if s == nil || s.persist == nil {
		return
	}
	s.replayLocalHardDisables(ctx, now)
	pctx, cancel := persistContext(ctx)
	defer cancel()
	rows, err := s.persist.List(pctx)
	if err != nil {
		slog.WarnContext(ctx, "auth cooldown lane reload failed; keeping previous process mirror",
			slog.String("error", err.Error()))
		return
	}
	next := make(map[int64]*entry, len(rows))
	tombstones := make(map[int64]time.Time)
	for _, row := range rows {
		if !row.ResumedAt.IsZero() {
			tombstones[row.AccountID] = row.ResumedAt
		}
		// 只有仍携带状态的行才进镜像:运营 resume 留下的墓碑与被成功清空的行对选号无意义
		//(与 List 的过滤谓词重复,作为防御保留)。
		if !row.HardDisabled && row.Strike == 0 && row.AuthUntil.IsZero() {
			continue
		}
		next[row.AccountID] = entryFromState(row, false)
	}
	retainedLocal := 0
	s.mu.Lock()
	for id, e := range s.entries {
		if !e.localOnly {
			continue
		}
		if !(e.hardDisabled || now.Before(e.authUntil)) {
			continue // 本地兜底已过期
		}
		// 他副本已在本地兜底之后 resume:本地条目(含软退避)作废,不再多挡一个退避窗口。
		if tomb, ok := tombstones[id]; ok && !localEvidenceAt(e, now).After(tomb) {
			continue
		}
		if persisted, ok := next[id]; ok {
			// 真相行存在但比本地兜底更弱(未硬禁 / 截止更早,例如写失败前的旧状态):本地兜底继续生效。
			stronger := (e.hardDisabled && !persisted.hardDisabled) ||
				(!persisted.hardDisabled && e.authUntil.After(persisted.authUntil))
			if !stronger {
				continue
			}
		}
		next[id] = e
		retainedLocal++
	}
	s.entries = next
	s.mu.Unlock()
	if retainedLocal > 0 {
		slog.WarnContext(ctx, "auth cooldown lane mirror still holds replica-local state that could not be persisted",
			slog.Int("local_only_entries", retainedLocal))
	}
}

// replayLocalHardDisables 把持久化失败遗留的本地硬禁重新写入真相表。软退避不重放:
// 它们有界过期,重放会在后端语义下被当作新的失败升级。
func (s *Store) replayLocalHardDisables(ctx context.Context, now time.Time) {
	type localHard struct {
		version    int
		class      string
		evidenceAt time.Time
	}
	s.mu.Lock()
	pending := make(map[int64]localHard, 0)
	for id, e := range s.entries {
		if e.localOnly && e.hardDisabled {
			class := e.failureClass
			if class == "" {
				class = failureClassRefreshPermanent
			}
			pending[id] = localHard{version: e.credVersion, class: class, evidenceAt: localEvidenceAt(e, now)}
		}
	}
	s.mu.Unlock()
	for id, local := range pending {
		pctx, cancel := persistContext(ctx)
		state, applied, err := s.persist.MarkHardDisabled(pctx, id, local.version, local.class, local.evidenceAt, now)
		cancel()
		if err != nil {
			continue
		}
		if applied {
			s.storeMirror(id, state, false)
			slog.InfoContext(ctx, "auth cooldown lane replayed replica-local hard disable into shared truth",
				slog.Int64("provider_account_id", id))
			continue
		}
		// 真相表明确拒绝(账号已轮换出更新凭据,或运营已在本地硬禁之后 resume):本地硬禁已过时,移出镜像。
		s.mu.Lock()
		delete(s.entries, id)
		s.mu.Unlock()
		slog.InfoContext(ctx, "auth cooldown lane dropped replica-local hard disable superseded by shared truth",
			slog.Int64("provider_account_id", id))
	}
}

// localEvidenceAt 是本地条目所代表证据的产生时刻:硬禁取进入硬禁时刻,软退避取最近升级时刻;
// 都未知时取 now(最保守:视为最新证据)。
func localEvidenceAt(e *entry, now time.Time) time.Time {
	switch {
	case e.hardDisabled && !e.hardDisabledAt.IsZero():
		return e.hardDisabledAt
	case !e.lastEscalatedAt.IsZero():
		return e.lastEscalatedAt
	default:
		return now
	}
}

func entryFromState(state PersistedState, localOnly bool) *entry {
	return &entry{
		strike:          state.Strike,
		authUntil:       state.AuthUntil,
		hardDisabled:    state.HardDisabled,
		credVersion:     state.CredentialVersion,
		failureClass:    state.FailureClass,
		lastEscalatedAt: state.LastEscalatedAt,
		hardDisabledAt:  state.HardDisabledAt,
		localOnly:       localOnly,
	}
}
