package accounthealthprobe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
)

// fakeProbeStore 记录每次 TouchProviderAccountProbe 调用的参数,供断言。
// 关键:它不是恒绿桩——它精确记录被写的参数,从而当 probe 被改回 nil(死开关复发)
// 或写错列/写错账号时,断言能立刻 red(满足 mutation 自检)。
type fakeProbeStore struct {
	calls []admin.TouchProviderAccountProbeParams
	err   error // 注入 DB 出错,验证 error 透传给 eventbus(走 DLQ)
}

func (f *fakeProbeStore) TouchProviderAccountProbe(_ context.Context, arg admin.TouchProviderAccountProbeParams) error {
	f.calls = append(f.calls, arg)
	return f.err
}

// TestProbeWritesRequestObservation 是接线与时间语义的回归守卫:
// handler 必须落一次写,使用请求完成事件自己的 CreatedAt,不能用队列消费时间。
//
// mutation 自检:
//   - 把 middleware.go 的 probe 改回 nil → handler 进入 if probe==nil 空转分支
//     → store.calls 为空 → 本测试 red。
//   - 把 TouchProviderAccountProbe 的 UPDATE 写成别的列 / 别的账号 → 参数断言 red。
func TestProbeWritesRequestObservation(t *testing.T) {
	store := &fakeProbeStore{}
	probe := NewPostgresProbe(store)

	// 用真实生产构造器把 probe 装进 handler,避免手搓 fake 绕过接线给假绿。
	handler := observability.NewAccountHealthProbeHandler(time.Second, probe)

	const wantAccount, wantTenant = int64(4242), int64(7)
	wantObservedAt := time.Date(2026, 7, 16, 18, 0, 0, 123, time.FixedZone("source", 8*60*60))
	if err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{
		ID:        "evt-1",
		TenantID:  wantTenant,
		AccountID: wantAccount,
		CreatedAt: wantObservedAt,
	}); err != nil {
		t.Fatalf("handler.Handle 返回错误: %v", err)
	}

	if len(store.calls) != 1 {
		t.Fatalf("期望 probe 落 1 次写, 实得 %d 次(死开关复发?)", len(store.calls))
	}
	got := store.calls[0]
	if got.ID != wantAccount {
		t.Errorf("写到错误账号: got id=%d want %d", got.ID, wantAccount)
	}
	if got.TenantID != wantTenant {
		t.Errorf("写到错误租户: got tenant=%d want %d", got.TenantID, wantTenant)
	}
	if !got.ProbedAt.Valid {
		t.Errorf("被动请求观测时间未写入(Valid=false)")
	}
	if !got.ProbedAt.Time.Equal(wantObservedAt.UTC()) {
		t.Errorf("观测时间=%s want event.CreatedAt=%s", got.ProbedAt.Time, wantObservedAt.UTC())
	}
}

// TestProbeSkipsWhenNoAccount 确认无有效账号(AccountID<=0)时不发空写,
// 避免对不存在/未获取的账号发无意义 UPDATE。
func TestProbeSkipsWhenNoAccount(t *testing.T) {
	store := &fakeProbeStore{}
	probe := NewPostgresProbe(store)
	if err := probe(context.Background(), observability.AccountHealthSignal{
		TenantID:  7,
		AccountID: 0, // 无账号
		At:        time.Now(),
	}); err != nil {
		t.Fatalf("probe 返回错误: %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("无账号时不应写, 实得 %d 次", len(store.calls))
	}
}

// TestProbeErrorPropagatesToEventbus 验证 DB 出错时 error 被透传回 eventbus
// (从而走 DLQ),而不是被吞掉。配合 handler.Critical()==false,这保证 probe
// 失败不会反压 / 影响请求转发,但仍可被 DLQ 兜底重试。
func TestProbeErrorPropagatesToEventbus(t *testing.T) {
	dbErr := errors.New("db boom")
	store := &fakeProbeStore{err: dbErr}
	handler := observability.NewAccountHealthProbeHandler(time.Second, NewPostgresProbe(store))

	err := handler.Handle(context.Background(), eventbus.RequestCompletionEvent{
		ID:        "evt-2",
		TenantID:  7,
		AccountID: 4242,
	})
	if !errors.Is(err, dbErr) {
		t.Fatalf("DB 错误未透传给 eventbus: got %v", err)
	}
	// 该 handler 非 critical,error 走 DLQ 不影响请求路径。
	if handler.Critical() {
		t.Fatalf("account_health_probe handler 不应是 critical, 否则 probe 失败会影响请求")
	}
}

// TestNilStoreReturnsErrorNotNilProbe 守住一个反模式:store 为 nil 时
// NewPostgresProbe 必须返回"返回错误的 probe",而不是 nil。返回 nil 会让 handler
// 退化成原 bug 的空转,接线缺失却悄悄静默。
func TestNilStoreReturnsErrorNotNilProbe(t *testing.T) {
	probe := NewPostgresProbe(nil)
	if probe == nil {
		t.Fatal("store=nil 时不应返回 nil probe(会退化成死开关空转)")
	}
	if err := probe(context.Background(), observability.AccountHealthSignal{AccountID: 1, TenantID: 1, At: time.Now()}); !errors.Is(err, ErrNoStore) {
		t.Fatalf("store=nil 时 probe 应返回 ErrNoStore, got %v", err)
	}
}
