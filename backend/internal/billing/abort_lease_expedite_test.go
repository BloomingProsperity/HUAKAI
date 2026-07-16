package billing

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbbillingrecovery "github.com/BloomingProsperity/HUAKAI/internal/db/billingrecovery"
)

// TestExpediteAbortLeaseQuery_ATCD3005_GuardsOnlyLease 固定恢复写只能按租户、
// claim、attempt 与 reserving 状态缩短 lease，不能顺手改终态、hold 或金额。
func TestExpediteAbortLeaseQuery_ATCD3005_GuardsOnlyLease(t *testing.T) {
	db := &capturingBillingExecDB{tag: pgconn.NewCommandTag("UPDATE 1")}
	queries := dbbillingrecovery.New(db)

	rows, err := queries.ExpediteAbortLease(context.Background(), dbbillingrecovery.ExpediteAbortLeaseParams{
		TenantID:   17,
		ClaimID:    29,
		AttemptSeq: 3,
	})
	if err != nil || rows != 1 {
		t.Fatalf("ExpediteAbortLease rows/err=%d/%v，want 1/nil", rows, err)
	}
	for _, clause := range []string{
		"SET lease_expires_at = LEAST(lease_expires_at, NOW())",
		"WHERE tenant_id = $1::bigint",
		"AND id = $2::bigint",
		"AND attempt_seq = $3::integer",
		"AND status = 'reserving'",
	} {
		if !strings.Contains(db.query, clause) {
			t.Fatalf("query 缺少 %q：\n%s", clause, db.query)
		}
	}
	setClause := strings.SplitN(db.query, "WHERE", 2)[0]
	for _, forbidden := range []string{"status =", "actual_cost", "balance_holds", "billing_events", "held ="} {
		if strings.Contains(setClause, forbidden) {
			t.Fatalf("lease 加速 SET 子句不应包含 %q：\n%s", forbidden, setClause)
		}
	}
	if len(db.args) != 3 || db.args[0] != int64(17) || db.args[1] != int64(29) || db.args[2] != int32(3) {
		t.Fatalf("query args=%v，want [17 29 3]", db.args)
	}
}

// TestAbortRetryExhaustion_ATCD3004_CallsLeaseExpedite 证明恢复入口实际执行
// guarded UPDATE；只保留 helper 或 SQL、却从 Abort 耗尽路径断开调用时，本测试会变红。
func TestAbortRetryExhaustion_ATCD3004_CallsLeaseExpedite(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	primaryErr := fakePgErr("40P01")
	db := &capturingBillingExecDB{tag: pgconn.NewCommandTag("UPDATE 1")}
	settler := &DefaultSettler{abortRecoveryQ: dbbillingrecovery.New(db)}

	err := settler.expediteAbortLeaseAfterConflict(requestCtx, 31, 47, abortClaimGeneration{attemptSeq: 5, observed: true}, primaryErr)

	if err != primaryErr {
		t.Fatalf("返回错误=%v，want 原始冲突指针 %v", err, primaryErr)
	}
	if !strings.Contains(db.query, "SET lease_expires_at = LEAST(lease_expires_at, NOW())") {
		t.Fatalf("未执行 lease 加速 query：%q", db.query)
	}
	if len(db.args) != 3 || db.args[0] != int64(31) || db.args[1] != int64(47) || db.args[2] != int32(5) {
		t.Fatalf("query args=%v，want [31 47 5]", db.args)
	}
	if db.ctxErr != nil || !db.hasDeadline {
		t.Fatalf("cleanup ctx err/deadline=%v/%v，want nil/true", db.ctxErr, db.hasDeadline)
	}
}

// TestAbortRetryExhaustion_ATCD3005_PreservesPrimaryErrorWhenExpediteFails
// 证明请求已经取消时清理仍获得一秒有界上下文，同时清理错误只记观测、不覆盖主错误。
func TestAbortRetryExhaustion_ATCD3005_PreservesPrimaryErrorWhenExpediteFails(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	primaryErr := fakePgErr("40001")
	db := &capturingBillingExecDB{err: &pgconn.PgError{Code: "55P03"}}
	settler := &DefaultSettler{abortRecoveryQ: dbbillingrecovery.New(db)}
	before := billingAbortMetricValue("40001", "expedite_failed")

	err := settler.expediteAbortLeaseAfterConflict(requestCtx, 17, 29, abortClaimGeneration{attemptSeq: 7, observed: true}, primaryErr)

	if err != primaryErr {
		t.Fatalf("返回错误=%v，want 原始冲突指针 %v", err, primaryErr)
	}
	if db.ctxErr != nil {
		t.Fatalf("加速上下文 err=%v，want 脱离已取消请求", db.ctxErr)
	}
	if !db.hasDeadline {
		t.Fatal("加速上下文缺少一秒 timeout")
	}
	if left := time.Until(db.deadline); left <= 0 || left > time.Second {
		t.Fatalf("加速上下文剩余=%v，want (0,1s]", left)
	}
	if got := billingAbortMetricValue("40001", "expedite_failed") - before; got != 1 {
		t.Fatalf("expedite_failed metric delta=%d，want 1", got)
	}
}

// TestAbortRetryExhaustion_NoClaimReadSkipsLeaseExpedite 固定九次尝试都未成功
// 读到 claim 时安全无为；若凭 tenant+claim 猜代际，恢复写会误伤后继 attempt。
func TestAbortRetryExhaustion_NoClaimReadSkipsLeaseExpedite(t *testing.T) {
	primaryErr := fakePgErr("40001")
	db := &capturingBillingExecDB{tag: pgconn.NewCommandTag("UPDATE 1")}
	settler := &DefaultSettler{abortRecoveryQ: dbbillingrecovery.New(db)}

	err := settler.expediteAbortLeaseAfterConflict(context.Background(), 17, 29, abortClaimGeneration{}, primaryErr)

	if err != primaryErr {
		t.Fatalf("返回错误=%v，want 原始冲突指针 %v", err, primaryErr)
	}
	if db.query != "" || len(db.args) != 0 {
		t.Fatalf("未读到 claim 却执行恢复 query=%q args=%v", db.query, db.args)
	}
}

func billingAbortMetricValue(sqlState, outcome string) int64 {
	return billingAbortMetricValueWithAttempts(sqlState, outcome, 0)
}

func billingAbortMetricValueWithAttempts(sqlState, outcome string, attempts int) int64 {
	metric := expvar.Get("billing_abort")
	m, ok := metric.(*expvar.Map)
	if !ok || m == nil {
		return 0
	}
	key := "sqlstate=" + sqlState + "|outcome=" + outcome
	if attempts > 0 {
		key += fmt.Sprintf("|attempts=%d", attempts)
	}
	value, ok := m.Get(key).(*expvar.Int)
	if !ok || value == nil {
		return 0
	}
	return value.Value()
}

type capturingBillingExecDB struct {
	query       string
	args        []any
	tag         pgconn.CommandTag
	err         error
	ctxErr      error
	deadline    time.Time
	hasDeadline bool
}

func (d *capturingBillingExecDB) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	d.query = query
	d.args = append([]any(nil), args...)
	d.ctxErr = ctx.Err()
	d.deadline, d.hasDeadline = ctx.Deadline()
	return d.tag, d.err
}

func (d *capturingBillingExecDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (d *capturingBillingExecDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return failingBillingRow{}
}

type failingBillingRow struct{}

func (failingBillingRow) Scan(...interface{}) error {
	return errors.New("unexpected QueryRow")
}
