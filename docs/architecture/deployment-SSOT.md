# 部署与运维领域唯一权威文档（SSOT）

> 建档：2026-07-15（UTC）  
> 核验基线：分支 `feat/ui-density-overview`，代码基线 `0f7d6b69`。  
> 本文只描述仓库内可验证的部署形态与启动门；真实环境密钥、域名和凭据不进入文档。

## 1. 支持的部署形态

| 形态 | 当前实现 | 安全边界 |
| --- | --- | --- |
| 域名 + 自动 HTTPS | `docker-compose.prod.yml` 先迁移，再启动 gateway；gateway 只在 compose 网络暴露 8080，Caddy 对外开放 80/443 | Caddy 阻断公网 `/internal/*` 并反代其余请求（`backend/docker-compose.prod.yml:16-113`；`backend/Caddyfile:12-41`） |
| 无域名/IP 直连 | `docker-compose.direct.yml` 不含 Caddy，默认把 8080 绑定到 `0.0.0.0` | 默认是明文 HTTP；公网使用必须由运维增加 TLS，或改绑 `127.0.0.1` 给本机反代（`backend/docker-compose.direct.yml:1-14,31-88`） |
| 裸二进制单实例 | 可选 `HUAKAI_AUTO_MIGRATE=true` 使用内嵌迁移 | 默认不开自迁移；多副本仍应使用受控 one-shot（`backend/internal/dbmigrate/dbmigrate.go:1-45`；`backend/sql/embed.go:1-13`） |

## 2. 构建与前端分发

- Docker build context 必须是仓库根；先运行 Vite build，再把 `frontend/dist` 放到 Go embed 位置并以
  `-tags embed` 构建单二进制（`backend/Dockerfile:18-45`）。
- 运行时由网关提供 SPA 回退，不需要独立前端容器
  （`backend/cmd/gateway/middleware.go:128-133`）。
- 仍写“控制台未实现/API-only”的部署文档断言已经过期，但部署步骤本身仍可能有运营价值；具体
  漂移列在 `DOC-CODE-DRIFT.md`。

## 3. 启动门的真实语义

- `HUAKAI_RELEASE_MODE` 必须显式为 `production`、`dev`、`development` 或 `test`；空值或未知值
  直接拒绝启动（`backend/cmd/gateway/config.go:63-81`）。
- 凭据加密 key 始终必需；production 的 session 签名 key 必须持久配置，非生产可临时随机生成
  （`backend/cmd/gateway/config.go:238-298`）。
- production 强制 PostgreSQL audit ledger 和持久 Ed25519 私钥
  （`backend/cmd/gateway/config.go:467-508`）。该族属于 trust-chain 保护边界，本波只陈述启动门，
  不归并或删除其专门文档。
- 邮箱门默认是软门：校验失败只警告，请求时惰性失败；只有
  `HUAKAI_REQUIRE_EMAIL_GATE=true` 才成为启动硬门
  （`backend/cmd/gateway/email_gate.go:8-30`；`backend/cmd/gateway/wiring.go:1037-1050`）。
- 邮箱门只遍历正数 active tenant，排除 tenant 0 系统哨兵
  （`backend/internal/email/settings_store.go:112-140`）。

因此，production 的“迁移、审计、邮箱三道硬门”说法不准确：compose 迁移和 audit 是硬约束，
邮箱默认仅警告；开启严格开关后才是硬门。

## 4. 迁移策略

- 两份 compose 都用 `migrate/migrate` one-shot，并要求它成功退出后才启动 gateway
  （`backend/docker-compose.prod.yml:22-26,95-113`；
  `backend/docker-compose.direct.yml:37-42,97-113`）。
- 进程内迁移与 one-shot 共用 `schema_migrations`，无变更视为成功，并依赖数据库 advisory lock
  （`backend/internal/dbmigrate/dbmigrate.go:1-45`）。
- 当前 runtime 中日志 sink 在可选进程内迁移之前启动，可能丢失空库启动期日志；该代码疑点记录在
  DRIFT，不在文档波修改（`backend/cmd/gateway/wiring.go:846-864`）。

## 5. 运营风险与文档边界

- 直连 compose 默认对所有网卡发布明文端口，是明确的部署选项而不是安全默认；公开部署必须前置
  TLS。
- production compose 自动挂载审计私钥；direct compose 的持久审计卷默认注释，选择 production
  直连时必须由运维显式启用（`backend/docker-compose.direct.yml:74-87`）。
- `docs/ops/remote-dev-setup.md` 含单台旧服务器、旧路径、硬编码连接信息和过期迁移数，不能作为可复用
  runbook，已列入删除批次。
- Bedrock CLI、Anthropic cache TTL 属 provider/credentials 运营主题；auth bootstrap TTL 属 auth
  主题；本波重新归类并保留，不能因为路径在 `docs/ops` 就当部署历史删除。
- `docs/process/plans/2026-05-14-l1-prod-wiring-codex.md` 涉及受保护 trust-chain，保留。
- `docs/process/plans/2026-07-15-mvp-launch-blockers.md` 与上线前验证仍是活跃发布输入，保留。

## 6. 当前保留的运维入口

- `docs/deploy/go-live-readiness.md`：运营现状说明；已登记其“前端仍 Owner-gated”等局部漂移。
- `docs/deploy/production-bootstrap.md`：部署步骤仍有价值；“API-only/无 UI”及标题中的三硬门已登记
  漂移，修订前须结合本 SSOT 阅读。
- `docs/runbooks/upstream-policy-monitor-runbook.md`：当前运营工具入口，归到 provider policy 监测。
- `docs/01_APPLOCKER_DEFENDER_RESOLUTION.md`、`docs/dependency-policy.md`：治理/环境与依赖政策，保留。
- 被删除的已实施计划和危险旧远程环境说明见
  `docs/architecture/DOC-CONSOLIDATION-DELETION-LOG.md`。
