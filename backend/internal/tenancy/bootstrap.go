// 包 tenancy — 单租户开箱即用的默认工作租户种子(定位:单租户后台 + 多用户
// 运营 + 多租户预留,Owner 2026-06-11)。
//
// 背景:迁移 0030 只种了 id=0 'public-pricing' 哨兵(公共定价 scope,不可当
// 工作租户:Register 等校验 TenantID<=0 直接拒绝),0001 注释承诺的 default
// tenant 从未落地——运营者从零部署后必须手写 psql 才能得到第一个可用租户。
// 本钩子镜像 admin.MaybeBootstrap 的「advisory lock 防双插 + 计数短路」形状,
// 启动时保证至少存在一个 active 工作租户。
//
// SQL 走 raw pgx 而非 sqlc:本仓 sqlc v1.27 对既有生成文件产生工具链漂移 diff
// (2026-06-11 实测,与 refs-delta-mine 记录的回退坑一致),启动钩子的四条短查询
// 不值得为此触发全量重新生成;先例:cmd/smoke-setup 同样 raw SQL 种租户。
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	// DefaultWorkingTenantID 是默认种子的工作租户 id(smoke-setup 同值,curl/
	// psql 人工核查方便)。
	DefaultWorkingTenantID = int64(1)
	// DefaultWorkingTenantIDEnv 允许运维覆盖种子租户 id(>0;与 config 包
	// 「全部配置走 env」的约定一致——config.example.yaml 的 tenancy 段是
	// 死文档,生产不解析 yaml)。
	DefaultWorkingTenantIDEnv = "HUAKAI_DEFAULT_WORKING_TENANT_ID"
	// defaultWorkingTenantName 是种子租户名;运营者可在管理面改。
	defaultWorkingTenantName = "default"
)

// ErrTenancyBackend 标记 EnsureDefaultTenant 的基础设施失败(启动 fatal)。
var ErrTenancyBackend = errors.New("tenancy: backend error")

// WorkingTenantIDFromEnv 解析默认工作租户 id(env 覆盖,默认 DefaultWorkingTenantID)。
// 非法覆盖 fail-loud(吞成默认值会让运维误以为覆盖生效)。这是「哪个租户是工作租户」
// 的单一真相源,供 EnsureDefaultTenant 与其他启动钩子(如 admin 用户 bootstrap)共用,
// 避免各处各读一份 env 而漂移。
func WorkingTenantIDFromEnv() (int64, error) {
	seedID := DefaultWorkingTenantID
	if raw := os.Getenv(DefaultWorkingTenantIDEnv); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%w: %s=%q 非法(需正整数)", ErrTenancyBackend, DefaultWorkingTenantIDEnv, raw)
		}
		seedID = parsed
	}
	return seedID, nil
}

// EnsureDefaultTenant 在启动时保证至少存在一个非哨兵(id>0)、未软删、active
// 的工作租户;一个都没有时种 DefaultWorkingTenantID(env 可覆盖 id)。幂等、
// advisory lock 串行化(多网关并发启动安全)。失败 = 启动 fatal(fail-loud,
// 与 MaybeBootstrap 同语义):没有工作租户的网关只能产出 FK 断头。
//
// 注意:已有任意工作租户(含 id≠默认值的)即短路返回——本钩子只救「从零部署」
// 的空库,绝不在运营中的库上做任何写入。
func EnsureDefaultTenant(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	if pool == nil {
		return fmt.Errorf("%w: nil pool", ErrTenancyBackend)
	}

	seedID, err := WorkingTenantIDFromEnv()
	if err != nil {
		return err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin tx: %v", ErrTenancyBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 与 admin_bootstrap 不同的 lock key:两个钩子互不阻塞。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('tenancy_default_tenant'::text, 0))`); err != nil {
		return fmt.Errorf("%w: advisory lock: %v", ErrTenancyBackend, err)
	}

	// 工作租户 = id>0、未软删。状态不限 active:存在 suspended 工作租户说明
	// 运营者动过手,绝不越权再种一个。
	var working int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id > 0 AND deleted_at IS NULL`).Scan(&working); err != nil {
		return fmt.Errorf("%w: count working tenants: %v", ErrTenancyBackend, err)
	}
	if working > 0 {
		logger.Info("default tenant seed skipped: working tenant already exists",
			zap.Int64("working_tenant_count", working))
		return nil
	}

	// 目标 id 若以软删状态存在(整库唯一工作租户被软删 = 站点等于砖了),
	// 复活它(并采纳为默认租户:归一 name 到 defaultWorkingTenantName——既给被
	// 收编的软删行一个规范名,也避开 uq_tenants_name:旧 name 恰与某 active 行
	// 同名时直接 UPDATE 会撞唯一冲突致启动 fatal,评审 S3-2)而非撞主键;否则
	// 新插。两路都显式 active。注:此处归一的是「软删、即将被收编为默认租户」
	// 的行,不是运营中的活租户(那种已在 count 短路里跳过),不篡改运营数据。
	tag, err := tx.Exec(ctx, `UPDATE tenants SET status = 'active', deleted_at = NULL, name = $2, updated_at = now() WHERE id = $1`, seedID, defaultWorkingTenantName)
	if err != nil {
		return fmt.Errorf("%w: restore tenant %d: %v", ErrTenancyBackend, seedID, err)
	}
	restored := tag.RowsAffected() > 0
	if !restored {
		if _, err := tx.Exec(ctx,
			`INSERT INTO tenants (id, name, status, created_at, updated_at) VALUES ($1, $2, 'active', now(), now())`,
			seedID, defaultWorkingTenantName); err != nil {
			return fmt.Errorf("%w: insert default tenant %d: %v", ErrTenancyBackend, seedID, err)
		}
	}

	// 承重细节:tenants.id 是 bigserial,显式插 id 不推进序列——不补 setval 的话
	// 下一个自动取号的 INSERT INTO tenants (name) 会撞 id=1 唯一冲突(集成测试
	// 与未来建租户全炸)。序列名一律走 pg_get_serial_sequence 动态解析,不硬编码
	// tenants_id_seq(序列被重命名时硬编码子查询会报 relation 不存在,评审 S3-1)。
	// 显式插 id 不动序列,故 MAX(id) 即足;GREATEST(.,1) 防空表回拨到 0。
	if _, err := tx.Exec(ctx,
		`SELECT setval(pg_get_serial_sequence('tenants','id'), GREATEST((SELECT COALESCE(MAX(id),1) FROM tenants), 1), true)`); err != nil {
		return fmt.Errorf("%w: advance tenants id sequence: %v", ErrTenancyBackend, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrTenancyBackend, err)
	}

	if restored {
		logger.Warn("default working tenant restored from soft-delete (library had zero working tenants)",
			zap.Int64("tenant_id", seedID))
	} else {
		logger.Info("default working tenant seeded",
			zap.Int64("tenant_id", seedID),
			zap.String("name", defaultWorkingTenantName))
	}
	return nil
}
