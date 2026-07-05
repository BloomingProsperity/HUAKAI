// HUAKAI · iKun

// StormController 失败路径判别测试 (DBTX 桩): current_in_flight 是持久计数器且
// cap 默认 1 —— acquire 半途出错不补偿 / release 一次瞬时失败被吞, 都等于该账号
// 永久无法刷新(死号)。

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// stormDBStub 按 SQL 片段路由行为的 dbauth.DBTX 桩。
type stormDBStub struct {
	tryAcquireErr error   // TryAcquire 的 Scan 返回错 (模拟 +1 提交后回读失败)
	releaseErrs   []error // Release Exec 依次弹出; 耗尽后 nil
	releaseCalls  int
}

type stormRowStub struct {
	fill func(dest ...any) error
}

func (r stormRowStub) Scan(dest ...any) error { return r.fill(dest...) }

func (s *stormDBStub) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "GREATEST(current_in_flight - 1") {
		s.releaseCalls++
		if len(s.releaseErrs) > 0 {
			err := s.releaseErrs[0]
			s.releaseErrs = s.releaseErrs[1:]
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.CommandTag{}, nil
}

func (s *stormDBStub) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (s *stormDBStub) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	if strings.Contains(sql, "current_in_flight + 1") { // TryAcquire
		return stormRowStub{fill: func(dest ...any) error {
			if s.tryAcquireErr != nil {
				return s.tryAcquireErr
			}
			*(dest[0].(*int32)) = 1
			return nil
		}}
	}
	// GetOrCreateAccountStormBudget: 按列型填默认值。
	return stormRowStub{fill: func(dest ...any) error {
		for _, d := range dest {
			switch v := d.(type) {
			case *int64:
				*v = 1
			case *int32:
				*v = 1
			case *string:
				*v = "account"
			case **int64, **string:
				// 可空列保持 nil
			case *pgtype.Timestamptz:
				*v = pgtype.Timestamptz{Valid: true}
			}
		}
		return nil
	}}
}

// TestAcquireScanErrorCompensatesRelease 守 A#6: TryAcquire 的 +1 与回读非原子,
// 回读失败时调用方拿不到 release 闭包, 必须就地补偿 -1 (Release 带 GREATEST 钳位,
// +1 未提交时补偿净安全)。
// mutation: Acquire 错误分支去掉 releaseSlotWithRetry 调用 → releaseCalls=0 → 红。
func TestAcquireScanErrorCompensatesRelease(t *testing.T) {
	stub := &stormDBStub{tryAcquireErr: errors.New("conn reset during scan")}
	c := NewStormController(dbauth.New(stub))
	release, _, err := c.Acquire(context.Background(), 1, 42)
	if err == nil || release != nil {
		t.Fatalf("acquire err=%v releaseNil=%v, want error + nil release", err, release == nil)
	}
	if stub.releaseCalls < 1 {
		t.Fatal("回读失败未补偿 -1 —— +1 已提交时该账号槽位永久泄漏 (cap=1 即死号)")
	}
}

// TestReleaseRetriesTransientFailure 守 A#5: release 吞错 + sync.Once = 一次瞬时
// DB 失败即永久泄漏。必须带重试; 两次瞬时失败后第三次成功。
// mutation: releaseSlotWithRetry 退回单次尝试吞错 → releaseCalls=1 → 红。
func TestReleaseRetriesTransientFailure(t *testing.T) {
	stub := &stormDBStub{releaseErrs: []error{errors.New("blip1"), errors.New("blip2")}}
	c := NewStormController(dbauth.New(stub))
	release, outcome, err := c.Acquire(context.Background(), 1, 42)
	if err != nil || outcome != "" || release == nil {
		t.Fatalf("acquire: err=%v outcome=%q releaseNil=%v", err, outcome, release == nil)
	}
	release()
	if stub.releaseCalls != 3 {
		t.Fatalf("release 尝试 %d 次, want 3 (两败一成) —— 单次吞错即永久泄漏", stub.releaseCalls)
	}
	// 幂等: 再调不加计。
	release()
	if stub.releaseCalls != 3 {
		t.Fatalf("release 非幂等: %d", stub.releaseCalls)
	}
}
