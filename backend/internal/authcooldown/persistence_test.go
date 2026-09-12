package authcooldown

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakePersistence 是可编排的真相后端替身:按脚本返回状态或错误,并记录调用,
// 用于验证 Store 的写穿、失败回退、读穿与重载合并,不依赖数据库。
type fakePersistence struct {
	mu             sync.Mutex
	rows           map[int64]PersistedState
	currentVersion map[int64]int       // 账号当前最高凭据版本(模拟 account_credentials)
	resumedAt      map[int64]time.Time // 运营 resume 墓碑
	failNext       error
	blocking       []PersistedState
	listErr        error
	calls          []string
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{rows: map[int64]PersistedState{}, currentVersion: map[int64]int{}, resumedAt: map[int64]time.Time{}}
}

func (f *fakePersistence) Kind() string { return "fake" }

func (f *fakePersistence) record(op string) error {
	f.calls = append(f.calls, op)
	err := f.failNext
	f.failNext = nil
	return err
}

func (f *fakePersistence) RecordFailure(_ context.Context, accountID int64, class FailureClass, credVersion int, now time.Time, cfg Config) (PersistedState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("record"); err != nil {
		return PersistedState{}, false, err
	}
	cur := f.rows[accountID]
	advanced := credVersion > 0 && cur.CredentialVersion > 0 && credVersion > cur.CredentialVersion
	if !advanced && !now.After(cur.AuthUntil) && cur.Strike > 0 {
		return PersistedState{}, false, nil
	}
	if advanced {
		cur = PersistedState{}
	}
	cur.AccountID = accountID
	cur.Strike++
	cur.AuthUntil = now.Add(cfg.normalized().Base)
	if credVersion > cur.CredentialVersion {
		cur.CredentialVersion = credVersion
	}
	if class == ClassIronClad && cur.Strike >= cfg.normalized().HardDisableStrikeK && !cur.HardDisabled {
		cur.HardDisabled = true
		cur.NewlyHardDisabled = true
	} else {
		cur.NewlyHardDisabled = false
	}
	f.rows[accountID] = cur
	return cur, true, nil
}

func (f *fakePersistence) MarkHardDisabled(_ context.Context, accountID int64, observed int, class string, evidenceAt, now time.Time) (PersistedState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("hard"); err != nil {
		return PersistedState{}, false, err
	}
	// 模拟真相表的版本守卫:currentVersion 高于观察到的版本 → 拒绝。
	if observed > 0 && f.currentVersion[accountID] > observed {
		return PersistedState{}, false, nil
	}
	// 模拟 resume 墓碑:早于墓碑的证据不得写回。
	if tomb, ok := f.resumedAt[accountID]; ok && !evidenceAt.After(tomb) {
		return PersistedState{}, false, nil
	}
	cur := f.rows[accountID]
	newly := !cur.HardDisabled
	cur.AccountID = accountID
	cur.HardDisabled = true
	cur.FailureClass = class
	cur.NewlyHardDisabled = newly
	if newly {
		cur.HardDisabledAt = now
	}
	if observed > cur.CredentialVersion {
		cur.CredentialVersion = observed
	}
	f.rows[accountID] = cur
	return cur, true, nil
}

func (f *fakePersistence) Clear(_ context.Context, accountID int64, hardToo bool, now time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("clear"); err != nil {
		return false, err
	}
	cur, ok := f.rows[accountID]
	if hardToo {
		f.resumedAt[accountID] = now
		delete(f.rows, accountID)
		return ok, nil
	}
	if !ok || cur.HardDisabled {
		return false, nil
	}
	delete(f.rows, accountID)
	return true, nil
}

func (f *fakePersistence) Get(_ context.Context, accountID int64) (PersistedState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("get"); err != nil {
		return PersistedState{}, false, err
	}
	st, ok := f.rows[accountID]
	return st, ok, nil
}

// List 返回真相里的全部行,再加上脚本额外注入的"他副本写入"行(blocking)。
func (f *fakePersistence) List(_ context.Context) ([]PersistedState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "list")
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]PersistedState, 0, len(f.rows)+len(f.blocking)+len(f.resumedAt))
	seen := map[int64]bool{}
	for id, row := range f.rows {
		row.AccountID = id
		out = append(out, row)
		seen[id] = true
	}
	for _, row := range f.blocking {
		if !seen[row.AccountID] {
			out = append(out, row)
			seen[row.AccountID] = true
		}
	}
	for id, tomb := range f.resumedAt {
		if !seen[id] {
			out = append(out, PersistedState{AccountID: id, ResumedAt: tomb})
		}
	}
	return out, nil
}

func (f *fakePersistence) callCount(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == op {
			n++
		}
	}
	return n
}

// 写穿:Suspend 先写真相再刷新镜像;窗口内(后端报告未变)不改镜像也不重复升级;
// Clear 删真相与镜像;PersistenceKind 反映后端。
func TestPersistentStoreWritesThroughTruth(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	if s.PersistenceKind() != "fake" {
		t.Fatalf("PersistenceKind=%s", s.PersistenceKind())
	}
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now)
	if ok, _ := s.Eligible(7, now.Add(time.Second)); ok {
		t.Fatal("写穿后镜像必须移出选号")
	}
	if fp.callCount("record") != 1 || fp.rows[7].Strike != 1 {
		t.Fatalf("真相后端应收到一次写入: calls=%v rows=%+v", fp.calls, fp.rows)
	}
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now.Add(time.Second))
	strike, _, _ := inspect(s, 7)
	if strike != 1 || fp.rows[7].Strike != 1 {
		t.Fatalf("窗口内失败不得升级镜像或真相: mirror=%d truth=%d", strike, fp.rows[7].Strike)
	}
	// 镜像已知窗口内且版本未前进 → 本地短路,不再访问真相后端(401 风暴不付数据库往返)。
	if fp.callCount("record") != 1 {
		t.Fatalf("窗口内重复失败不得访问后端: record 调用数=%d", fp.callCount("record"))
	}
	// 窗口内到达更高凭据版本的失败 → 不短路,必须打到后端按版本重置。
	s.Suspend(context.Background(), 7, ClassAmbiguous, 2, now.Add(2*time.Second))
	if fp.callCount("record") != 2 || fp.rows[7].CredentialVersion != 2 {
		t.Fatalf("版本前进的失败必须写后端: calls=%d version=%d", fp.callCount("record"), fp.rows[7].CredentialVersion)
	}
	// 窗口内到达更低(迟到)版本的失败 → 短路,不打后端。
	s.Suspend(context.Background(), 7, ClassAmbiguous, 1, now.Add(3*time.Second))
	if fp.callCount("record") != 2 {
		t.Fatalf("迟到旧版本失败不得访问后端: calls=%d", fp.callCount("record"))
	}
	// 已硬禁(且版本未前进)的账号再失败不改变任何结果 → 短路,即使没有软退避截止。
	fp.rows[30] = PersistedState{AccountID: 30, HardDisabled: true, CredentialVersion: 1}
	fp.blocking = nil
	s.Reload(context.Background(), now)
	before := fp.callCount("record")
	s.Suspend(context.Background(), 30, ClassIronClad, 1, now.Add(time.Hour))
	if fp.callCount("record") != before {
		t.Fatal("硬禁账号的重复失败不得访问后端")
	}
	s.Clear(context.Background(), 7, ClearReasonSuccess)
	if _, ok := fp.rows[7]; ok {
		t.Fatal("Clear 必须删除真相行")
	}
	if ok, _ := s.Eligible(7, now.Add(time.Second)); !ok {
		t.Fatal("Clear 必须删除镜像条目")
	}
	// 每次成功请求都会触发 Clear:镜像没有条目的账号不得向真相表发删除语句。
	before = fp.callCount("clear")
	s.Clear(context.Background(), 8, ClearReasonSuccess)
	if fp.callCount("clear") != before {
		t.Fatal("镜像无条目的成功请求不得产生持久化写")
	}
	// 运营 resume 即使镜像不知情也必须删除真相行(他副本刚写入、本副本尚未重载)。
	fp.rows[10] = PersistedState{AccountID: 10, HardDisabled: true}
	s.Clear(context.Background(), 10, ClearReasonOperatorResume)
	if _, ok := fp.rows[10]; ok {
		t.Fatal("运营 resume 必须无条件删除真相行")
	}
	// 他副本写入的行经重载进入镜像后,本副本的成功请求才会清除它。
	fp.rows[12] = PersistedState{AccountID: 12, Strike: 2, AuthUntil: now.Add(-time.Minute)}
	fp.blocking = []PersistedState{fp.rows[12]}
	s.Reload(context.Background(), now)
	s.Clear(context.Background(), 12, ClearReasonSuccess)
	if _, ok := fp.rows[12]; ok {
		t.Fatal("重载后已知的过期行必须在成功时清除(strike 归零)")
	}
}

// 写失败回退:真相后端报错时本副本仍把账号移出选号(镜像条目标记为本地),
// Snapshot 以 process_local 暴露;后端恢复后的重载不冲掉仍生效的本地条目,过期后丢弃。
func TestPersistentStoreFallsBackLocallyOnWriteFailure(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	fp.failNext = errors.New("db down")
	s.Suspend(context.Background(), 9, ClassIronClad, 1, now)
	if ok, _ := s.Eligible(9, now.Add(time.Second)); ok {
		t.Fatal("写失败时本地仍须移出选号")
	}
	if _, ok := fp.rows[9]; ok {
		t.Fatal("前置:真相后端不应有行")
	}
	snap := s.Snapshot(context.Background(), 9, now.Add(time.Second))
	if !snap.Found || snap.Eligible || snap.Persistence != "process_local" {
		t.Fatalf("写失败的本地条目应以 process_local 暴露: %+v", snap)
	}
	// 本地兜底条目不代表真相表状态:窗口内的下一次失败仍必须尝试写后端(后端已恢复时立即收敛)。
	before := fp.callCount("record")
	s.Suspend(context.Background(), 9, ClassIronClad, 1, now.Add(2*time.Second))
	if fp.callCount("record") != before+1 {
		t.Fatal("本地兜底条目不得短路后端写入")
	}
	if row, ok := fp.rows[9]; !ok || row.Strike != 1 {
		t.Fatalf("后端恢复后应写入真相: %+v", fp.rows)
	}
	s.Reload(context.Background(), now.Add(time.Second))
	if ok, _ := s.Eligible(9, now.Add(time.Second)); ok {
		t.Fatal("重载不得冲掉仍生效的本地条目")
	}
	s.Reload(context.Background(), now.Add(time.Hour))
	if ok, _ := s.Eligible(9, now.Add(time.Hour)); !ok {
		t.Fatal("过期本地条目应在重载时丢弃")
	}
	// 硬禁的本地条目不过期:后端仍不可用时跨重载保留;后端恢复后由重载补写回真相表并转为普通条目。
	fp.failNext = errors.New("db down")
	s.OnRefreshResult(context.Background(), 11, 0, false, true)
	fp.failNext = errors.New("db still down")
	s.Reload(context.Background(), now.Add(48*time.Hour))
	if ok, hard := s.Eligible(11, now.Add(48*time.Hour)); ok || !hard {
		t.Fatalf("后端不可用时写失败的本地硬禁必须跨重载保留: ok=%v hard=%v", ok, hard)
	}
	if _, ok := fp.rows[11]; ok {
		t.Fatal("前置:后端不可用时不应有真相行")
	}
	s.Reload(context.Background(), now.Add(49*time.Hour))
	if row, ok := fp.rows[11]; !ok || !row.HardDisabled {
		t.Fatalf("后端恢复后重载必须把本地硬禁补写回真相表: %+v", fp.rows)
	}
	snap = s.Snapshot(context.Background(), 11, now.Add(49*time.Hour))
	if !snap.HardDisabled || snap.Persistence != "fake" {
		t.Fatalf("补写成功后快照应来自真相表: %+v", snap)
	}
	// 补写时若真相表判定该硬禁属于已轮换掉的旧凭据,则本地条目作废。
	fp.failNext = errors.New("db down")
	s.OnRefreshResult(context.Background(), 12, 1, false, true)
	fp.currentVersion[12] = 2
	s.Reload(context.Background(), now.Add(50*time.Hour))
	if ok, _ := s.Eligible(12, now.Add(50*time.Hour)); !ok {
		t.Fatal("已被更新凭据取代的本地硬禁应在补写被拒后移出镜像")
	}
}

// 重载:真相里其他副本写入的阻断进入本镜像;真相里已被删除(他副本 resume)的条目从镜像消失;
// 列表失败时保留上次镜像。Start 首轮即重载(重启恢复)。
func TestPersistentStoreReloadConvergesWithOtherReplicas(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(Config{Base: time.Second, Cap: time.Minute, HardDisableStrikeK: 3, ReloadInterval: 5 * time.Millisecond}, fp)
	now := time.Unix(1_000_000, 0)
	s.Suspend(context.Background(), 5, ClassAmbiguous, 1, now)
	delete(fp.rows, 5) // 他副本对账号 5 执行了 resume:真相行已被删除
	fp.blocking = []PersistedState{{AccountID: 21, HardDisabled: true}, {AccountID: 22, Strike: 1, AuthUntil: now.Add(time.Hour)}}
	s.Reload(context.Background(), now)
	if ok, hard := s.Eligible(21, now); ok || !hard {
		t.Fatalf("他副本硬禁应进入镜像: ok=%v hard=%v", ok, hard)
	}
	if ok, hard := s.Eligible(22, now); ok || hard {
		t.Fatalf("他副本软退避应进入镜像: ok=%v hard=%v", ok, hard)
	}
	if ok, _ := s.Eligible(5, now); !ok {
		t.Fatal("真相里不存在(已被他副本 resume)的账号不得留在镜像")
	}
	fp.listErr = errors.New("db down")
	fp.blocking = nil
	s.Reload(context.Background(), now)
	if ok, _ := s.Eligible(21, now); ok {
		t.Fatal("列表失败必须保留上次镜像")
	}
	fp.listErr = nil
	fp.blocking = []PersistedState{{AccountID: 31, HardDisabled: true}}
	ctx, cancel := context.WithCancel(context.Background())
	done := s.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if ok, _ := s.Eligible(31, now); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start 后应在首轮重载灌回真相")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后重载循环必须退出")
	}
	if fp.callCount("list") < 3 {
		t.Fatalf("重载次数=%d,期望至少 3(两次显式 + 至少一次 Start)", fp.callCount("list"))
	}
}

// 无持久化时 Start 立即返回已关闭通道,Reload 为 no-op,PersistenceKind 为 process_local。
func TestProcessLocalStoreHasNoReloader(t *testing.T) {
	s := NewStore(testCfg())
	select {
	case <-s.Start(context.Background()):
	case <-time.After(time.Second):
		t.Fatal("纯进程内车道的 Start 必须立即完成")
	}
	s.Reload(context.Background(), time.Now())
	if s.PersistenceKind() != "process_local" {
		t.Fatalf("PersistenceKind=%s", s.PersistenceKind())
	}
	var nilStore *Store
	if nilStore.PersistenceKind() != "process_local" {
		t.Fatal("nil Store 的 PersistenceKind 必须安全")
	}
	select {
	case <-nilStore.Start(context.Background()):
	case <-time.After(time.Second):
		t.Fatal("nil Store 的 Start 必须立即完成")
	}
}

// 读穿:配置了持久化时 Snapshot 读真相(他副本写入立即可见),读失败回退镜像。
func TestPersistentSnapshotReadsThroughTruth(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	until := now.Add(time.Minute)
	fp.rows[41] = PersistedState{AccountID: 41, Strike: 2, AuthUntil: until, CredentialVersion: 3}
	snap := s.Snapshot(context.Background(), 41, now)
	if !snap.Found || snap.Eligible || snap.Strike != 2 || snap.CredentialVersion != 3 || snap.AuthUntil == nil || !snap.AuthUntil.Equal(until) || snap.Persistence != "fake" {
		t.Fatalf("Snapshot 应读穿真相: %+v", snap)
	}
	if ok, _ := s.Eligible(41, now); !ok {
		t.Fatal("前置:镜像尚未重载,选号门本地仍放行(候选查询负责跨副本排除)")
	}
	fp.failNext = errors.New("db down")
	snap = s.Snapshot(context.Background(), 41, now)
	if snap.Found {
		t.Fatalf("读失败且镜像无条目时应返回未找到: %+v", snap)
	}
}

// TestReplayDoesNotUndoLaterOperatorResume:副本 A 写库失败留下本地硬禁;运营随后在别的副本 resume
// (真相表留下墓碑);A 的重载补写必须被墓碑拒绝并丢弃本地硬禁,不得把人工恢复再撤销。
// 判别:补写不带证据时刻或后端不比较墓碑 → 真相被重新硬禁,断言红。
func TestReplayDoesNotUndoLaterOperatorResume(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	fp.failNext = errors.New("db down")
	s.OnRefreshResult(context.Background(), 21, 0, false, true) // 本地硬禁,证据时刻 ≈ 现在
	if ok, hard := s.Eligible(21, now); ok || !hard {
		t.Fatalf("前置:本地硬禁: ok=%v hard=%v", ok, hard)
	}
	fp.resumedAt[21] = time.Now().Add(time.Minute) // 运营在别的副本 resume(晚于本地证据)
	s.Reload(context.Background(), now.Add(5*time.Second))
	if _, ok := fp.rows[21]; ok {
		t.Fatal("补写不得撤销更晚的运营 resume")
	}
	if ok, _ := s.Eligible(21, now.Add(5*time.Second)); !ok {
		t.Fatal("被墓碑拒绝的本地硬禁必须丢弃")
	}
}

// TestReloadDropsLocalSoftEntryOlderThanResume:他副本 resume 留下墓碑后,本副本因写失败持有的
// 更早软退避兜底在重载时作废,不再多挡一个退避窗口。判别:重载不读墓碑 → 本地条目保留,断言红。
func TestReloadDropsLocalSoftEntryOlderThanResume(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	fp.failNext = errors.New("db down")
	s.Suspend(context.Background(), 31, ClassAmbiguous, 1, now) // 本地软兜底,证据时刻 now
	if ok, _ := s.Eligible(31, now.Add(time.Second)); ok {
		t.Fatal("前置:本地软兜底应生效")
	}
	fp.resumedAt[31] = now.Add(2 * time.Second) // 他副本随后 resume
	s.Reload(context.Background(), now.Add(3*time.Second))
	if ok, _ := s.Eligible(31, now.Add(3*time.Second)); !ok {
		t.Fatal("晚于本地证据的 resume 墓碑必须让本地软兜底作废")
	}
	// 墓碑早于本地证据(先 resume 后失败)则本地兜底保留。
	fp.failNext = errors.New("db down")
	s.Suspend(context.Background(), 32, ClassAmbiguous, 1, now.Add(10*time.Second))
	fp.resumedAt[32] = now.Add(5 * time.Second)
	s.Reload(context.Background(), now.Add(11*time.Second))
	if ok, _ := s.Eligible(32, now.Add(11*time.Second)); ok {
		t.Fatal("早于本地证据的墓碑不得作废本地兜底")
	}
}

// TestSnapshotIgnoresStaleMirrorWhenTruthHasNoState:真相明确无状态时,镜像里来自真相的陈旧条目不作为
// 状态展示;仅本副本持有的兜底仍暴露;真相读不到时镜像条目以 process_local 暴露。
func TestSnapshotIgnoresStaleMirrorWhenTruthHasNoState(t *testing.T) {
	fp := newFakePersistence()
	s := NewPersistentStore(testCfg(), fp)
	now := time.Unix(1_000_000, 0)
	fp.blocking = []PersistedState{{AccountID: 41, Strike: 1, AuthUntil: now.Add(time.Hour)}}
	s.Reload(context.Background(), now)
	fp.blocking = nil // 他副本随即清除了该状态,本副本尚未重载
	snap := s.Snapshot(context.Background(), 41, now)
	if snap.Found {
		t.Fatalf("真相无状态时不得把陈旧镜像当状态: %+v", snap)
	}
	fp.failNext = errors.New("db down")
	s.OnRefreshResult(context.Background(), 42, 0, false, true) // 本地兜底
	snap = s.Snapshot(context.Background(), 42, now)
	if !snap.Found || snap.Persistence != "process_local" || !snap.HardDisabled {
		t.Fatalf("本地兜底必须暴露为 process_local: %+v", snap)
	}
	fp.blocking = []PersistedState{{AccountID: 43, HardDisabled: true}}
	s.Reload(context.Background(), now)
	fp.failNext = errors.New("db down")
	snap = s.Snapshot(context.Background(), 43, now)
	if !snap.Found || snap.Persistence != "process_local" {
		t.Fatalf("真相读不到时镜像条目应以 process_local 暴露: %+v", snap)
	}
}

// TestPersistWriteBudgetAndDetachedContext:阻塞的后端在写预算(2s)内返回并留下本地兜底;
// 请求上下文已取消时状态转换仍然写穿(状态转换不依赖客户端连接)。
func TestPersistWriteBudgetAndDetachedContext(t *testing.T) {
	if testing.Short() {
		t.Skip("需要真实等待写预算")
	}
	blocking := &blockingPersistence{inner: newFakePersistence()}
	s := NewPersistentStore(testCfg(), blocking)
	now := time.Unix(1_000_000, 0)
	started := time.Now()
	s.Suspend(context.Background(), 51, ClassIronClad, 1, now)
	elapsed := time.Since(started)
	if elapsed < persistTimeout || elapsed > persistTimeout+time.Second {
		t.Fatalf("写预算应约为 %v,实际 %v", persistTimeout, elapsed)
	}
	if ok, _ := s.Eligible(51, now.Add(time.Second)); ok {
		t.Fatal("后端超时后必须留下本地兜底")
	}
	fp := newFakePersistence()
	s2 := NewPersistentStore(testCfg(), fp)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s2.Suspend(cancelled, 52, ClassAmbiguous, 1, now)
	if _, ok := fp.rows[52]; !ok {
		t.Fatal("请求上下文已取消时状态转换仍必须写穿")
	}
	defaults := (Config{}).normalized()
	if defaults.ReloadInterval != 5*time.Second || defaults.Base != 30*time.Second || defaults.Cap != 30*time.Minute || defaults.HardDisableStrikeK != 3 {
		t.Fatalf("配置缺失必须取默认: %+v", defaults)
	}
}

// blockingPersistence 的写操作阻塞到上下文超时,用来验证写预算与本地兜底。
type blockingPersistence struct{ inner *fakePersistence }

func (b *blockingPersistence) Kind() string { return "blocking" }
func (b *blockingPersistence) RecordFailure(ctx context.Context, _ int64, _ FailureClass, _ int, _ time.Time, _ Config) (PersistedState, bool, error) {
	<-ctx.Done()
	return PersistedState{}, false, ctx.Err()
}
func (b *blockingPersistence) MarkHardDisabled(ctx context.Context, _ int64, _ int, _ string, _, _ time.Time) (PersistedState, bool, error) {
	<-ctx.Done()
	return PersistedState{}, false, ctx.Err()
}
func (b *blockingPersistence) Clear(ctx context.Context, _ int64, _ bool, _ time.Time) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}
func (b *blockingPersistence) Get(ctx context.Context, id int64) (PersistedState, bool, error) {
	return b.inner.Get(ctx, id)
}
func (b *blockingPersistence) List(ctx context.Context) ([]PersistedState, error) {
	return b.inner.List(ctx)
}
