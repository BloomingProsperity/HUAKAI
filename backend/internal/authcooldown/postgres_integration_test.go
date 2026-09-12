//go:build integration_pg

package authcooldown

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
)

// laneFixture 是车道集成测试的最小账号图谱:一个租户、一个 provider、一个池、一个渠道、若干账号。
type laneFixture struct {
	tenantID int64
	accounts []int64
}

func openLanePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func seedLaneFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accounts int) laneFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var fx laneFixture
	scan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql[:40], err)
		}
	}
	scan(&fx.tenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "auth-lane-"+suffix)
	var providerID, poolID, channelID int64
	scan(&providerID, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		VALUES ($1, $2, 'Auth Lane Provider', 'openai_chat') RETURNING id`, fx.tenantID, "al-"+suffix)
	scan(&poolID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.tenantID, "al-pool-"+suffix)
	scan(&channelID, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, fx.tenantID, poolID, "al-chan-"+suffix)
	for i := 0; i < accounts; i++ {
		var id int64
		scan(&id, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, enabled, health_state)
			VALUES ($1, $2, $3, $4, 'api_key', true, 'healthy') RETURNING id`,
			fx.tenantID, providerID, channelID, fmt.Sprintf("al-%s-%d", suffix, i))
		fx.accounts = append(fx.accounts, id)
	}
	t.Cleanup(func() {
		// 清理带上限:测试失败路径若仍有事务未释放,不能让清理无限期等锁。
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM provider_account_auth_cooldowns WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id = $1`, fx.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, fx.tenantID)
	})
	return fx
}

func laneCfg() Config {
	return Config{Base: 30 * time.Second, Cap: 4 * time.Minute, HardDisableStrikeK: 3, ReloadInterval: time.Hour}
}

// TestPostgresPersistenceEscalationMatchesProcessLane:真相表上的升级序列与进程内车道逐条一致:
// 首次失败 strike=1/+30s;窗口内再失败不写(changed=false,行不变);越过窗口 +60s、+120s;
// iron-clad 第 3 次硬禁且 newly 只报一次;已硬禁的行对请求失败零写入(含版本前进);歧义类永不硬禁;
// 软退避账号上版本前进重置为 strike=1,迟到的旧版本既不升级也不把版本退回。
// 变异守卫:去掉 DO UPDATE 的 WHERE 去抖 → 窗口内 strike 变 2;把 GREATEST 版本改成直接覆盖 →
// 旧版本事件后再来新版本会被误判为轮换;任一都让本测试变红。
func TestPostgresPersistenceEscalationMatchesProcessLane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 2)
	p := NewPostgresPersistence(pool)
	cfg := laneCfg()
	now := time.Now().UTC().Truncate(time.Microsecond)
	iron, amb := fx.accounts[0], fx.accounts[1]

	st, changed, err := p.RecordFailure(ctx, iron, ClassIronClad, 1, now, cfg)
	if err != nil || !changed || st.Strike != 1 || !st.AuthUntil.Equal(now.Add(30*time.Second)) || st.HardDisabled || st.NewlyHardDisabled {
		t.Fatalf("首次失败: st=%+v changed=%v err=%v", st, changed, err)
	}
	if _, changed, err := p.RecordFailure(ctx, iron, ClassIronClad, 1, now.Add(10*time.Second), cfg); err != nil || changed {
		t.Fatalf("窗口内失败不得升级: changed=%v err=%v", changed, err)
	}
	if got, found, err := p.Get(ctx, iron); err != nil || !found || got.Strike != 1 || !got.AuthUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("窗口内失败后行必须不变: %+v found=%v err=%v", got, found, err)
	}
	now2 := now.Add(31 * time.Second)
	st, changed, err = p.RecordFailure(ctx, iron, ClassIronClad, 1, now2, cfg)
	if err != nil || !changed || st.Strike != 2 || !st.AuthUntil.Equal(now2.Add(60*time.Second)) || st.HardDisabled {
		t.Fatalf("第二次升级: st=%+v changed=%v err=%v", st, changed, err)
	}
	now3 := now2.Add(61 * time.Second)
	st, changed, err = p.RecordFailure(ctx, iron, ClassIronClad, 1, now3, cfg)
	if err != nil || !changed || st.Strike != 3 || !st.AuthUntil.Equal(now3.Add(120*time.Second)) || !st.HardDisabled || !st.NewlyHardDisabled {
		t.Fatalf("第三次 iron-clad 必须硬禁且 newly=true: st=%+v changed=%v err=%v", st, changed, err)
	}
	// 已硬禁的行不再被请求失败改写(硬禁只增不减,strike/截止对选号已无意义):第四次失败 no rows、行不变。
	now4 := now3.Add(121 * time.Second)
	if _, changed, err := p.RecordFailure(ctx, iron, ClassIronClad, 1, now4, cfg); err != nil || changed {
		t.Fatalf("硬禁行不得被请求失败改写: changed=%v err=%v", changed, err)
	}
	if got, _, _ := p.Get(ctx, iron); got.Strike != 3 || !got.HardDisabled || !got.HardDisabledAt.Equal(now3) {
		t.Fatalf("硬禁行必须保持原样: %+v", got)
	}
	// 版本前进也不改写硬禁行(刷新成功抬升的版本不得借一次失败把硬禁降回软退避)。
	if _, changed, err := p.RecordFailure(ctx, iron, ClassIronClad, 2, now4, cfg); err != nil || changed {
		t.Fatalf("版本前进不得改写硬禁行: changed=%v err=%v", changed, err)
	}

	// 软退避账号上的版本语义:升级到 strike=2 后,v2 失败即使在窗口内也重置为 strike=1、退避回到 +30s;
	// 迟到的 v1 不升级、不写行、不退版本;之后 v2 正常继续升级。
	soft := amb
	s1, _, err := p.RecordFailure(ctx, soft, ClassAmbiguous, 1, now, cfg)
	if err != nil || s1.Strike != 1 {
		t.Fatalf("soft 第一次: %+v err=%v", s1, err)
	}
	s2, _, err := p.RecordFailure(ctx, soft, ClassAmbiguous, 1, s1.AuthUntil.Add(time.Second), cfg)
	if err != nil || s2.Strike != 2 {
		t.Fatalf("soft 第二次: %+v err=%v", s2, err)
	}
	now5 := s2.AuthUntil.Add(-time.Second) // 仍在窗口内
	st, changed, err = p.RecordFailure(ctx, soft, ClassAmbiguous, 2, now5, cfg)
	if err != nil || !changed || st.Strike != 1 || st.HardDisabled || !st.AuthUntil.Equal(now5.Add(30*time.Second)) || st.CredentialVersion != 2 {
		t.Fatalf("版本前进必须重置 strike(窗口内亦生效): st=%+v changed=%v err=%v", st, changed, err)
	}
	now6 := now5.Add(31 * time.Second)
	if _, changed, err := p.RecordFailure(ctx, soft, ClassAmbiguous, 1, now6, cfg); err != nil || changed {
		t.Fatalf("迟到旧版本失败不得升级: changed=%v err=%v", changed, err)
	}
	if got, _, _ := p.Get(ctx, soft); got.Strike != 1 || got.CredentialVersion != 2 {
		t.Fatalf("迟到旧版本失败后行必须不变: %+v", got)
	}
	now7 := now6.Add(time.Second)
	st, changed, err = p.RecordFailure(ctx, soft, ClassAmbiguous, 2, now7, cfg)
	if err != nil || !changed || st.Strike != 2 {
		t.Fatalf("v2 第二次升级: st=%+v changed=%v err=%v", st, changed, err)
	}
	// 清掉 soft 的状态,下面的歧义类计数从零开始(成功请求删除无墓碑的软退避行)。
	if removed, err := p.Clear(ctx, soft, false, time.Now()); err != nil || !removed {
		t.Fatalf("soft 清除: removed=%v err=%v", removed, err)
	}
	if _, found, _ := p.Get(ctx, soft); found {
		t.Fatal("无墓碑的软退避行应被成功请求删除")
	}

	// 歧义类:连续 6 次越窗失败 strike=6 仍不硬禁。
	at := now
	for i := 1; i <= 6; i++ {
		st, changed, err = p.RecordFailure(ctx, amb, ClassAmbiguous, 0, at, cfg)
		if err != nil || !changed || st.Strike != i || st.HardDisabled {
			t.Fatalf("歧义类第 %d 次: st=%+v changed=%v err=%v", i, st, changed, err)
		}
		at = st.AuthUntil.Add(time.Second)
	}
}

// TestPostgresPersistenceConcurrentBurstEscalatesOnce:多副本/多请求在同一时刻对同一坏号并发写,
// 行锁串行化 + WHERE 去抖保证只升级一次(strike=1),越窗后再并发一轮也只到 strike=2。
func TestPostgresPersistenceConcurrentBurstEscalatesOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 1)
	p := NewPostgresPersistence(pool)
	cfg := laneCfg()
	acct := fx.accounts[0]
	burst := func(now time.Time) int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		changedCount := 0
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, changed, err := p.RecordFailure(ctx, acct, ClassIronClad, 1, now, cfg)
				if err != nil {
					t.Errorf("并发写失败: %v", err)
					return
				}
				if changed {
					mu.Lock()
					changedCount++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return changedCount
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if n := burst(now); n != 1 {
		t.Fatalf("同一时刻并发爆发只允许一次真实升级,实得 %d", n)
	}
	if st, _, _ := p.Get(ctx, acct); st.Strike != 1 {
		t.Fatalf("并发爆发后 strike=%d,期望 1", st.Strike)
	}
	if n := burst(now.Add(31 * time.Second)); n != 1 {
		t.Fatalf("越窗后的并发爆发也只允许一次升级,实得 %d", n)
	}
	if st, _, _ := p.Get(ctx, acct); st.Strike != 2 {
		t.Fatalf("第二轮爆发后 strike=%d,期望 2", st.Strike)
	}
}

// TestPostgresPersistenceHardDisableClearAndListing:刷新永久失效即时硬禁(newly 只报一次、strike 不变);
// Clear 删除后 Get/ListBlocking 都看不到;ListBlocking 只列硬禁与未过期软退避。
func TestPostgresPersistenceHardDisableClearAndListing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 4)
	p := NewPostgresPersistence(pool)
	cfg := laneCfg()
	now := time.Now().UTC().Truncate(time.Microsecond)
	hardOnly, softLive, softExpired, escalatedHard := fx.accounts[0], fx.accounts[1], fx.accounts[2], fx.accounts[3]

	st, applied, err := p.MarkHardDisabled(ctx, hardOnly, 0, "refresh_permanent", now, now)
	if err != nil || !applied || !st.NewlyHardDisabled || !st.HardDisabled || st.Strike != 0 || !st.AuthUntil.IsZero() {
		t.Fatalf("刷新永久失效首次硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}
	st, applied, err = p.MarkHardDisabled(ctx, hardOnly, 0, "refresh_permanent", now.Add(time.Second), now.Add(time.Second))
	if err != nil || !applied || st.NewlyHardDisabled || !st.HardDisabled {
		t.Fatalf("重复硬禁 newly 必须为 false 且仍能读到状态: st=%+v applied=%v err=%v", st, applied, err)
	}
	if _, _, err := p.RecordFailure(ctx, softLive, ClassAmbiguous, 0, now, cfg); err != nil {
		t.Fatalf("softLive: %v", err)
	}
	if _, _, err := p.RecordFailure(ctx, softExpired, ClassAmbiguous, 0, now.Add(-time.Hour), cfg); err != nil {
		t.Fatalf("softExpired: %v", err)
	}
	if _, _, err := p.RecordFailure(ctx, escalatedHard, ClassIronClad, 0, now, cfg); err != nil {
		t.Fatalf("escalatedHard: %v", err)
	}
	st, applied, err = p.MarkHardDisabled(ctx, escalatedHard, 0, "refresh_permanent", now.Add(time.Second), now.Add(time.Second))
	if err != nil || !applied || !st.NewlyHardDisabled || st.Strike != 1 || !st.AuthUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("已有退避的账号被刷新硬禁时 strike 与截止保持: st=%+v applied=%v err=%v", st, applied, err)
	}

	listed, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[int64]PersistedState{}
	for _, b := range listed {
		got[b.AccountID] = b
	}
	if e, ok := got[softExpired]; !ok || e.Strike != 1 || !e.AuthUntil.Equal(now.Add(-time.Hour).Add(30*time.Second)) {
		t.Fatalf("已过期但保留 strike 的行必须在列表里(供成功请求清除): %+v", listed)
	}
	if h, ok := got[hardOnly]; !ok || !h.HardDisabled {
		t.Fatalf("硬禁账号必须在列表: %+v", listed)
	}
	if s, ok := got[softLive]; !ok || s.HardDisabled || !s.AuthUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("未过期软退避必须在列表: %+v", listed)
	}
	if e, ok := got[escalatedHard]; !ok || !e.HardDisabled || e.Strike != 1 {
		t.Fatalf("升级后再硬禁的账号必须在列表: %+v", listed)
	}
	// 镜像重载后:过期行放行但保留 strike,硬禁与未过期行阻断。
	mirror := NewPersistentStore(cfg, p)
	mirror.Reload(ctx, now.Add(time.Second))
	if ok, _ := mirror.Eligible(softExpired, now.Add(time.Second)); !ok {
		t.Fatal("过期软退避重载后应放行")
	}
	if ok, hard := mirror.Eligible(hardOnly, now.Add(time.Second)); ok || !hard {
		t.Fatalf("硬禁重载后应阻断: ok=%v hard=%v", ok, hard)
	}
	if ok, _ := mirror.Eligible(softLive, now.Add(time.Second)); ok {
		t.Fatal("未过期软退避重载后应阻断")
	}
	// 成功请求清除:镜像已知的过期行(无墓碑)被删除;镜像不知道的账号不触发写。
	mirror.Clear(ctx, softExpired, ClearReasonSuccess)
	if _, found, _ := p.Get(ctx, softExpired); found {
		t.Fatal("成功请求必须删除镜像已知的过期行")
	}

	removed, err := p.Clear(ctx, hardOnly, true, time.Now())
	if err != nil || !removed {
		t.Fatalf("Clear 首次应删除: removed=%v err=%v", removed, err)
	}
	removed, err = p.Clear(ctx, hardOnly, true, time.Now())
	if err != nil || removed {
		t.Fatalf("Clear 重复应报告不存在: removed=%v err=%v", removed, err)
	}
	if _, found, err := p.Get(ctx, hardOnly); err != nil || found {
		t.Fatalf("运营 Clear 后墓碑对读取方无状态(found=false): found=%v err=%v", found, err)
	}
	var tomb bool
	if err := pool.QueryRow(ctx, `SELECT resumed_at IS NOT NULL FROM provider_account_auth_cooldowns WHERE provider_account_id = $1`, hardOnly).Scan(&tomb); err != nil || !tomb {
		t.Fatalf("运营 Clear 后行应保留为墓碑: tomb=%v err=%v", tomb, err)
	}
	// 账号真相行不存在:没有可保护的对象,按"状态未变"返回(不报错、不建行、也不触发本地兜底)。
	if _, changed, err := p.RecordFailure(ctx, fx.accounts[0]+1_000_000, ClassAmbiguous, 0, now, cfg); err != nil || changed {
		t.Fatalf("不存在的账号不得建行: changed=%v err=%v", changed, err)
	}
}

// TestStoreCrossReplicaConvergence:两个 Store 实例共用真相表模拟两个网关副本 + 重启:
// 副本 A 记录硬禁后,副本 B 重载即拒绝该账号(重启后的新实例首轮重载同样恢复);
// 副本 B 执行运营 resume(Clear)后,副本 A 重载即放行——硬禁不再卡死在别的副本;
// 持久化写失败时(断开的后端)本地仍保护,且重载不会把仍生效的本地条目冲掉。
func TestStoreCrossReplicaConvergence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 2)
	cfg := laneCfg()
	acct, other := fx.accounts[0], fx.accounts[1]
	now := time.Now().UTC().Truncate(time.Microsecond)

	replicaA := NewPersistentStore(cfg, NewPostgresPersistence(pool))
	replicaB := NewPersistentStore(cfg, NewPostgresPersistence(pool))
	if replicaA.PersistenceKind() != "postgres" {
		t.Fatalf("PersistenceKind=%s", replicaA.PersistenceKind())
	}
	replicaA.OnRefreshResult(ctx, acct, 0, false, true)
	if ok, hard := replicaA.Eligible(acct, now); ok || !hard {
		t.Fatalf("副本 A 本地镜像应即时硬禁: ok=%v hard=%v", ok, hard)
	}
	if ok, _ := replicaB.Eligible(acct, now); !ok {
		t.Fatal("前置:副本 B 尚未重载时本地镜像不知情")
	}
	replicaB.Reload(ctx, now)
	if ok, hard := replicaB.Eligible(acct, now); ok || !hard {
		t.Fatalf("副本 B 重载后必须看到硬禁: ok=%v hard=%v", ok, hard)
	}
	restarted := NewPersistentStore(cfg, NewPostgresPersistence(pool))
	done := restarted.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, hard := restarted.Eligible(acct, now); !ok && hard {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("重启后的新实例必须在首轮重载后恢复硬禁")
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := restarted.Snapshot(ctx, acct, now)
	if !snap.Found || !snap.HardDisabled || snap.Persistence != "postgres" {
		t.Fatalf("重启实例的快照应来自真相表: %+v", snap)
	}

	// 运营在副本 B resume:真相行删除,副本 A 重载后放行。
	replicaB.Clear(ctx, acct, ClearReasonOperatorResume)
	if ok, _ := replicaA.Eligible(acct, now); ok {
		t.Fatal("前置:副本 A 重载前本地镜像仍硬禁")
	}
	replicaA.Reload(ctx, now)
	if ok, hard := replicaA.Eligible(acct, now); !ok || hard {
		t.Fatalf("副本 A 重载后必须解除硬禁: ok=%v hard=%v", ok, hard)
	}
	cancel()
	<-done

	// 软退避跨副本:A 记录一次失败,B 重载后拒绝到截止,越过截止放行。
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	replicaA.Suspend(ctx2, other, ClassAmbiguous, 1, now)
	replicaB.Reload(ctx2, now)
	if ok, hard := replicaB.Eligible(other, now.Add(10*time.Second)); ok || hard {
		t.Fatalf("副本 B 应看到软退避: ok=%v hard=%v", ok, hard)
	}
	if ok, _ := replicaB.Eligible(other, now.Add(31*time.Second)); !ok {
		t.Fatal("软退避到期后应放行")
	}

	// 持久化写失败(注入一次后端错误)时本地仍保护;后续成功的重载不得冲掉仍生效的本地条目,过期后才丢弃。
	flakyA := NewPersistentStore(cfg, &flakyPersistence{inner: NewPostgresPersistence(pool), failures: 1})
	flakyAcct := fx.accounts[1]
	flakyA.Suspend(ctx2, flakyAcct, ClassIronClad, 1, now.Add(2*time.Hour))
	if ok, _ := flakyA.Eligible(flakyAcct, now.Add(2*time.Hour+time.Second)); ok {
		t.Fatal("写失败后本地镜像必须仍然移出选号")
	}
	if got, found, _ := NewPostgresPersistence(pool).Get(ctx2, flakyAcct); found && got.AuthUntil.After(now.Add(2*time.Hour)) {
		t.Fatalf("前置:写失败不应落到真相表: %+v", got)
	}
	flakyA.Reload(ctx2, now.Add(2*time.Hour+time.Second))
	if ok, _ := flakyA.Eligible(flakyAcct, now.Add(2*time.Hour+time.Second)); ok {
		t.Fatal("成功重载不得冲掉仍生效的本地条目")
	}
	flakyA.Reload(ctx2, now.Add(4*time.Hour))
	if ok, _ := flakyA.Eligible(flakyAcct, now.Add(4*time.Hour)); !ok {
		t.Fatal("过期的本地条目应在重载时丢弃")
	}

	// 持久化整体不可用:本地仍保护,重载失败保留上次镜像。
	broken, err := pgxpool.New(ctx2, os.Getenv("HUAKAI_DATABASE_URL"))
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	broken.Close()
	degraded := NewPersistentStore(cfg, NewPostgresPersistence(broken))
	degraded.Suspend(ctx2, other, ClassIronClad, 1, now)
	if ok, _ := degraded.Eligible(other, now.Add(time.Second)); ok {
		t.Fatal("持久化写失败时本地镜像必须仍然移出选号")
	}
	degraded.Reload(ctx2, now.Add(time.Second))
	if ok, _ := degraded.Eligible(other, now.Add(time.Second)); ok {
		t.Fatal("重载失败不得冲掉仍生效的本地条目")
	}
	snap = degraded.Snapshot(ctx2, other, now.Add(time.Second))
	if !snap.Found || snap.Persistence != "process_local" {
		t.Fatalf("写失败的本地条目应以 process_local 暴露给运营: %+v", snap)
	}
}

// insertLaneCredential 给账号写一条可服务凭据(指定版本),模拟凭据轮换后的账号真相。
func insertLaneCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, version int32) {
	t.Helper()
	insertLaneCredentialState(t, ctx, pool, tenantID, accountID, version, "active")
}

func insertLaneCredentialState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, version int32, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
			encrypted_payload, key_id, nonce, aad_hash
		) VALUES ($1, $2, 'openai', 'api_key', $7, $3, $4, 'test-key', $5, $6)`,
		tenantID, accountID, version, []byte("ciphertext"), []byte("nonce-12345678"),
		fmt.Sprintf("aad-lane-%d-%d-%d", tenantID, accountID, version), state); err != nil {
		t.Fatalf("insert credential v%d(%s): %v", version, state, err)
	}
}

// TestPostgresPersistenceRefreshHardDisableRespectsRotation:刷新永久失效的硬禁带凭据版本守卫——
// 账号已轮换出更高版本的可服务凭据时,旧版本的结论不写行(applied=false);当前版本或未知版本(0)
// 才硬禁;运营 Clear 后到达的新证据重新建立硬禁(不会因"已硬禁 no rows + 并发清除"退化成本地硬禁)。
// 变异守卫:去掉 INSERT ... SELECT 的 NOT EXISTS 守卫 → 第一段断言红;把 DO UPDATE 加回
// WHERE NOT hard_disabled → 并发清除段的 applied 断言红。
func TestPostgresPersistenceRefreshHardDisableRespectsRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 4)
	p := NewPostgresPersistence(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rotated, plain, viaStore, midState := fx.accounts[0], fx.accounts[1], fx.accounts[2], fx.accounts[3]
	insertLaneCredential(t, ctx, pool, fx.tenantID, rotated, 2)
	insertLaneCredential(t, ctx, pool, fx.tenantID, viaStore, 2)
	// v2 处于暂时不可调度的中间态:它仍是更新的凭据,旧版本结论同样不得硬禁整账号。
	insertLaneCredentialState(t, ctx, pool, fx.tenantID, midState, 2, "temp_unschedulable")

	if st, applied, err := p.MarkHardDisabled(ctx, midState, 1, "refresh_permanent", now, now); err != nil || applied {
		t.Fatalf("更新凭据处于中间态时旧版本结论也不得硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}

	if st, applied, err := p.MarkHardDisabled(ctx, rotated, 1, "refresh_permanent", now, now); err != nil || applied {
		t.Fatalf("旧版本(v1)的永久失效不得硬禁已轮换到 v2 的账号: st=%+v applied=%v err=%v", st, applied, err)
	}
	if _, found, _ := p.Get(ctx, rotated); found {
		t.Fatal("被版本守卫拒绝的硬禁不得留下任何行")
	}
	st, applied, err := p.MarkHardDisabled(ctx, rotated, 2, "refresh_permanent", now, now)
	if err != nil || !applied || !st.HardDisabled || !st.NewlyHardDisabled || st.CredentialVersion != 2 {
		t.Fatalf("当前版本(v2)的永久失效必须硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}
	// 未知版本(0):不做守卫,直接硬禁。
	st, applied, err = p.MarkHardDisabled(ctx, plain, 0, "refresh_permanent", now, now)
	if err != nil || !applied || !st.NewlyHardDisabled {
		t.Fatalf("未知版本的永久失效应直接硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}
	// 重复硬禁:仍 applied,但 newly=false,进入时刻保持首次。
	st, applied, err = p.MarkHardDisabled(ctx, plain, 0, "refresh_permanent", now.Add(time.Second), now.Add(time.Second))
	if err != nil || !applied || st.NewlyHardDisabled {
		t.Fatalf("重复硬禁应 applied 且 newly=false: st=%+v applied=%v err=%v", st, applied, err)
	}
	if got, _, _ := p.Get(ctx, plain); !got.HardDisabledAt.Equal(now) {
		t.Fatalf("重复硬禁不得改写进入时刻: %v", got.HardDisabledAt)
	}
	// 运营 Clear 之后到达的永久失效:是新的证据,重新建立硬禁而不是丢失。
	if removed, err := p.Clear(ctx, plain, true, time.Now()); err != nil || !removed {
		t.Fatalf("Clear: removed=%v err=%v", removed, err)
	}
	st, applied, err = p.MarkHardDisabled(ctx, plain, 0, "refresh_permanent", now.Add(2*time.Second), now.Add(2*time.Second))
	if err != nil || !applied || !st.NewlyHardDisabled {
		t.Fatalf("清除后到达的永久失效应重新硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}

	// Store 层:账号已持有 v2 凭据并因 v2 失败处于软退避;迟到的 v1 永久失效被真相表拒绝,本地也不硬禁,
	// 软退避(当前版本的真实状态)保留。
	store := NewPersistentStore(laneCfg(), p)
	store.Suspend(ctx, viaStore, ClassAmbiguous, 2, now)
	if ok, _ := store.Eligible(viaStore, now); ok {
		t.Fatal("前置:v2 软退避应生效")
	}
	store.OnRefreshResult(ctx, viaStore, 1, false, true)
	if ok, hard := store.Eligible(viaStore, now); ok || hard {
		t.Fatalf("被真相表拒绝的旧版本永久失效不得在本地硬禁,软退避应保留: ok=%v hard=%v", ok, hard)
	}
	if got, found, _ := p.Get(ctx, viaStore); !found || got.HardDisabled || got.Strike != 1 {
		t.Fatalf("真相表不得因旧版本结论硬禁,软退避应保留: %+v found=%v", got, found)
	}
}

// TestPostgresPersistenceSuccessKeepsHardDisable:成功请求只清软退避,硬禁必须由运营 resume 解除;
// 两层(真相表与 Store 镜像)语义一致。判别:把 ClearAuthCooldownSoft 的 NOT hard_disabled 去掉 → 红。
func TestPostgresPersistenceSuccessKeepsHardDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 2)
	p := NewPostgresPersistence(pool)
	cfg := laneCfg()
	now := time.Now().UTC().Truncate(time.Microsecond)
	hard, soft := fx.accounts[0], fx.accounts[1]

	if _, _, err := p.MarkHardDisabled(ctx, hard, 0, "refresh_permanent", now, now); err != nil {
		t.Fatalf("MarkHardDisabled: %v", err)
	}
	if removed, err := p.Clear(ctx, hard, false, time.Now()); err != nil || removed {
		t.Fatalf("成功请求(软清除)不得删除硬禁行: removed=%v err=%v", removed, err)
	}
	if got, found, _ := p.Get(ctx, hard); !found || !got.HardDisabled {
		t.Fatalf("软清除后硬禁行必须仍在: %+v found=%v", got, found)
	}
	if _, _, err := p.RecordFailure(ctx, soft, ClassAmbiguous, 0, now, cfg); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if removed, err := p.Clear(ctx, soft, false, time.Now()); err != nil || !removed {
		t.Fatalf("成功请求应清除软退避行: removed=%v err=%v", removed, err)
	}
	if removed, err := p.Clear(ctx, hard, true, time.Now()); err != nil || !removed {
		t.Fatalf("运营 resume 应解除硬禁: removed=%v err=%v", removed, err)
	}
	if _, found, _ := p.Get(ctx, hard); found {
		t.Fatal("运营 resume 后对读取方应为无状态(墓碑不计)")
	}

	// Store 层:硬禁由真相表加载到镜像后,成功请求不得清除,运营 resume 才清除。
	// (前面的运营 resume 留下了墓碑,新证据必须晚于墓碑才生效。)
	time.Sleep(10 * time.Millisecond)
	later := time.Now().UTC().Truncate(time.Microsecond)
	if _, applied, err := p.MarkHardDisabled(ctx, hard, 0, "refresh_permanent", later, later); err != nil || !applied {
		t.Fatalf("墓碑之后的新永久失效应重新硬禁: applied=%v err=%v", applied, err)
	}
	store := NewPersistentStore(cfg, p)
	store.Reload(ctx, now)
	store.Clear(ctx, hard, ClearReasonSuccess)
	if ok, isHard := store.Eligible(hard, now); ok || !isHard {
		t.Fatalf("成功请求不得解除镜像里的硬禁: ok=%v hard=%v", ok, isHard)
	}
	if got, found, _ := p.Get(ctx, hard); !found || !got.HardDisabled {
		t.Fatal("成功请求不得解除真相表里的硬禁")
	}
	store.Clear(ctx, hard, ClearReasonOperatorResume)
	if ok, _ := store.Eligible(hard, now); !ok {
		t.Fatal("运营 resume 必须解除硬禁")
	}
	if _, found, _ := p.Get(ctx, hard); found {
		t.Fatal("运营 resume 必须把真相行变成无状态墓碑(读取方视为无状态)")
	}
}

// flakyPersistence 在前 failures 次写操作上返回错误,之后透传给真正的后端,用来模拟数据库短暂不可用后恢复。
type flakyPersistence struct {
	inner    Persistence
	failures int
}

func (f *flakyPersistence) Kind() string { return f.inner.Kind() }
func (f *flakyPersistence) RecordFailure(ctx context.Context, accountID int64, class FailureClass, credVersion int, now time.Time, cfg Config) (PersistedState, bool, error) {
	if f.failures > 0 {
		f.failures--
		return PersistedState{}, false, errors.New("injected persistence failure")
	}
	return f.inner.RecordFailure(ctx, accountID, class, credVersion, now, cfg)
}
func (f *flakyPersistence) MarkHardDisabled(ctx context.Context, accountID int64, observed int, class string, evidenceAt, now time.Time) (PersistedState, bool, error) {
	if f.failures > 0 {
		f.failures--
		return PersistedState{}, false, errors.New("injected persistence failure")
	}
	return f.inner.MarkHardDisabled(ctx, accountID, observed, class, evidenceAt, now)
}
func (f *flakyPersistence) Clear(ctx context.Context, accountID int64, hardToo bool, now time.Time) (bool, error) {
	return f.inner.Clear(ctx, accountID, hardToo, now)
}
func (f *flakyPersistence) Get(ctx context.Context, accountID int64) (PersistedState, bool, error) {
	return f.inner.Get(ctx, accountID)
}
func (f *flakyPersistence) List(ctx context.Context) ([]PersistedState, error) {
	return f.inner.List(ctx)
}

// TestStoreReplaysLocalHardDisableAfterRecovery:数据库短暂不可用时刷新永久失效只落本地硬禁;
// 数据库恢复后,下一次重载无需任何新请求就把硬禁补写回真相表,其他副本随即可见。
// 判别:去掉 Reload 里的 replayLocalHardDisables → 真相表始终无行,断言红。
func TestStoreReplaysLocalHardDisableAfterRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 1)
	acct := fx.accounts[0]
	real := NewPostgresPersistence(pool)
	flaky := &flakyPersistence{inner: real, failures: 1}
	store := NewPersistentStore(laneCfg(), flaky)
	now := time.Now().UTC().Truncate(time.Microsecond)

	store.OnRefreshResult(ctx, acct, 0, false, true) // 第一次写失败 → 本地硬禁
	if ok, hard := store.Eligible(acct, now); ok || !hard {
		t.Fatalf("写失败时本地必须硬禁: ok=%v hard=%v", ok, hard)
	}
	if _, found, _ := real.Get(ctx, acct); found {
		t.Fatal("前置:真相表不应有行")
	}
	snap := store.Snapshot(ctx, acct, now)
	if snap.Persistence != "process_local" || !snap.HardDisabled {
		t.Fatalf("仅本副本持有的硬禁应以 process_local 暴露: %+v", snap)
	}
	store.Reload(ctx, now.Add(5*time.Second)) // 数据库已恢复:补写
	got, found, err := real.Get(ctx, acct)
	if err != nil || !found || !got.HardDisabled {
		t.Fatalf("重载必须把本地硬禁补写回真相表: %+v found=%v err=%v", got, found, err)
	}
	snap = store.Snapshot(ctx, acct, now.Add(5*time.Second))
	if snap.Persistence != "postgres" {
		t.Fatalf("补写后快照应来自真相表: %+v", snap)
	}
	other := NewPersistentStore(laneCfg(), real)
	other.Reload(ctx, now.Add(5*time.Second))
	if ok, hard := other.Eligible(acct, now.Add(5*time.Second)); ok || !hard {
		t.Fatalf("补写后其他副本重载必须看到硬禁: ok=%v hard=%v", ok, hard)
	}
}

// TestPostgresPersistenceHardDisableSerializesWithRotation:硬禁与显式轮换并发——轮换事务先锁住并更新
// 凭据行(版本 +1)但尚未提交时,硬禁事务在 FOR UPDATE 上等待;轮换提交后硬禁事务读到新版本,判定这次
// 结论属于旧凭据而放弃(applied=false,无行)。判别:把 LockCurrentCredentialVersion 去掉 FOR UPDATE 或
// 让守卫只在 INSERT 的快照里评估 → 硬禁抢先写入,轮换后账号被错误硬禁,断言红。
func TestPostgresPersistenceHardDisableSerializesWithRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 1)
	acct := fx.accounts[0]
	insertLaneCredential(t, ctx, pool, fx.tenantID, acct, 1)
	p := NewPostgresPersistence(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// 模拟显式轮换事务:更新凭据版本并清车道行,但先不提交。
	rotate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rotate: %v", err)
	}
	// 断言失败时必须释放事务,否则连接池关闭会永久等待。
	defer func() { _ = rotate.Rollback(context.Background()) }()
	if _, err := rotate.Exec(ctx, `UPDATE account_credentials SET credential_version = credential_version + 1
		WHERE provider_account_id = $1 AND deleted_at IS NULL`, acct); err != nil {
		t.Fatalf("rotate update: %v", err)
	}
	if _, err := rotate.Exec(ctx, `DELETE FROM provider_account_auth_cooldowns WHERE provider_account_id = $1`, acct); err != nil {
		t.Fatalf("rotate delete: %v", err)
	}
	type result struct {
		applied bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, applied, err := p.MarkHardDisabled(ctx, acct, 1, "refresh_permanent", now, now)
		done <- result{applied: applied, err: err}
	}()
	select {
	case r := <-done:
		t.Fatalf("硬禁事务不得在轮换提交前完成: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if err := rotate.Commit(ctx); err != nil {
		t.Fatalf("rotate commit: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.applied {
			t.Fatalf("轮换提交后旧版本结论必须被放弃: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("硬禁事务在轮换提交后仍未结束")
	}
	if _, found, _ := p.Get(ctx, acct); found {
		t.Fatal("轮换后账号不得留下硬禁行")
	}

	// 反向顺序:硬禁先提交,随后的轮换(同一事务内删除车道行)清掉它——由 credentialstore.Rotate 保证,
	// 这里只验证硬禁在无并发时正常写入,作为上面等待断言的对照。
	st, applied, err := p.MarkHardDisabled(ctx, acct, 2, "refresh_permanent", now, now)
	if err != nil || !applied || !st.HardDisabled {
		t.Fatalf("当前版本(v2)的永久失效应硬禁: st=%+v applied=%v err=%v", st, applied, err)
	}
}

// TestPostgresPersistenceResumeTombstoneBlocksStaleReplay:运营 resume 在真相表留下墓碑;
// 早于墓碑的证据(某副本写库失败留下的旧本地硬禁经重载补写)被拒绝并丢弃本地状态,
// 晚于墓碑的新永久失效照常重新硬禁。判别:去掉 Mark 的墓碑 WHERE → 补写重新硬禁,断言红。
func TestPostgresPersistenceResumeTombstoneBlocksStaleReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 1)
	acct := fx.accounts[0]
	real := NewPostgresPersistence(pool)
	flaky := &flakyPersistence{inner: real, failures: 1}
	replicaA := NewPersistentStore(laneCfg(), flaky)
	replicaB := NewPersistentStore(laneCfg(), real)
	now := time.Now().UTC().Truncate(time.Microsecond)

	replicaA.OnRefreshResult(ctx, acct, 0, false, true) // 写失败 → 仅副本 A 本地硬禁(证据时刻 ≈ 现在)
	if ok, hard := replicaA.Eligible(acct, now); ok || !hard {
		t.Fatalf("前置:A 本地硬禁: ok=%v hard=%v", ok, hard)
	}
	time.Sleep(20 * time.Millisecond)
	replicaB.Clear(ctx, acct, ClearReasonOperatorResume) // 运营在 B resume:真相表留下墓碑
	if _, found, err := real.Get(ctx, acct); err != nil || found {
		t.Fatalf("resume 后真相应为无状态墓碑: found=%v err=%v", found, err)
	}
	replicaA.Reload(ctx, time.Now())
	if got, found, _ := real.Get(ctx, acct); found && got.HardDisabled {
		t.Fatal("A 的补写不得撤销更晚的运营 resume")
	}
	if ok, _ := replicaA.Eligible(acct, time.Now()); !ok {
		t.Fatal("被墓碑拒绝的本地硬禁必须丢弃")
	}
	// resume 之后新到达的永久失效:证据晚于墓碑,重新硬禁。
	time.Sleep(20 * time.Millisecond)
	replicaB.OnRefreshResult(ctx, acct, 0, false, true)
	if got, found, _ := real.Get(ctx, acct); !found || !got.HardDisabled {
		t.Fatal("墓碑之后的新永久失效必须重新硬禁")
	}
	// 成功请求只清软退避:硬禁行不受影响。
	replicaB.Clear(ctx, acct, ClearReasonSuccess)
	if got, found, _ := real.Get(ctx, acct); !found || !got.HardDisabled {
		t.Fatal("成功请求不得解除硬禁")
	}
}

// TestPostgresPersistenceRecordFailureRespectsRotationWhenRowMissing:显式轮换已删行后,
// 在途旧版本的失败不得给新凭据建退避窗(INSERT 分支同样做版本守卫);已硬禁的行不再被请求失败改写。
// 判别:去掉 INSERT...SELECT 的 NOT EXISTS → 旧版本失败建行,断言红;去掉 DO UPDATE 的 NOT hard_disabled →
// 硬禁行 strike 被改写,断言红。
func TestPostgresPersistenceRecordFailureRespectsRotationWhenRowMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openLanePool(t, ctx)
	fx := seedLaneFixture(t, ctx, pool, 2)
	p := NewPostgresPersistence(pool)
	cfg := laneCfg()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rotated, hard := fx.accounts[0], fx.accounts[1]
	insertLaneCredential(t, ctx, pool, fx.tenantID, rotated, 2)

	if _, changed, err := p.RecordFailure(ctx, rotated, ClassIronClad, 1, now, cfg); err != nil || changed {
		t.Fatalf("账号已持有 v2 凭据时 v1 的失败不得建行: changed=%v err=%v", changed, err)
	}
	if _, found, _ := p.Get(ctx, rotated); found {
		t.Fatal("旧版本失败不得留下任何行")
	}
	if st, changed, err := p.RecordFailure(ctx, rotated, ClassIronClad, 2, now, cfg); err != nil || !changed || st.Strike != 1 {
		t.Fatalf("当前版本的失败照常建行: st=%+v changed=%v err=%v", st, changed, err)
	}

	if _, _, err := p.MarkHardDisabled(ctx, hard, 0, "refresh_permanent", now, now); err != nil {
		t.Fatalf("MarkHardDisabled: %v", err)
	}
	if _, changed, err := p.RecordFailure(ctx, hard, ClassIronClad, 0, now.Add(time.Minute), cfg); err != nil || changed {
		t.Fatalf("已硬禁的行不得被请求失败改写: changed=%v err=%v", changed, err)
	}
	if got, _, _ := p.Get(ctx, hard); got.Strike != 0 || !got.AuthUntil.IsZero() || !got.HardDisabled {
		t.Fatalf("硬禁行必须保持原样: %+v", got)
	}
}
