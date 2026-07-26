//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMediaSubmissionRecoveryMigrationHandlesPopulatedUpgrade(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()

	t.Run("历史任务号无冲突时升级并启用待结算态", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(
			t, ctx, baseDSN, "huakai_media_recovery_clean_upgrade",
		)
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(231); err != nil {
			t.Fatalf("迁移到 0231: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接 0231 临时库: %v", err)
		}
		defer conn.Close(ctx)
		taskIDs := seedMediaMigrationTasks(
			t, ctx, conn, []string{"  upstream-clean-1  ", "upstream-clean-2"},
		)

		if err := runner.Migrate(232); err != nil {
			t.Fatalf("有历史数据但无冲突时升级 0232: %v", err)
		}
		var firstProviderTaskID string
		if err := conn.QueryRow(ctx,
			`SELECT provider_task_id FROM media_tasks WHERE id=$1`, taskIDs[0],
		).Scan(&firstProviderTaskID); err != nil {
			t.Fatalf("读取规范化任务号: %v", err)
		}
		if firstProviderTaskID != "upstream-clean-1" {
			t.Fatalf("历史任务号未按运行时合同去除首尾空白: %q", firstProviderTaskID)
		}
		if _, err := conn.Exec(ctx, `
UPDATE media_tasks
SET status='settlement_pending', result='{"status":"completed"}', actual_cents=77
WHERE id=$1`, taskIDs[0]); err != nil {
			t.Fatalf("0232 未允许待结算耐久状态: %v", err)
		}
		if _, err := conn.Exec(ctx, `
UPDATE media_tasks
SET provider_task_id='upstream-clean-1'
WHERE id=$1`, taskIDs[1]); err == nil {
			t.Fatal("0232 唯一约束未阻止同账号范围重复上游任务号")
		}
		var indexCount int
		if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE tablename='media_tasks'
  AND indexname='uq_media_tasks_provider_account_task'`).Scan(&indexCount); err != nil {
			t.Fatalf("检查任务号唯一索引: %v", err)
		}
		if indexCount != 1 {
			t.Fatalf("任务号唯一索引 count=%d want 1", indexCount)
		}
	})

	t.Run("历史重复任务号明确拒绝且人工消歧后可重试", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(
			t, ctx, baseDSN, "huakai_media_recovery_conflict_upgrade",
		)
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(231); err != nil {
			t.Fatalf("迁移到 0231: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接 0231 冲突临时库: %v", err)
		}
		defer conn.Close(ctx)
		taskIDs := seedMediaMigrationTasks(
			t, ctx, conn, []string{"upstream-duplicate", " upstream-duplicate "},
		)

		err = runner.Migrate(232)
		if err == nil || !strings.Contains(err.Error(), "拒绝自动选择账号") ||
			!strings.Contains(err.Error(), "人工消歧") {
			t.Fatalf("重复任务号升级错误=%v，期望明确拒绝并要求人工消歧", err)
		}
		var duplicateRows int
		if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM media_tasks
WHERE id=ANY($1) AND btrim(provider_task_id)='upstream-duplicate'`,
			taskIDs,
		).Scan(&duplicateRows); err != nil {
			t.Fatalf("读取迁移拒绝后的历史任务: %v", err)
		}
		if duplicateRows != 2 {
			t.Fatalf("迁移拒绝后历史任务被静默改写或删除: %d", duplicateRows)
		}
		var version uint
		var dirty bool
		if err := conn.QueryRow(ctx,
			`SELECT version, dirty FROM schema_migrations`,
		).Scan(&version, &dirty); err != nil {
			t.Fatalf("读取失败迁移版本: %v", err)
		}
		if version != 232 || !dirty {
			t.Fatalf("失败迁移 version/dirty=%d/%v want 232/true", version, dirty)
		}

		sourceErr, databaseErr := runner.Close()
		if sourceErr != nil || databaseErr != nil {
			t.Fatalf("关闭失败迁移器以释放数据库锁: source=%v database=%v",
				sourceErr, databaseErr)
		}
		runner = newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Force(231); err != nil {
			t.Fatalf("人工修复前恢复迁移版本: %v", err)
		}
		if _, err := conn.Exec(ctx, `
UPDATE media_tasks
SET provider_task_id='upstream-disambiguated'
WHERE id=$1`, taskIDs[1]); err != nil {
			t.Fatalf("人工消歧历史任务号: %v", err)
		}
		if err := runner.Migrate(232); err != nil {
			t.Fatalf("人工消歧后重试 0232: %v", err)
		}
		if err := conn.QueryRow(ctx,
			`SELECT version, dirty FROM schema_migrations`,
		).Scan(&version, &dirty); err != nil {
			t.Fatalf("读取重试迁移版本: %v", err)
		}
		if version != 232 || dirty {
			t.Fatalf("重试迁移 version/dirty=%d/%v want 232/false", version, dirty)
		}
	})
}

func seedMediaMigrationTasks(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	providerTaskIDs []string,
) []int64 {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, userID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"media-migration-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入迁移租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name)
VALUES ($1,$2)
RETURNING id`, tenantID, "media-migration-user-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("插入迁移用户: %v", err)
	}
	taskIDs := make([]int64, 0, len(providerTaskIDs))
	for index, providerTaskID := range providerTaskIDs {
		var taskID int64
		if err := conn.QueryRow(ctx, `
INSERT INTO media_tasks (
    tenant_id, user_id, task_type, status, provider, provider_task_id,
    request_id, input_params, estimated_cents, progress
) VALUES ($1,$2,'video_generate','in_progress','grok_video',$3,$4,'{}',123,50)
RETURNING id`,
			tenantID, userID, providerTaskID,
			fmt.Sprintf("media-migration-request-%s-%d", suffix, index),
		).Scan(&taskID); err != nil {
			t.Fatalf("插入第 %d 条历史媒体任务: %v", index, err)
		}
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}
