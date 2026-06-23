# 计划:迁移加 HUAKAI_AUTO_MIGRATE 单机自迁移开关(默认关)

- 日期:2026-06-23
- 切片:soften/auto-migrate(四条软化之③)
- 基线:origin/feat/frontend-portal @b14d960c
- Owner 绿灯:软化全选 + 明确"依赖该加就加"(放行新依赖 golang-migrate)

## 背景

现状:gateway 刻意不自管迁移,prod/direct compose 用 `migrate/migrate` one-shot 外置先跑。对 **compose 部署**这其实已是"一条 `docker compose up` 连带迁移"(depends_on `service_completed_successfully`),摩擦不大;真正的摩擦只落在**裸二进制单实例**运维——要先手动跑迁移再起 gateway。本切片给这条路加可选的进程内自迁移。

## 三镜对照(§16)

| 项目 | 启动自迁移 |
|---|---|
| sub2api @e34ad2b | 启动时自动跑迁移 |
| new-api @1ac0f58 | InitDB 内 AutoMigrate 自动 |
| CLIProxyAPI @2a050dc | 启用 PG 时自动建表 |

三镜都"迁移随启动自动跑"。HUAKAI 默认外置(解耦 schema 变更、多副本防竞态),本切片把"自动随启动"做成**可选开关**,默认仍外置——既补齐裸二进制摩擦,又不丢多副本受控迁移的正确性。

## 设计

- 新依赖 `github.com/golang-migrate/migrate/v4 v4.19.1`(**MIT**,GitHub 因双版权头误标 NOASSERTION,正文逐字 MIT;Owner 放行)。与 compose one-shot(同为 migrate)共用同一张 `schema_migrations`(version+dirty),版本语义一致、互相幂等、不撞表。**实测证实**:dev 库现有 schema_migrations 即此格式。二进制实际只新增编译 `golang-migrate` + `lib/pq`(pgx 本就是依赖);go.mod 仅 +2 行,docker/containerd 等仅为 tidy 图噪音不编入。
- 新增 `backend/sql/embed.go`(package sqlmigrations):`go:embed migrations/*.sql`(embed 不能跨目录向上,故放 sql/ 内)。
- 新增 `backend/internal/dbmigrate/dbmigrate.go`:`Up(fs, dsn)` 复用 golang-migrate(iofs 源 + postgres 驱动),`ErrNoChange` 视为成功;postgres 驱动自带 advisory lock,多副本并发启动安全。
- 新增 `backend/cmd/gateway/auto_migrate.go`:`autoMigrateEnabled()` 读 `HUAKAI_AUTO_MIGRATE`(默认 false)。
- 改 `backend/cmd/gateway/wiring.go:698` 后:`autoMigrateEnabled()` 为真时,在任何代码用表之前 `dbmigrate.Up`。
- `.env.prod.example`/`.env.direct.example`/`docs/deploy` 增 `HUAKAI_AUTO_MIGRATE` 说明。

## 默认行为(非 default-flip)

默认 false → 行为零变化(迁移仍外置);仅显式 `HUAKAI_AUTO_MIGRATE=true` 才进程内自迁移。对既有 compose 部署无任何影响。

## 测试(判别式)

- `auto_migrate_test.go`:`autoMigrateEnabled` 默认 false;变异翻默认即 RED。
- `dbmigrate_integration_pg_test.go`:临时 CREATE 空库 → `Up` → 断言 tenants 表建出 + schema_migrations 非零版本 + dirty=false + 再跑幂等;变异(Up 空操作)→ tenants 缺失 RED。**已实测**:空库真跑全 148 迁移(5.1s)通过,变异 RED。

## blast radius / 风险

- 改动限启动期(不碰请求面);默认关不改既有行为。
- 风险:① 撞表——已用同一 golang-migrate + 同表规避并实测;② 多副本竞态——postgres 驱动 advisory lock 兜底,且默认关时多副本仍走 one-shot;③ 新依赖足迹——实测仅 +golang-migrate/lib/pq 编入,go.mod +2 行。
