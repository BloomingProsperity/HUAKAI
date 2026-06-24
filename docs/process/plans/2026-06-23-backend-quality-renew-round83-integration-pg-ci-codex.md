# 2026-06-23 backend quality renew round83 integration_pg ci

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文档 §③-6 点名 CI 不带 `integration_pg` 且只设置 `HUAKAI_TEST_DATABASE_URL`，导致真 PG 集成测试假绿。 |
| Scope | 仅改 `.github/workflows/backend-ci.yml` 的测试步骤；不改 Go 测试实现、不改数据库 schema、不改迁移文件、不碰 auth/billing/quota 生产逻辑、不读取或修改另一个 security 目标。 |
| Success criteria | CI 在迁移后的 Postgres 上新增带 `-tags=integration_pg` 的 Go 测试步骤；该步骤设置 `HUAKAI_DATABASE_URL`；保留 `HUAKAI_TEST_DATABASE_URL` 作为少数旧用例兼容；默认 `go test -race` 仍保留。 |
| Time estimate | 约 10-15 分钟；一个 Codex 小闭环。 |
| Blast radius | 中低。仅改变 CI 覆盖面，会让此前未运行的真 PG 测试暴露问题；不会改变运行时代码。若测试互相争用同一库，CI 会转红，但这是目标文档要求揭出的假绿。 |
| Failure modes | `integration_pg` 包并发共享数据库导致互相污染；CI 时间增长；本地没有 Go 工具链无法验证测试执行。缓解：集成步骤使用 `-p 1` 顺序跑包，timeout 放宽；本地用 YAML/grep 检查证明配置到位。 |
| Decision points | 无需 Owner 中途确认；如果需要改 schema、迁移或高风险生产逻辑才能让测试通过，停止并单独确认。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已确认 workflow 当前只设置 `HUAKAI_TEST_DATABASE_URL` 且默认测试不带 tag；3. 已确认至少 112 个 `//go:build integration_pg` 文件；4. 已确认大量测试读取 `HUAKAI_DATABASE_URL`；5. 修改后检查 YAML 片段、关键词与可用命令。 |
