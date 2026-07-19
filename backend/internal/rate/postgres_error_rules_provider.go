package rate

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	accountErrorPolicyQueryTimeout = 500 * time.Millisecond
	accountErrorPolicyFreshTTL     = 5 * time.Second
	accountErrorPolicyStaleTTL     = time.Minute
	accountErrorPolicyErrorBackoff = 5 * time.Second
	accountErrorPolicyCacheLimit   = 4096
)

type accountErrorPolicyQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// postgresAccountErrorRulesProvider 从 provider_accounts 获取账号错误策略。
// 新鲜缓存与同账号并发合并避免上游故障把数据库查询放大；依赖失败时只在有界
// 时间内返回陈旧策略，超过上限后 fail-open 为零策略。
type postgresAccountErrorRulesProvider struct {
	queryer accountErrorPolicyQueryer
	now     func() time.Time
	logger  *slog.Logger

	cacheMu sync.RWMutex
	cache   map[int64]accountErrorPolicyCacheEntry
	loads   singleflight.Group
}

type accountErrorPolicyCacheEntry struct {
	policy     AccountErrorPolicy
	freshUntil time.Time
	staleUntil time.Time
}

// NewPostgresAccountErrorRulesProvider 返回一个由 Postgres 支撑的
// AccountErrorRulesProvider。queryer 必须非 nil；生产传连接池，集成测试可传事务。
func NewPostgresAccountErrorRulesProvider(queryer accountErrorPolicyQueryer) AccountErrorRulesProvider {
	return newPostgresAccountErrorRulesProvider(queryer, time.Now, slog.Default())
}

func newPostgresAccountErrorRulesProvider(queryer accountErrorPolicyQueryer, now func() time.Time, logger *slog.Logger) *postgresAccountErrorRulesProvider {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &postgresAccountErrorRulesProvider{
		queryer: queryer,
		now:     now,
		logger:  logger,
		cache:   make(map[int64]accountErrorPolicyCacheEntry),
	}
}

// GetAccountErrorPolicy 实现 AccountErrorRulesProvider。
// 它应用两个 enable 标志:
//   - 当 temp_unschedulable_enabled = false 时返回空 rules
//   - 当 custom_error_codes_enabled = false 时返回空 codes
func (p *postgresAccountErrorRulesProvider) GetAccountErrorPolicy(accountID int64) AccountErrorPolicy {
	if p == nil || p.queryer == nil || accountID <= 0 {
		return AccountErrorPolicy{}
	}
	now := p.currentTime()
	if policy, ok := p.freshPolicy(accountID, now); ok {
		return policy
	}

	value, _, _ := p.loads.Do(strconv.FormatInt(accountID, 10), func() (any, error) {
		now := p.currentTime()
		if policy, ok := p.freshPolicy(accountID, now); ok {
			return policy, nil
		}
		policy, err := p.loadPolicy(accountID)
		if err == nil {
			p.storePolicy(accountID, policy, now.Add(accountErrorPolicyFreshTTL), now.Add(accountErrorPolicyStaleTTL), now)
			return cloneAccountErrorPolicy(policy), nil
		}

		stale, servedStale := p.stalePolicy(accountID, now)
		p.logLoadFailure(accountID, err, servedStale)
		if servedStale {
			p.deferRefresh(accountID, now.Add(accountErrorPolicyErrorBackoff))
			return stale, nil
		}
		// 没有陈旧值时短时缓存零策略，避免数据库故障期间每个请求都再次查询。
		zero := AccountErrorPolicy{}
		until := now.Add(accountErrorPolicyErrorBackoff)
		p.storePolicy(accountID, zero, until, until, now)
		return zero, nil
	})
	policy, _ := value.(AccountErrorPolicy)
	return cloneAccountErrorPolicy(policy)
}

func (p *postgresAccountErrorRulesProvider) loadPolicy(accountID int64) (AccountErrorPolicy, error) {
	ctx, cancel := context.WithTimeout(context.Background(), accountErrorPolicyQueryTimeout)
	defer cancel()

	row := p.queryer.QueryRow(ctx,
		`SELECT temp_unschedulable_enabled, temp_unschedulable_rules,
			        custom_error_codes_enabled, custom_error_codes, pool_mode
			   FROM provider_accounts
			  WHERE id = $1
			    AND deleted_at IS NULL`,
		accountID,
	)

	var (
		tempEnabled   bool
		rulesRaw      []byte
		customEnabled bool
		customCodes   []int32
		poolMode      bool
	)
	if err := row.Scan(&tempEnabled, &rulesRaw, &customEnabled, &customCodes, &poolMode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AccountErrorPolicy{}, nil
		}
		return AccountErrorPolicy{}, err
	}

	var rules []TempUnschedulableRule
	if tempEnabled {
		rules = ParseTempUnschedulableRules(rulesRaw)
	}

	var effectiveCodes []int32
	if customEnabled {
		effectiveCodes = customCodes
	}

	return AccountErrorPolicy{Rules: rules, CustomErrorCodes: effectiveCodes, PoolMode: poolMode}, nil
}

func (p *postgresAccountErrorRulesProvider) currentTime() time.Time {
	if p == nil || p.now == nil {
		return time.Now().UTC()
	}
	return p.now().UTC()
}

func (p *postgresAccountErrorRulesProvider) freshPolicy(accountID int64, now time.Time) (AccountErrorPolicy, bool) {
	p.cacheMu.RLock()
	entry, ok := p.cache[accountID]
	p.cacheMu.RUnlock()
	if !ok || !now.Before(entry.freshUntil) {
		return AccountErrorPolicy{}, false
	}
	return cloneAccountErrorPolicy(entry.policy), true
}

func (p *postgresAccountErrorRulesProvider) stalePolicy(accountID int64, now time.Time) (AccountErrorPolicy, bool) {
	p.cacheMu.RLock()
	entry, ok := p.cache[accountID]
	p.cacheMu.RUnlock()
	if !ok || !now.Before(entry.staleUntil) {
		return AccountErrorPolicy{}, false
	}
	return cloneAccountErrorPolicy(entry.policy), true
}

func (p *postgresAccountErrorRulesProvider) deferRefresh(accountID int64, freshUntil time.Time) {
	p.cacheMu.Lock()
	entry, ok := p.cache[accountID]
	if ok && freshUntil.Before(entry.staleUntil) {
		entry.freshUntil = freshUntil
		p.cache[accountID] = entry
	}
	p.cacheMu.Unlock()
}

func (p *postgresAccountErrorRulesProvider) storePolicy(accountID int64, policy AccountErrorPolicy, freshUntil, staleUntil, now time.Time) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if _, exists := p.cache[accountID]; !exists && len(p.cache) >= accountErrorPolicyCacheLimit {
		for candidate, entry := range p.cache {
			if !now.Before(entry.staleUntil) {
				delete(p.cache, candidate)
			}
		}
		if len(p.cache) >= accountErrorPolicyCacheLimit {
			for candidate := range p.cache {
				delete(p.cache, candidate)
				break
			}
		}
	}
	p.cache[accountID] = accountErrorPolicyCacheEntry{
		policy: cloneAccountErrorPolicy(policy), freshUntil: freshUntil, staleUntil: staleUntil,
	}
}

func (p *postgresAccountErrorRulesProvider) logLoadFailure(accountID int64, err error, servedStale bool) {
	if p == nil || p.logger == nil || err == nil {
		return
	}
	ctx := context.Background()
	p.logger.WarnContext(ctx, "读取账号错误策略失败，已进入短时降级",
		logcontract.FieldCategory, string(logcontract.CategoryError),
		logcontract.FieldEventType, "upstream_error_policy.load_failed",
		logcontract.FieldResult, string(logcontract.ResultServerFailure),
		logcontract.FieldErrorClass, string(logcontract.ErrorDependency),
		logcontract.FieldErrorCode, "account_error_policy_load_failed",
		logcontract.FieldRetryable, true,
		logcontract.FieldActorKind, string(logcontract.ActorSystem),
		logcontract.FieldActorRef, "gateway",
		logcontract.FieldTargetType, "provider_account",
		logcontract.FieldTargetRef, strconv.FormatInt(accountID, 10),
		"dependency_error_class", privacy.ErrorClassFor(ctx, err),
		"served_stale_policy", servedStale,
	)
}

func cloneAccountErrorPolicy(in AccountErrorPolicy) AccountErrorPolicy {
	out := AccountErrorPolicy{PoolMode: in.PoolMode}
	out.CustomErrorCodes = append([]int32(nil), in.CustomErrorCodes...)
	out.Rules = make([]TempUnschedulableRule, len(in.Rules))
	for i := range in.Rules {
		out.Rules[i] = in.Rules[i]
		out.Rules[i].Keywords = append([]string(nil), in.Rules[i].Keywords...)
		if in.Rules[i].ClientStatus != nil {
			value := *in.Rules[i].ClientStatus
			out.Rules[i].ClientStatus = &value
		}
		if in.Rules[i].AffectHealth != nil {
			value := *in.Rules[i].AffectHealth
			out.Rules[i].AffectHealth = &value
		}
	}
	return out
}
