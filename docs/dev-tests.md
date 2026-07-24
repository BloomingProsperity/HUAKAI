# 开发与测试入口

本文件只描述当前主线已有的测试入口。命令、范围或参数发生冲突时，以
`scripts/`、`backend/Makefile` 和 `.github/workflows/backend-ci.yml`
的实际实现为准。

## 1. 日常快速门

从仓库根目录运行：

```bash
scripts/run-tests.sh
```

该脚本进入 `backend/`，执行：

```bash
go test -race -count=1 -timeout 180s ./...
```

它不包含带 `integration_pg` 或 `smoke` build tag 的测试。只验证一个包时，
可传入包路径：

```bash
scripts/run-tests.sh ./internal/billing/...
```

Makefile 还提供以下独立入口：

```bash
make -C backend test
make -C backend vet
make -C backend quality-gate
make -C backend perf
```

`make test` 与根脚本并不完全等价：它没有设置 180 秒全局超时。性能门也不会由
上述普通测试命令自动执行，必须单独运行 `make perf`，CI 则有独立性能步骤。

## 2. PostgreSQL 集成门

当前主线存在三种入口，隔离级别不同：

| 入口 | 实际行为 | 适用范围 |
| --- | --- | --- |
| `scripts/run-integration-tests.sh` | 先跑普通 race 测试，再对同一 `HUAKAI_DATABASE_URL` 运行全部 `integration_pg` 包 | 兼容旧本地流程；调用者负责数据库独占与迁移 |
| `make -C backend test-integration` | 对同一数据库以 `-p 1` 串行运行 `integration_pg` | 本地单人、独占数据库 |
| `backend/scripts/integration-pg.sh` | 建立迁移模板，为每个测试包克隆独立数据库，逐包运行后清理 | CI 与高可信全量验证，推荐 |

本地运行共享数据库入口前，先启动 PostgreSQL 并完成迁移：

```bash
make -C backend db-up
make -C backend db-migrate
scripts/run-integration-tests.sh
```

高可信集成门使用 `backend/scripts/integration-pg.sh`。它读取 `PGHOST`、
`PGPORT`、`PGUSER`、`PGPASSWORD` 等管理连接参数，避免临时触发器、清理语句、
资金与租户测试互相污染。当前 CI 的 `integration-pg` job 使用的就是该入口。

## 3. CI 实际门

`.github/workflows/backend-ci.yml` 当前执行：

- `go vet ./...`
- `govulncheck ./...`
- staticcheck、deadcode 与代码体量预算门
- migration `up -> down -> up`
- 普通 Go race 测试
- 每包独立数据库的 `integration_pg` 测试
- 性能门
- Rust workspace 的格式、clippy、普通测试与 ignored 测试
- 生产容器双进程生命周期冒烟

## 4. 冒烟与真实上游

带 `smoke` 标签的网关测试：

```bash
cd backend
go test -tags=smoke ./cmd/gateway -count=1
```

生产镜像生命周期：

```bash
bash backend/scripts/container-smoke.sh
```

真实账号或官方 Key 测试必须显式提供对应环境变量，不能把凭据写进仓库、测试
日志或 Issue。没有活体凭据时必须标记“未做活体验证”，不能用 mock 成功冒充
真实上游成功。

## 5. 判别要求

- 修 Bug 时先证明旧实现会失败，再证明修复后通过。
- 钱路、鉴权、租户隔离、配额、迁移和恢复必须覆盖失败、重放、并发与回滚。
- `t.Skip` 只允许用于明确缺少外部前置条件的测试，不能把实现缺失包装成绿灯。
- 测试必须断言正确结果，不能只断言“不是某个错误值”。
- PR 描述必须列出实际运行命令、结果、未运行项及原因。

## 6. Windows 测试环境

Windows 11 的 Smart App Control 可能按新生成测试二进制的哈希临时阻止
`go test`，常见错误包含 `Application Control policy has blocked this file`
或 `Permission denied`。`backend/scripts/run-go-test.sh` 与
`backend/scripts/run-go-test.ps1` 只在命中这两类特征时最多重试三次，不会
掩盖编译失败或真实测试失败。

长期处置应由 Windows 管理员关闭 Smart App Control，或者在受控开发机上配置
组织认可的应用控制策略；不要通过无限重试、关闭全部安全软件或把失败测试当作
成功来绕过。
