package rate

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresAccountErrorRulesProviderCachesAndReturnsDefensiveCopies(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	queryer := &accountPolicyQueryStub{data: accountPolicyRowData{
		tempEnabled:   true,
		rulesRaw:      []byte(`[{"rule_id":"busy-503","error_code":503,"keywords":["busy"],"duration_minutes":5,"client_status":422,"client_code":"account_busy","message_mode":"custom","client_message":"账号暂不可用","affect_health":false}]`),
		customEnabled: true,
		customCodes:   []int32{429},
		poolMode:      true,
	}}
	provider := newPostgresAccountErrorRulesProvider(queryer, func() time.Time { return now }, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	first := provider.GetAccountErrorPolicy(44)
	if queryer.callCount() != 1 || len(first.Rules) != 1 || first.Rules[0].RuleID != "busy-503" {
		t.Fatalf("首次加载=%+v calls=%d", first, queryer.callCount())
	}
	first.Rules[0].Keywords[0] = "被调用方篡改"
	*first.Rules[0].ClientStatus = 499
	*first.Rules[0].AffectHealth = true
	first.CustomErrorCodes[0] = 500

	second := provider.GetAccountErrorPolicy(44)
	if queryer.callCount() != 1 {
		t.Fatalf("新鲜缓存仍查询数据库: calls=%d", queryer.callCount())
	}
	if second.Rules[0].Keywords[0] != "busy" || *second.Rules[0].ClientStatus != 422 || *second.Rules[0].AffectHealth || second.CustomErrorCodes[0] != 429 {
		t.Fatalf("缓存被调用方修改污染: %+v", second)
	}

	now = now.Add(accountErrorPolicyFreshTTL + time.Millisecond)
	queryer.setData(accountPolicyRowData{customEnabled: true, customCodes: []int32{529}})
	third := provider.GetAccountErrorPolicy(44)
	if queryer.callCount() != 2 || len(third.Rules) != 0 || len(third.CustomErrorCodes) != 1 || third.CustomErrorCodes[0] != 529 {
		t.Fatalf("过期缓存未刷新: policy=%+v calls=%d", third, queryer.callCount())
	}
}

func TestPostgresAccountErrorRulesProviderCollapsesConcurrentMisses(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 10, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	queryer := &accountPolicyQueryStub{
		data:        accountPolicyRowData{customEnabled: true, customCodes: []int32{429}},
		scanStarted: started,
		scanRelease: release,
	}
	provider := newPostgresAccountErrorRulesProvider(queryer, func() time.Time { return now }, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	results := make(chan AccountErrorPolicy, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			results <- provider.GetAccountErrorPolicy(44)
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("数据库加载未开始")
	}
	close(release)
	wg.Wait()
	close(results)

	if queryer.callCount() != 1 {
		t.Fatalf("并发冷缓存查询=%d want 1", queryer.callCount())
	}
	for result := range results {
		if len(result.CustomErrorCodes) != 1 || result.CustomErrorCodes[0] != 429 {
			t.Fatalf("并发结果错误: %+v", result)
		}
	}
}

func TestPostgresAccountErrorRulesProviderServesBoundedStaleOnDependencyFailure(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 20, 0, 0, time.UTC)
	queryer := &accountPolicyQueryStub{data: accountPolicyRowData{customEnabled: true, customCodes: []int32{429}}}
	var logs bytes.Buffer
	provider := newPostgresAccountErrorRulesProvider(queryer, func() time.Time { return now }, slog.New(slog.NewJSONHandler(&logs, nil)))

	if got := provider.GetAccountErrorPolicy(44); len(got.CustomErrorCodes) != 1 {
		t.Fatalf("测试前提加载失败: %+v", got)
	}
	const sensitiveMarker = "sk-db-error-must-not-log"
	queryer.setError(errors.New("database unavailable " + sensitiveMarker))
	now = now.Add(accountErrorPolicyFreshTTL + time.Millisecond)

	stale := provider.GetAccountErrorPolicy(44)
	if len(stale.CustomErrorCodes) != 1 || stale.CustomErrorCodes[0] != 429 || queryer.callCount() != 2 {
		t.Fatalf("依赖失败未返回陈旧策略: %+v calls=%d", stale, queryer.callCount())
	}
	if !strings.Contains(logs.String(), `"event_type":"upstream_error_policy.load_failed"`) ||
		!strings.Contains(logs.String(), `"served_stale_policy":true`) {
		t.Fatalf("依赖失败日志缺结构化字段: %s", logs.String())
	}
	if strings.Contains(logs.String(), sensitiveMarker) {
		t.Fatalf("依赖失败日志泄漏原始错误: %s", logs.String())
	}

	_ = provider.GetAccountErrorPolicy(44)
	if queryer.callCount() != 2 {
		t.Fatalf("错误退避窗口内重复查询: calls=%d", queryer.callCount())
	}
	now = now.Add(accountErrorPolicyStaleTTL + time.Second)
	zero := provider.GetAccountErrorPolicy(44)
	if len(zero.Rules) != 0 || len(zero.CustomErrorCodes) != 0 || zero.PoolMode || queryer.callCount() != 3 {
		t.Fatalf("陈旧上限后未 fail-open: policy=%+v calls=%d", zero, queryer.callCount())
	}
}

type accountPolicyRowData struct {
	tempEnabled   bool
	rulesRaw      []byte
	customEnabled bool
	customCodes   []int32
	poolMode      bool
}

type accountPolicyQueryStub struct {
	mu          sync.Mutex
	calls       int
	data        accountPolicyRowData
	err         error
	scanStarted chan struct{}
	scanRelease <-chan struct{}
	startOnce   sync.Once
}

func (q *accountPolicyQueryStub) QueryRow(context.Context, string, ...any) pgx.Row {
	q.mu.Lock()
	q.calls++
	data := q.data
	err := q.err
	started := q.scanStarted
	release := q.scanRelease
	q.mu.Unlock()
	return accountPolicyRow{data: data, err: err, started: started, release: release, startOnce: &q.startOnce}
}

func (q *accountPolicyQueryStub) callCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls
}

func (q *accountPolicyQueryStub) setData(data accountPolicyRowData) {
	q.mu.Lock()
	q.data = data
	q.err = nil
	q.mu.Unlock()
}

func (q *accountPolicyQueryStub) setError(err error) {
	q.mu.Lock()
	q.err = err
	q.mu.Unlock()
}

type accountPolicyRow struct {
	data      accountPolicyRowData
	err       error
	started   chan struct{}
	release   <-chan struct{}
	startOnce *sync.Once
}

func (r accountPolicyRow) Scan(dest ...any) error {
	if r.started != nil && r.startOnce != nil {
		r.startOnce.Do(func() { close(r.started) })
	}
	if r.release != nil {
		<-r.release
	}
	if r.err != nil {
		return r.err
	}
	if len(dest) != 5 {
		return errors.New("测试行接收字段数量错误")
	}
	*(dest[0].(*bool)) = r.data.tempEnabled
	*(dest[1].(*[]byte)) = append([]byte(nil), r.data.rulesRaw...)
	*(dest[2].(*bool)) = r.data.customEnabled
	*(dest[3].(*[]int32)) = append([]int32(nil), r.data.customCodes...)
	*(dest[4].(*bool)) = r.data.poolMode
	return nil
}
