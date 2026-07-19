package channelhealth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

// b10ContendedRamp 模拟并发选号中"竞争落败者":到期冷却账号的机会性 ramp-start 是
// Serializable 写(无 40001 重试),同时评估同一账号的多个选号只有一个能提交,其余收到 40001。
type b10ContendedRamp struct{ err error }

func (r b10ContendedRamp) MaybeStartRamp(context.Context, ChannelKey) (Record, error) {
	return Record{}, r.err
}

// b10ExpiredCoolingStore 恒返回一条"冷却已到期"的 cooling_down 记录(即正在自动恢复的账号)。
type b10ExpiredCoolingStore struct{ rec Record }

func (s b10ExpiredCoolingStore) LatestByProviderAccount(context.Context, int64, int64) (Record, error) {
	return s.rec, nil
}

// B10:健康 gate 在读路径(Allow)对每个候选做未重试的 SERIALIZABLE 写(MaybeStartRamp)。
// 当某到期冷却账号被多个并发选号同时评估时,竞争落败者收到 40001,Allow 便把 gate 错误上抛;
// DefaultSelector.filter / PASR.allowAccount 一律把 gate 错误当作"排除此账号"。若它是唯一可选
// 账号,调用方拿到 spurious NoCapacity,正在恢复的账号在负载下持续抖动。
//
// 正确行为:序列化竞争(40001/40P01)是良性的——胜者已把记录翻成 ramping;落败者不应因此把
// 正在恢复的账号误判为不可用,应交由 IsEligible 的"到期冷却→放行"闸门放行。
//
// 变异/证伪:当前 maybeStartExpiredRamp 直接上抛 MaybeStartRamp 的 error → 本测试 RED。
func TestChannelHealth_B10_RampContentionDoesNotExcludeRecoveringAccount(t *testing.T) {
	past := time.Now().Add(-time.Minute) // 冷却已到期:账号正在自动恢复
	rec := Record{
		Key:           ChannelKey{TenantID: 7, ProviderAccountID: 101},
		State:         StateCoolingDown,
		CooldownUntil: &past,
	}
	gate := &PoolGate{
		store: b10ExpiredCoolingStore{rec: rec},
		ramp:  b10ContendedRamp{err: &pgconn.PgError{Code: "40001"}}, // 竞争落败者
		clock: realClock{},
	}

	ok, why, err := gate.Allow(context.Background(),
		&pool.AccountSnapshot{ID: 101, TenantID: 7},
		pool.SelectionRequest{TenantID: 7, RequestedModel: "m"})

	if err != nil {
		t.Fatalf("B10: 序列化竞争(40001)不应作为 gate 错误上抛(会致账号被 spurious 排除→NoCapacity 抖动);err=%v why=%s", err, why)
	}
	if !ok {
		t.Fatalf("B10: ramp 竞争落败后,正在恢复的到期冷却账号应仍放行(交由 IsEligible),实得排除 why=%s", why)
	}

	// 边界:非序列化错误(真实 DB 故障)仍须上抛,保持既有保守语义,不得被一并吞掉。
	dbErr := context.DeadlineExceeded
	gate.ramp = b10ContendedRamp{err: dbErr}
	if _, _, err := gate.Allow(context.Background(),
		&pool.AccountSnapshot{ID: 101, TenantID: 7},
		pool.SelectionRequest{TenantID: 7, RequestedModel: "m"}); err == nil {
		t.Fatal("B10: 非序列化错误应继续上抛(保守语义),不得被吞掉")
	}
}
