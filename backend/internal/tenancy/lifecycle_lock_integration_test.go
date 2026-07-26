//go:build integration_pg

package tenancy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLockActiveForWriteSerializesTenantDisable(t *testing.T) {
	// 变异：把生产查询改成 FOR KEY SHARE，非键状态更新会立即完成，本测试在
	// “停用不得越过业务写锁”断言处转红。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)

	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"lifecycle-lock-"+uuid.NewString(),
	).Scan(&tenantID); err != nil {
		t.Fatalf("创建租户：%v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	writer, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("开始业务事务：%v", err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if err := LockActiveForWrite(ctx, writer, tenantID); err != nil {
		t.Fatalf("锁定活跃租户：%v", err)
	}

	disabled := make(chan error, 1)
	go func() {
		_, updateErr := pool.Exec(ctx,
			`UPDATE tenants SET status='disabled', updated_at=now() WHERE id=$1`,
			tenantID,
		)
		disabled <- updateErr
	}()

	select {
	case updateErr := <-disabled:
		_ = writer.Rollback(ctx)
		t.Fatalf("业务事务未结束时停用越过 SHARE 锁：%v", updateErr)
	case <-time.After(150 * time.Millisecond):
	}

	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("提交业务事务：%v", err)
	}
	select {
	case updateErr := <-disabled:
		if updateErr != nil {
			t.Fatalf("业务事务结束后停用：%v", updateErr)
		}
	case <-ctx.Done():
		t.Fatalf("业务事务结束后停用仍阻塞：%v", ctx.Err())
	}

	afterDisable, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("开始停用后事务：%v", err)
	}
	defer func() { _ = afterDisable.Rollback(context.Background()) }()
	if err := LockActiveForWrite(ctx, afterDisable, tenantID); !errors.Is(err, ErrTenantInactive) {
		t.Fatalf("停用后锁定错误=%v，期望 ErrTenantInactive", err)
	}
}
