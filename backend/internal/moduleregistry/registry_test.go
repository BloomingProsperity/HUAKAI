package moduleregistry

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRegisterGetListByCategory —— 演练核心的类 CRUD 接口面。
// 它能捕获的回归:若 List/ListByCategory 停止过滤或排序(例如 ListByCategory
// 不分 cat 地返回每个模块),category 计数断言会转红。
func TestRegisterGetListByCategory(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "billing.service", Category: "money-path", Title: "Billing"})
	mustRegister(t, r, ModuleDescriptor{ID: "routing.selector", Category: "routing", Title: "Selector"})
	mustRegister(t, r, ModuleDescriptor{ID: "credentials.worker", Category: "credentials", Title: "Cred worker"})

	if got, ok := r.Get("billing.service"); !ok || got.Title != "Billing" {
		t.Fatalf("Get(billing.service) = %+v ok=%v; want Billing", got, ok)
	}
	if _, ok := r.Get("does.not.exist"); ok {
		t.Fatalf("Get(missing) returned ok=true")
	}
	if all := r.List(); len(all) != 3 {
		t.Fatalf("List len=%d want 3", len(all))
	}
	// List 必须按 ID 排序 —— billing < credentials < routing。
	all := r.List()
	if all[0].ID != "billing.service" || all[2].ID != "routing.selector" {
		t.Fatalf("List not sorted by ID: %v", idsOf(all))
	}
	money := r.ListByCategory("money-path")
	if len(money) != 1 || money[0].ID != "billing.service" {
		t.Fatalf("ListByCategory(money-path)=%v want [billing.service]", idsOf(money))
	}
	if got := r.ListByCategory("nope"); len(got) != 0 {
		t.Fatalf("ListByCategory(nope)=%v want empty", idsOf(got))
	}
}

// TestRegisterEmptyIDRejected —— 空 ID 是编程错误。
// 回归:若 Register 停止守卫 ID=="",registry 会持有一个无法寻址的模块,
// 此处会返回 nil 而非 ErrEmptyID -> 转红。
func TestRegisterEmptyIDRejected(t *testing.T) {
	r := New()
	if err := r.Register(ModuleDescriptor{ID: "", Title: "ghost"}); err != ErrEmptyID {
		t.Fatalf("Register(empty ID) err=%v want ErrEmptyID", err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("empty-ID descriptor was stored")
	}
}

// TestRegisterDupIDLastWins —— 文档化的重复策略是后者胜出/幂等。
// 回归:若 Register 被改成前者胜出(忽略重新注册),Title 仍会读到旧值,
// 此断言转红;若被改成对重复报错,则会触发 err!=nil 分支。两种偏离都能被捕获。
func TestRegisterDupIDLastWins(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "billing.service", Title: "old"})
	if err := r.Register(ModuleDescriptor{ID: "billing.service", Title: "new"}); err != nil {
		t.Fatalf("re-register same ID err=%v want nil (idempotent last-wins)", err)
	}
	if len(r.List()) != 1 {
		t.Fatalf("dup ID created a second entry: %d", len(r.List()))
	}
	got, _ := r.Get("billing.service")
	if got.Title != "new" {
		t.Fatalf("dup ID Title=%q want %q (last-wins)", got.Title, "new")
	}
}

// TestSnapshotNoProbeIsUnknown —— 没有探针的模块是 "unknown",而非
// "error" 或 "ok"。回归:若 runProbe/Snapshot 把无探针模块默认成 StatusOK,
// 运维人员会看到假健康,此处转红。
func TestSnapshotNoProbeIsUnknown(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "x", Title: "x"}) // 无探针
	snaps := r.Snapshot(context.Background())
	if len(snaps) != 1 {
		t.Fatalf("snap len=%d want 1", len(snaps))
	}
	if snaps[0].Probe.Status != StatusUnknown {
		t.Fatalf("no-probe status=%q want unknown", snaps[0].Probe.Status)
	}
}

// TestSnapshotSlowProbeTimesOutToUnknown —— 核心的超时保证。阻塞远超每探针
// 超时的探针必须解析为 "unknown",且不挂起 snapshot。
// 回归:若 runProbe 等待探针而非让它与超时赛跑(删掉 `case <-ctx.Done()`
// 分支),Snapshot 会阻塞约 2s,此测试自身类似 1s 截止的断言 + 状态断言都
// 会转红。
func TestSnapshotSlowProbeTimesOutToUnknown(t *testing.T) {
	r := NewWithProbeTimeout(50 * time.Millisecond)
	slowStarted := make(chan struct{})
	mustRegister(t, r, ModuleDescriptor{
		ID:    "slow",
		Title: "slow",
		HealthProbe: func(ctx context.Context) ProbeResult {
			close(slowStarted)
			// 阻塞远超 50ms 的探针超时;尊重 ctx,这样 goroutine 不会真正泄漏。
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
			return ProbeResult{Status: StatusOK} // 若真返回则是谎报
		},
	})

	start := time.Now()
	snaps := r.Snapshot(context.Background())
	elapsed := time.Since(start)

	<-slowStarted // 探针确实运行了
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Snapshot blocked %v on slow probe; want ~timeout (50ms), not a hang", elapsed)
	}
	if snaps[0].Probe.Status != StatusUnknown {
		t.Fatalf("slow-probe status=%q want unknown (timed out), got=%+v", snaps[0].Probe.Status, snaps[0].Probe)
	}
}

// TestSnapshotRunsProbesConcurrently —— N 个各睡 D 的探针必须在远低于 N*D 的
// 墙钟时间内完成,证明它们是并行而非串行运行。
// 回归:若 Snapshot 串行运行探针(移除 goroutine / wg),4 个探针 * 60ms 会
// 耗时 >240ms 并超过 200ms 上限 -> 转红。
func TestSnapshotRunsProbesConcurrently(t *testing.T) {
	const n = 4
	const each = 60 * time.Millisecond
	r := NewWithProbeTimeout(2 * time.Second) // 宽松:此处不测试超时
	var ran int32
	for i := 0; i < n; i++ {
		mustRegister(t, r, ModuleDescriptor{
			ID:    string(rune('a' + i)),
			Title: "p",
			HealthProbe: func(ctx context.Context) ProbeResult {
				atomic.AddInt32(&ran, 1)
				time.Sleep(each)
				return ProbeResult{Status: StatusOK}
			},
		})
	}
	start := time.Now()
	snaps := r.Snapshot(context.Background())
	elapsed := time.Since(start)

	if atomic.LoadInt32(&ran) != n {
		t.Fatalf("ran=%d probes want %d", ran, n)
	}
	if elapsed >= n*each {
		t.Fatalf("Snapshot took %v >= serial bound %v; probes did not run concurrently", elapsed, n*each)
	}
	for _, s := range snaps {
		if s.Probe.Status != StatusOK {
			t.Fatalf("probe %s status=%q want ok", s.Descriptor.ID, s.Probe.Status)
		}
	}
}

// TestSnapshotProbePanicBecomesError —— panic 的探针绝不能让运维路径崩溃;
// 它会降级为 StatusError。
// 回归:移除 runProbe 中的 recover(),此测试会让测试二进制 panic,而非断言
// StatusError -> 转红(崩溃)。
func TestSnapshotProbePanicBecomesError(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{
		ID:          "boom",
		Title:       "boom",
		HealthProbe: func(ctx context.Context) ProbeResult { panic("kaboom") },
	})
	snaps := r.Snapshot(context.Background())
	if snaps[0].Probe.Status != StatusError {
		t.Fatalf("panicking probe status=%q want error", snaps[0].Probe.Status)
	}
}

func TestActivationSnapshotJSONOmitsNilAndKeepsAdditiveContract(t *testing.T) {
	d := ModuleDescriptor{
		ID:       "routing.selector",
		Category: "routing",
		Title:    "Selector",
		Activation: &ActivationSnapshot{
			Declared:    boolPtr(true),
			Constructed: boolPtr(true),
			Mode:        "canary",
			Endpoints: []ActivationEndpoint{
				{Name: "chat", Injected: boolPtr(true)},
				{Name: "images"},
			},
		},
	}

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"activation":{"declared":true,"constructed":true,"mode":"canary","endpoints":[{"name":"chat","injected":true},{"name":"images"}]}`) {
		t.Fatalf("activation JSON = %s", got)
	}
	if strings.Contains(got, `"active"`) || strings.Contains(got, `"shared_safe"`) || strings.Contains(got, `"traffic_percent"`) {
		t.Fatalf("nil activation fields should be omitted: %s", got)
	}
}

func mustRegister(t *testing.T, r *Registry, d ModuleDescriptor) {
	t.Helper()
	if err := r.Register(d); err != nil {
		t.Fatalf("Register(%s): %v", d.ID, err)
	}
}

func idsOf(ds []ModuleDescriptor) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func boolPtr(v bool) *bool { return &v }
