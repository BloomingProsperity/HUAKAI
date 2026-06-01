# Backend 测试矩阵

## 两套入口

| 用途 | 入口 | 范围 | 期望耗时 | 适用场景 |
|------|------|------|----------|----------|
| Fast | `scripts/run-tests.sh` 或 `cd backend && make test` | 不带 build tag 的全部 unit / contract test | < 3 分钟 | PR check / 本地随手跑 |
| 全量 | `scripts/run-integration-tests.sh` 或 `cd backend && make test-integration` | fast + `integration_pg` tag (真 PG) | ~ 10–15 分钟 | nightly / 发版前 |

PR check 用 fast，发版与 nightly 用全量。两类入口都自动把 `GOCACHE` 指
到 `$HOME/.cache/go-build`，避免在 sandbox 里把 `/tmp` 配额打爆。

## Build tag 约定

- `_integration_test.go` 文件头第一行必须是 `//go:build integration_pg`。
  这套测试默认从 `HUAKAI_DATABASE_URL` 取连接，连不上时早期 `t.Skip`。
  - 已有 12 个文件遵循这个约定：`internal/db`、`internal/registry`、
    `internal/obs`、`internal/billing`、`internal/pool`、`internal/auth`、
    `internal/admin`、`internal/auditledger`、`internal/dlq` 等。
- `smoke_test.go`（端到端启动真 binary）用 `//go:build smoke`，由
  `backend/cmd/gateway/smoke_test.go` 单点持有。
- 不要为"功能尚未实现"的占位 case 加 build tag — 那些走 `t.Skip("Phase
  4.5 ...")` 已足够清晰；加 tag 反而隐藏待办。

## 前置条件（仅全量入口）

启动本地 PG + 跑 migration：

```bash
cd backend
make db-up        # 起 docker compose 里的 postgres 容器
make db-migrate   # 用 golang-migrate 跑 sql/migrations
```

然后：

```bash
scripts/run-integration-tests.sh
# 或者覆盖默认 DATABASE_URL
HUAKAI_DATABASE_URL="postgres://user:pass@host:5432/db?sslmode=disable" \
  scripts/run-integration-tests.sh
```

## CI 分层建议

- **PR check**: `scripts/run-tests.sh`（fast）+ `go vet ./...`
- **Nightly / pre-release**: `scripts/run-integration-tests.sh`（fast + integration_pg）
- **Smoke gate**（Phase C lockdown）: `go test -tags=smoke ./cmd/gateway`

CI 配置文件本身不在本次 PR 改动范围内；如需调，对照本文档矩阵添加
即可。

## 已知"long-running / Phase 4.5"占位

下列测试目前以 `t.Skip("...Phase 4.5...")` 标占位，直到对应 feature 着陆
才能 unstick。S2-003 已于 2026-06-01 退休
`internal/gateway/forwarder_test.go` 的 AT_GW_002 占位；stream failover、
replay、settlement recovery、tenant isolation、inferred usage 现在由
`internal/gateway` 和 `internal/gatewayhttp` 的可执行测试守住。

- `internal/proto/proto_test.go`：3 个 AT_PROTO_002_* buffered 占位
- `internal/pool/pool_test.go`：`TestAT_POOL_019_Tx2Atomicity`（等 Slice 5）
- `internal/auth/auth_test.go`：1 个 bounded-timeout 8s 占位
- `internal/pool/pasr_selector_test.go`：5 个段内"无第二成员"分支跳过
- `internal/auditledger/canonical_test.go`：跨语言可复现验证（缺 python3 时跳）

这些 skip 是 future-feature 占位，**不是**集成测试入口能改变的。对应 feature
landing 时同 commit 取消对应 `t.Skip`。
