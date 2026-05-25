# 2026-05-13 sub2api 目录骨架深挖（Codex lane）

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: sub2api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## 0. 元数据
- Agent: Codex lane / GPT-5 Codex。
- 输出文件: `docs/research/2026-05-13-sub2api-dir-skeleton-codex.md`。
- 目标 ref: `~/refs/sub2api/`。
- 目标 commit: `dbc8ae658cfc`。
- 最近提交时间: `2026-05-08T20:00:06+08:00`。
- 本轮只读取 `sub2api` 一个 reference project；未读取其他 reference repo。
- 本轮没有读取 HUAKAI 业务代码；仅读取 Owner brief、技能说明和 `~/refs/sub2api/`。
- 本轮没有读取旧 sub2api decomposition 文件内容。
- 写作方式: 路径、目录、文件名仅作为证据索引，不建议 HUAKAI 复制目录结构。
- Observed regions: 约 88 个源码/配置/脚本/文档区域，详见 §30。
- Inferences: 20 个，集中在 HUAKAI 升级点与 punch list。
- Open questions: 7 个，集中在多节点运行、支付合规、数据管理守护进程和商业化边界。

## 1. 顶层目录快照
- 顶层目录观察到 `.github/`、`assets/`、`backend/`、`deploy/`、`docs/`、`frontend/`、`tools/`。
- 顶层文件包含根 README、开发指南、根 Dockerfile、根 Makefile、发布配置和 license 文件。
- 根 README 把项目定位为面向 AI API 账户池、订阅额度、认证、计费、负载均衡和请求转发的一体平台。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:35`
- 根 README 显式列出多账号管理、API key 分发、token 级计费、调度、并发限制、支付和管理后台。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:39`
- 根 README 把后端、前端、数据库与缓存栈说明为 Go/Gin/Ent、Vue/Vite、PostgreSQL、Redis。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:124`
- 根 README 对部署、源码构建、Docker、安装脚本、Simple Mode、Antigravity 和安全项都有直接说明。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:145`
- 根 README 声明其许可为 LGPL。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:631`
- HUAKAI 不能复制其源代码、注释、schema、UI 源或文件结构；只能用观察到的行为做 clean-room 功能映射。

## 2. 根目录（repo root）
### 2.1 用途
- 根目录承担项目说明、整体构建、容器打包、发布元数据和开发入口。
- README 是用户安装、运行、安全配置和功能暴露的主要入口。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:145`
- 开发指南规定后端、前端、CI、锁文件和代码生成约束。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:11`
- 根 Dockerfile 把前端产物嵌入后端二进制，最终镜像运行单进程服务。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:19`
- 根 Makefile 把后端、前端、测试和关键前端测试整合为开发命令。`Wei-Shaw/sub2api@dbc8ae658cfc:Makefile:11`

### 2.2 关键文件
- `README.md`: 功能、部署、Simple Mode、安全配置和项目结构说明。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:39`
- `DEV_GUIDE.md`: 技术栈、CI、锁文件和 Ent 生成要求。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:46`
- `Dockerfile`: 多阶段构建、嵌入前端、非 root 运行和健康检查。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:37`
- `Makefile`: 后端构建、前端构建、单元测试和关键 UI 测试。`Wei-Shaw/sub2api@dbc8ae658cfc:Makefile:26`
- `.goreleaser*.yaml`: 发布流程依赖的顶层配置，工作流会调用它们。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:175`

### 2.3 入口
- 用户入口是 README 中的脚本安装、Docker Compose 和源码构建路径。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:145`
- 开发入口是根 Makefile 与前后端子目录脚本。`Wei-Shaw/sub2api@dbc8ae658cfc:Makefile:11`
- 容器入口是根 Dockerfile 生成的单个后端可执行文件。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:63`
- 发布入口是 tag 或手动触发的 release workflow。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:3`

### 2.4 logic
- 构建逻辑先生成前端静态产物，再把产物复制给后端编译步骤。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:19`
- 后端编译使用嵌入前端的构建标签，并注入版本相关元数据。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:63`
- 最终镜像建立非 root 用户、数据目录和健康检查。`Wei-Shaw/sub2api@dbc8ae658cfc:Dockerfile:84`
- 开发规则要求前端 lockfile 参与 CI，避免依赖漂移。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:46`
- schema 变更要求运行 Ent 生成并提交生成结果。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:199`

### 2.5 暴露功能
- 根目录暴露安装、更新、回滚、源码构建、Docker 启动和运维说明。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:145`
- README 暴露安全控制项，包括 CORS、URL 白名单、响应头过滤、CSP、计费熔断、可信代理和 Turnstile。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:457`
- README 暴露 Simple Mode，说明该模式隐藏部分 SaaS 能力并跳过计费确认。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:539`
- README 暴露 Antigravity 兼容端点和混合调度警告。`Wei-Shaw/sub2api@dbc8ae658cfc:README.md:549`

### 2.6 HUAKAI 升级点
- HUAKAI 应把“单二进制 + 内嵌前端 + 可选 setup wizard”作为部署体验候选，而不是复制 upstream 构建实现。
- HUAKAI 应保留双模式设计，但把 Simple/Standard 与 HUAKAI 双版本和租户策略做清晰映射。
- HUAKAI 应把 README 中安全开关升级为可审计安全基线：默认值、风险说明、回滚策略、操作日志。
- HUAKAI 应把根级 Makefile 能力拆成 CI 可验证合同，防止“文档可运行、流水线不可运行”。
- HUAKAI 不应继承 LGPL 代码或文件结构；应实现 MIT clean-room 等价行为。

## 3. `.github/`
### 3.1 用途
- `.github/` 承担 CI、发布、CLA 和安全扫描自动化。
- 后端 CI 会运行 Go 单元测试、集成测试和 linter。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:21`
- 前端 CI 使用 pnpm、Node、锁文件并执行类型检查与关键 Vitest。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:35`
- 安全扫描定期运行后端漏洞扫描和前端高危依赖审计。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/security-scan.yml:3`
- 发布 workflow 负责版本文件、前端产物、GoReleaser 和容器镜像。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:29`

### 3.2 关键文件
- `.github/workflows/backend-ci.yml`: Go、前端和 lint 三类 CI。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:10`
- `.github/workflows/release.yml`: tag 和手动触发发布。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:3`
- `.github/workflows/security-scan.yml`: 后端和前端安全扫描。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/security-scan.yml:12`
- `.github/audit-exceptions.yml`: 前端 audit exception 清单。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/audit-exceptions.yml:1`

### 3.3 入口
- Push 和 PR 触发 CI。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:3`
- tag 或手动输入触发 release。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:3`
- Push、PR 和周计划触发安全扫描。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/security-scan.yml:3`

### 3.4 logic
- CI 固定检查 Go 版本，后端测试分为单元和集成两步。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:21`
- 前端依赖安装使用 frozen lockfile，说明锁文件被当作发布一致性输入。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/backend-ci.yml:45`
- 发布先构建前端 artifact，再在 release job 中下载并嵌入。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:54`
- 安全扫描把 pnpm audit 输出交给本地异常验证脚本。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/security-scan.yml:50`

### 3.5 暴露功能
- 暴露基本质量门：测试、lint、类型检查、前端关键测试。
- 暴露供应链门：后端漏洞扫描、前端高危/严重漏洞异常清单。
- 暴露发布链：版本同步、前端产物、二进制/镜像发布和通知。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:175`

### 3.6 HUAKAI 升级点
- HUAKAI 需要把 reference 的 CI 能力升级成 release gate：schema、auth、billing、quota、gateway、ops 分域检查。
- HUAKAI 的 audit exception 必须有 Owner、到期日、影响范围和替代计划；reference 清单给了到期字段示例。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/audit-exceptions.yml:3`
- HUAKAI 发布链应增加 SBOM、license scan、provenance、镜像签名和回滚演练。
- HUAKAI 不应把通知 webhook 或容器账号假设硬编码到发布流程。

## 4. `assets/`
### 4.1 用途
- `assets/` 目前主要存放合作方或展示用 logo 图片。
- 该目录不是 gateway 运行主路径，也不是后端业务逻辑入口。

### 4.2 关键文件
- `assets/partners/logos/*.jpg|*.png`: 多个合作方 logo 资源。
- 观察到的文件集中在 `partners/logos` 下，没有发现源码逻辑文件。

### 4.3 入口
- 没有观察到独立程序入口。
- 可能由 README 或前端静态展示引用；本轮未深追引用链。

### 4.4 logic
- 该目录的行为逻辑是静态资源承载。
- 未观察到资源生成、压缩、版权元数据或校验逻辑。

### 4.5 暴露功能
- 暴露品牌/合作方展示素材。
- 不暴露 API、调度、支付或账户池能力。

### 4.6 HUAKAI 升级点
- HUAKAI 应对任何第三方 logo/图片建立素材授权清单。
- HUAKAI 若做 marketplace/partner 展示，应把图片来源、授权、压缩版本和替换流程产品化。
- HUAKAI 不应复制 reference 图片资产。

## 5. `backend/`
### 5.1 用途
- `backend/` 是网关、账号池、用户账户、计费、支付、调度、运维和 setup 的主实现区域。
- 后端依赖 Go、Gin、Ent、PostgreSQL、Redis、支付 SDK、OAuth、cron、JWT、TOTP 等能力。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/go.mod:1`
- 后端入口可在 setup mode、auto setup 和 normal server 之间分支。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:57`

### 5.2 关键文件
- `backend/cmd/server/main.go`: 进程启动、setup 判断和 HTTP 生命周期。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:57`
- `backend/cmd/server/wire.go`: DI 组装和后台服务 cleanup。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/wire.go:31`
- `backend/internal/server/router.go`: 路由装配和 API 分组。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:103`
- `backend/internal/service/`: 大多数业务服务。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1343`
- `backend/internal/handler/`: HTTP handler 和网关请求循环。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:113`
- `backend/internal/repository/`: Ent/Redis/外部调用/缓存仓储。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/repository/concurrency_cache.go:13`
- `backend/ent/`: Ent schema 和生成代码。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:18`
- `backend/migrations/`: SQL migration 嵌入资源。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/migrations/migrations.go:12`

### 5.3 入口
- CLI/HTTP 进程入口读取 flag、setup 状态和 auto setup 状态。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:57`
- Setup server 单独注册 setup 路由，并在可用时服务嵌入前端。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:99`
- Normal server 加载配置、初始化依赖、启动 HTTP server 并处理 graceful shutdown。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:131`
- Router 入口把 common、auth、user、admin、gateway、payment 等组挂到 API。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:103`

### 5.4 logic
- 后端把 API 路由、管理路由、支付路由和前端静态服务放在同一进程中。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:53`
- Gateway 路由链先做 body 限制、request id、错误记录、端点标准化、API key 认证和分组绑定。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:25`
- 账号选择会考虑强制平台、分组平台、渠道计价、混合调度、平台专属选择和账户数据补齐。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1343`
- 负载感知选择会结合 sticky、候选过滤、并发槽、会话限制和等待策略。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1401`
- OpenAI 路径还观察到 previous response、session sticky、transport 兼容、模型/图片/compact 能力和 WS/HTTP 分支。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_gateway_service.go:1994`
- 失败路径会在同账号重试、临时不可调度、切换次数和等待延迟之间做状态推进。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/failover_loop.go:63`

### 5.5 暴露功能
- 暴露 Anthropic Messages、token count、OpenAI Responses、Chat Completions、Images、Gemini 兼容和 Antigravity 专用路径。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:43`
- 暴露 admin dashboard、用户、分组、账号、OAuth、代理、设置、数据管理、备份、ops、系统、订阅、用量、风控、联盟等管理能力。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/admin.go:17`
- 暴露注册、登录、2FA、邮箱验证、重置密码、第三方 OAuth 和会话撤销。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/auth.go:27`
- 暴露用户 profile、API key、分组费率、可用渠道、用量、公告、兑换、订阅和监控状态。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/user.go:23`
- 暴露支付配置、订单、订阅计划、支付验证、webhook 和管理端支付 dashboard。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/payment.go:23`

### 5.6 HUAKAI 升级点
- HUAKAI 应把 gateway 调度、计费、quota、auth、ops 解耦成可测试边界，避免所有行为都隐式耦合到 handler 循环。
- HUAKAI 应把 per-request failover 状态、账号临时禁用、用户并发等待和用量写入形成明确状态机。
- HUAKAI 应把 OpenAI WS/HTTP 双路径抽象成 provider capability contract。
- HUAKAI 应把后台服务启动/关闭次序纳入 readiness/liveness 和 drain 测试。
- HUAKAI 应保留 full parity，但所有行为要通过 MIT clean-room 重新实现。

## 6. `backend/cmd/`
### 6.1 用途
- `backend/cmd/` 是后端可执行进程入口和依赖装配区域。
- 入口处理 setup、auto setup、正常 server 和信号关闭。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:57`

### 6.2 关键文件
- `backend/cmd/server/main.go`: 主进程生命周期。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:131`
- `backend/cmd/server/wire.go`: 依赖集合与 cleanup provider。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/wire.go:31`
- `backend/cmd/server/VERSION`: release workflow 会在发布时更新。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/release.yml:36`

### 6.3 入口
- 启动时先处理命令行 flag，再判断是否进入 CLI setup、auto setup 或 setup server。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:57`
- 普通模式先加载配置和应用依赖，再启动 HTTP server。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:131`

### 6.4 logic
- Cleanup provider 并行停止多个后台服务，再顺序关闭 Redis 和 Ent client。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/wire.go:111`
- 停止序列覆盖 ops、scheduler、token refresh、订阅过期、价格刷新、账单缓存、用量写入、OAuth、账号池、监控、备份、支付过期和渠道监控。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/wire.go:111`

### 6.5 暴露功能
- 暴露单进程部署、setup wizard、auto setup、graceful shutdown 和后台服务生命周期。

### 6.6 HUAKAI 升级点
- HUAKAI 应把启动阶段拆成可观测 readiness 阶段：配置、数据库、Redis、migration、cache warmup、scheduler ready。
- HUAKAI 应记录每个后台服务的 stop timeout 和失败策略。
- HUAKAI 应把 cleanup 顺序写成 acceptance tests，尤其是 usage/billing 关闭前 flush。

## 7. `backend/internal/config/`
### 7.1 用途
- 配置目录把 server、日志、CORS、安全、计费、数据库、Redis、ops、JWT、OAuth、定价、gateway、订阅、缓存、并发、token refresh、运行模式和幂等性集中建模。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/config/config.go:60`

### 7.2 关键文件
- `backend/internal/config/config.go`: 主配置模型、默认值和嵌套模块。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/config/config.go:60`
- `deploy/config.example.yaml`: 配置样例和部署可调参数。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/config.example.yaml:13`

### 7.3 入口
- Normal server 通过配置加载进入 app 初始化。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:131`
- Auto setup 会从环境变量生成配置文件。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:534`

### 7.4 logic
- Gateway 配置覆盖响应体和响应头限制、连接池隔离、Codex 桥、OpenAI 透传、WS、图片并发、上游连接池、并发槽 TTL、stream timeout、failover、调度、TLS 指纹、用量写入池和用户消息队列。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/config/config.go:611`
- OpenAI WS 配置覆盖 v2 开关、强制 HTTP、恢复、池容量、重试预算、sticky TTL 和调度权重。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/config/config.go:763`
- 用量写入池配置独立存在，说明请求热路径与落库写入之间有缓冲层。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/config/config.go:858`

### 7.5 暴露功能
- 暴露 run mode、URL allowlist、响应头过滤、CSP、计费熔断、proxy fallback、web search emulation、TLS 指纹、并发、缓存和 idempotency 配置。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/config.example.yaml:85`

### 7.6 HUAKAI 升级点
- HUAKAI 应把配置模型分成 owner-visible、ops-visible、danger-zone 三层。
- HUAKAI 应为每个高风险配置增加默认值理由、热更新性、审计记录和回滚方式。
- HUAKAI 应把 gateway 热路径配置转成 typed policy，不应让 handler 直接读取散落开关。

## 8. `backend/internal/server/`
### 8.1 用途
- `server/` 装配 HTTP engine、全局中间件、前端服务和路由分组。
- 它是 handler/service 与外部 HTTP contract 的边界。

### 8.2 关键文件
- `backend/internal/server/http.go`: Gin mode、trusted proxy、web search manager、HTTP server 参数。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:43`
- `backend/internal/server/router.go`: 全局中间件、前端 fallback 和 API group。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:23`
- `backend/internal/server/routes/*.go`: admin/auth/user/gateway/payment/common 路由表。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/admin.go:17`

### 8.3 入口
- HTTP provider 注入 config、handlers、auth middleware、API key service、订阅、ops、setting、Redis 等依赖。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:30`
- Router group 从 `/api/v1` 挂载主要 API。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:103`

### 8.4 logic
- 全局 middleware 包括 request id、安全头、恢复、日志、CORS、大小限制、响应限制和前端注入。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:53`
- Server 会依据配置设置 release mode、trusted proxies、H2C 和最大 header/body 行为。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:43`
- Web search manager 会在启动和设置更新时重建。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:62`

### 8.5 暴露功能
- 暴露健康检查、setup 状态、telemetry no-op、API group 和 SPA fallback。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/common.go:12`
- 暴露 gateway 多协议端点。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:43`
- 暴露 admin 大量 ops 和业务管理端点。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/admin.go:124`

### 8.6 HUAKAI 升级点
- HUAKAI 应把路由表生成 API contract 快照，配 acceptance tests 保证兼容。
- HUAKAI 应把 gateway 路由链的鉴权、分组绑定、限流、计费预检和日志归因显式化。
- HUAKAI 应把 `/health` 拆成 liveness/readiness/startup 三类，避免 migration/cache 未 ready 时误判。

## 9. `backend/internal/handler/`
### 9.1 用途
- `handler/` 处理 HTTP 请求解析、用户/API key 上下文、计费预检、并发等待、转发循环、failover 和响应写回。

### 9.2 关键文件
- `backend/internal/handler/gateway_handler.go`: Anthropic/Gemini/Antigravity 共享 gateway handler。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:113`
- `backend/internal/handler/openai_gateway_handler.go`: OpenAI Responses 入口和请求循环。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/openai_gateway_handler.go:82`
- `backend/internal/handler/failover_loop.go`: failover 状态推进。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/failover_loop.go:63`
- `backend/internal/handler/gateway_handler_responses.go`: Anthropic group 下的 Responses 兼容路径。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler_responses.go:47`
- `backend/internal/handler/gateway_helper.go`: 等待、释放和客户端上下文辅助。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_helper.go:106`

### 9.3 入口
- Gateway handler 依赖 gateway、兼容、Antigravity、user、billing、usage、API key、worker pool、透传、moderation、concurrency、消息队列和 settings。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:37`
- OpenAI Responses handler 包含 panic guard、body 校验、previous response 约束、moderation、图片并发、函数调用校验、用户并发和计费预检。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/openai_gateway_handler.go:82`

### 9.4 logic
- Messages handler 从 body 读取、解析、识别渠道、检测客户端、做内容检查、等待队列、用户并发槽和计费复核。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:113`
- Gemini/Antigravity 分支在循环里选择账号、等待账号槽、转发、处理 failover、记录 RPM 和异步 usage。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:314`
- Anthropic 分支也进入嵌套 failover 循环并不断重新选择账号。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:539`
- OpenAI loop 会调度账号、拿并发槽、转发、处理同账号重试或切换、记录调度结果、异步用量。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/openai_gateway_handler.go:260`
- 等待账号槽时可以在 streaming 场景发 ping，并使用退避和抖动。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_helper.go:284`

### 9.5 暴露功能
- 暴露用户等待队列体验、sticky session、failover、同账号重试、账号槽释放、内容 moderation、usage 写入和 ops 错误上下文。

### 9.6 HUAKAI 升级点
- HUAKAI 应把 handler 中的“请求生命周期事件”做成 trace span：预检、排队、选号、转发、stream、usage、billing、failover。
- HUAKAI 应把 failover 状态机独立建模并做 scenario tests。
- HUAKAI 应把 streaming wait ping、取消释放、用户并发释放做成可靠性测试。

## 10. `backend/internal/service/`
### 10.1 用途
- `service/` 是核心业务域：账号池、调度、网关兼容、OAuth、token 刷新、计费、订阅、支付、ops、channel monitor、备份、数据管理、风控和内容 moderation。

### 10.2 关键文件
- `gateway_service.go`: Anthropic/Gemini 混合调度和通用账号选择。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1343`
- `openai_gateway_service.go`: OpenAI 转发、模型映射、WS/HTTP、failover 和用量。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_gateway_service.go:1994`
- `openai_account_scheduler.go`: OpenAI 分层调度、sticky、运行时指标和负载选择。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_account_scheduler.go:243`
- `scheduler_snapshot_service.go`: 调度快照、outbox 轮询、fallback 限流。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/scheduler_snapshot_service.go:67`
- `concurrency_service.go`: 账号/用户并发槽和等待队列服务。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/concurrency_service.go:15`
- `billing_service.go`: 统一计费价格和 fallback 价格。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/billing_service.go:44`
- `billing_cache_service.go`: billing cache 与写入 worker。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/billing_cache_service.go:49`
- `usage_record_worker_pool.go`: 用量记录 worker pool。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/usage_record_worker_pool.go:17`
- `token_refresh_service.go`: OAuth token refresh 调度与失败处理。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/token_refresh_service.go:103`
- `payment_order.go`: 订单创建、限额、provider 调用和列表。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/payment_order.go:23`
- `ops_metrics_collector.go`: ops 指标采集、leader lock 和 heartbeat。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/ops_metrics_collector.go:86`

### 10.3 入口
- Gateway handler 调用服务层做账号选择、转发、计费、用量和并发控制。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:37`
- Wire 装配时把大量后台 service 纳入生命周期管理。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/wire.go:111`
- Admin routes 调用 service 层管理账号、设置、ops、支付、系统更新和风控。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/admin.go:278`

### 10.4 logic
- 通用调度先筛掉不适合的候选，再按优先级、负载、最近使用等策略排序并尝试拿槽。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1535`
- OpenAI 分层调度优先处理 previous response，其次 session sticky，再走负载平衡。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_account_scheduler.go:254`
- 调度快照启动时重建缓存，之后用 outbox 和定期 full rebuild 更新。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/scheduler_snapshot_service.go:67`
- 并发服务同时管理账号级和用户级槽，Redis 异常时部分路径 fail-open，等待计数有 cleanup。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/concurrency_service.go:126`
- 计费缓存服务有固定 worker pool、非阻塞队列和调用方 fallback。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/billing_cache_service.go:160`
- 用量记录池有默认 worker、队列、超时、overflow 策略和 autoscale。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/usage_record_worker_pool.go:17`
- Token refresh 会周期扫描账号，失败重试，重试耗尽后临时退出调度，并在成功后同步缓存。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/token_refresh_service.go:155`
- 支付订单会校验计划、限额和 provider，然后事务创建订单并调用 provider，调用失败会落失败状态。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/payment_order.go:23`
- Ops 指标采集有 leader lock，定期采集系统/数据库/Redis/并发/账号切换指标并写 heartbeat。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/ops_metrics_collector.go:158`

### 10.5 暴露功能
- 暴露多 provider 兼容、账号池调度、sticky、failover、用量计费、订阅、支付、ops dashboard、token refresh、channel monitor、内容 moderation、备份和系统更新。

### 10.6 HUAKAI 升级点
- HUAKAI 应把 service 目录内能力拆成 contract-first vertical slices。
- HUAKAI 应优先实现调度快照 + outbox + fallback 限流的安全等价，但不能复制 upstream 数据结构。
- HUAKAI 应把用量记录、账单缓存、billing ledger 明确分层；支付/计费属于高风险区，需要 Owner 确认后实现。
- HUAKAI 应为 token refresh、账号临时禁用、sticky 失效、failover 切换建立 scenario acceptance tests。

## 11. `backend/internal/repository/`
### 11.1 用途
- `repository/` 承担数据库、Redis、外部 HTTP、缓存、迁移、备份、OAuth client、scheduler outbox 和 ops 查询。

### 11.2 关键文件
- `ent.go`, `db_pool.go`, `migrations_runner.go`: DB 连接、池和迁移执行。
- `concurrency_cache.go`: Redis 并发槽、等待计数和批量负载查询。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/repository/concurrency_cache.go:13`
- `scheduler_cache.go`, `scheduler_outbox_repo.go`: 调度快照与 outbox 存取。
- `ops_repo*.go`: ops dashboard、趋势、错误、histogram、request details、pre-agg 查询。
- `usage_log_repo.go`, `usage_billing_repo.go`: usage 与 billing 写入/查询。
- `payment_*`: 支付订单、配置、审计相关 DB 访问。

### 11.3 入口
- Service 层通过 repository 接口访问持久层和缓存层。
- Setup 初始化通过 migration runner 应用 SQL。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:330`
- Concurrency service 通过 Redis repository 获取和释放并发槽。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/concurrency_service.go:126`

### 11.4 logic
- 并发缓存用 Redis sorted set 表达账号/用户槽，另有等待计数和启动清理。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/repository/concurrency_cache.go:39`
- 批量负载查询把当前并发和等待数转成 load rate，供调度选择使用。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/repository/concurrency_cache.go:370`
- migration runner 使用嵌入 SQL，并通过 checksum 防止既有 migration 被修改。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/migrations/migrations.go:19`

### 11.5 暴露功能
- 暴露数据库抽象、缓存抽象、外部 client、迁移、备份、ops 查询、usage 查询、账号同步和 OAuth 支撑。

### 11.6 HUAKAI 升级点
- HUAKAI 应把 repository 层按读写路径拆分：gateway hot path、ops 查询、admin mutation、ledger mutation。
- HUAKAI 应为 Redis fail-open/fail-closed 策略做成显式配置和测试。
- HUAKAI 应避免将生产 SQL WHERE 逻辑只藏在 repository；cross-review 要校验测试 stub 与生产查询一致。

## 12. `backend/internal/payment/`
### 12.1 用途
- `payment/` 提供支付 provider 抽象、实例选择、配置解密、限额判断、退款和 provider factory。

### 12.2 关键文件
- `types.go`: 支付方式、订单状态、订单类型、provider 响应抽象。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/types.go:7`
- `load_balancer.go`: provider instance 选择、限额和日用量判断。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/load_balancer.go:79`
- `registry.go`: provider registry。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/registry.go:9`
- `provider/factory.go`: 根据 provider key 创建具体 provider。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/provider/factory.go:9`
- `provider/*.go`: Alipay、WeChat Pay、Stripe 和 EasyPay 适配。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/provider/factory.go:11`

### 12.3 入口
- Payment service 创建订单时选择 provider instance 并调用支付 provider。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/payment_order.go:313`
- Webhook handler 会按订单/provider 解析回调并验证。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/payment_webhook_handler.go:63`

### 12.4 logic
- Provider instance 选择会查询启用实例、按支付方式过滤、批量统计当天占用、检查单笔和日限额、按策略选择，若全部超限则退回全候选。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/load_balancer.go:79`
- 日用量统计把 pending、paid、completed、recharging 等状态纳入容量占用。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/load_balancer.go:167`
- Registry 以线程安全方式保存可用 provider。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/registry.go:9`

### 12.5 暴露功能
- 暴露多支付 provider、支付方式聚合、单笔限制、日限制、provider instance 选择、支付回调验证和退款入口。

### 12.6 HUAKAI 升级点
- HUAKAI 的支付逻辑属于高风险文件域；实现前需要 Owner 确认。
- HUAKAI 应将 provider 选择、订单状态、回调幂等、退款账务和余额/订阅履约拆成独立审计链。
- HUAKAI 应补强支付 provider 健康状态、限额耗尽告警、回调 replay 防护和人工补单流程。

## 13. `backend/internal/pkg/`
### 13.1 用途
- `pkg/` 承载协议转换、provider 客户端、HTTP 工具、错误模型、日志、代理、TLS 指纹、时间、web search 和 OpenAI/Anthropic/Gemini 支撑。

### 13.2 关键文件
- `apicompat/*`: Responses、Messages、Chat Completions 之间的兼容转换。
- `openai/*`, `gemini/*`, `geminicli/*`, `antigravity/*`: provider-specific 请求/响应/OAuth/模型工具。
- `httpclient/*`, `proxyutil/*`, `tlsfingerprint/*`: 出站 HTTP 连接与代理/TLS 行为。
- `errors/*`, `response/*`, `logger/*`: 错误、响应和日志基础设施。
- `websearch/*`: Brave/Tavily provider 与 manager。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:62`

### 13.3 入口
- Service 层调用这些 package 做 protocol transform、OAuth、transport、error mapping 和 web search。
- HTTP server 启动时构建 web search manager。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/http.go:62`

### 13.4 logic
- 包目录是“协议和基础设施工具层”，不直接暴露路由。
- 多 provider 兼容行为通过 service 和 handler 调用进入请求生命周期。

### 13.5 暴露功能
- 暴露协议兼容、OAuth 支撑、代理拨号、TLS 指纹、统一错误、日志、分页和 web search 插件能力。

### 13.6 HUAKAI 升级点
- HUAKAI 应把协议转换抽象成 per-protocol contract tests。
- HUAKAI 应把 TLS 指纹和代理选择作为 provider transport policy，而不是散落工具调用。
- HUAKAI 应把 web search 从 gateway 核心拆成 plugin/feature flag，避免默认扩大数据外流面。

## 14. `backend/ent/`
### 14.1 用途
- `backend/ent/` 是 Ent ORM schema 与生成代码区域。
- Schema 覆盖账号、分组、API key、用户、usage、payment、promo、proxy、channel monitor、error passthrough、TLS、安全 secret、订阅、auth identity 等实体。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:18`

### 14.2 关键文件
- `backend/ent/generate.go`: Ent 代码生成入口。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:199`
- `backend/ent/schema/account.go`: 账号实体、调度字段、关联和索引。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:18`
- `backend/ent/schema/payment_order.go`, `payment_provider_instance.go`, `payment_audit_log.go`: 支付域 schema。
- `backend/ent/schema/usage_log.go`: 使用记录 schema。
- `backend/ent/schema/auth_identity*.go`, `pending_auth_session.go`: 身份/OAuth schema。

### 14.3 入口
- Repository 层通过 Ent client 访问数据库。
- 开发流程要求 schema 变更后运行生成并提交生成代码。`Wei-Shaw/sub2api@dbc8ae658cfc:DEV_GUIDE.md:199`

### 14.4 logic
- 账号 schema 包含凭证、扩展信息、代理、并发、优先级、倍率、状态、最近使用、过期、可调度、限流、过载、临时不可调度和会话窗口。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:50`
- 账号与分组是多对多，账号与代理是一对一，账号与 usage log 有关联。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:199`
- 账号 schema 有平台、类型、状态、代理、优先级、最近使用、可调度、限流和软删除等索引。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:217`

### 14.5 暴露功能
- 暴露持久化模型、关系、索引和生成代码。
- 对外功能通过 repository/service/handler 间接暴露。

### 14.6 HUAKAI 升级点
- HUAKAI 不应复制 schema 字段和 Ent 结构；应从行为 contract 反推自己的 MIT schema。
- HUAKAI 应把多租户、双版本、PostgreSQL 和审计要求作为 schema 主约束。
- HUAKAI 的 schema migration 属高风险文件域，执行前需要 Owner 确认。

## 15. `backend/migrations/`
### 15.1 用途
- `migrations/` 存放嵌入式 SQL migration，用于初始化和演进 PostgreSQL schema。

### 15.2 关键文件
- `migrations.go`: 以 embed 方式打包 SQL migration。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/migrations/migrations.go:12`
- `001_init.sql` 到 `135_*`: 覆盖账号、订阅、usage 聚合、ops、scheduler outbox、模型路由、TOTP、安全 secret、idempotency、channel、payment、auth identity、channel monitor、affiliate、内容 moderation 等。
- `README.md`: migration 目录说明。

### 15.3 入口
- Setup 初始化时会调用 migration runner。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:330`
- Docker auto setup 也会在首次运行时应用 migration。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:109`

### 15.4 logic
- Migration 文件被嵌入二进制，部署时不依赖外部 SQL 文件。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/migrations/migrations.go:1`
- Migration 命名要求零填充数字前缀、幂等、checksum 校验，不应修改已应用文件。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/migrations/migrations.go:14`
- README 说明 PostgreSQL migration 按字典序执行，并记录文件名与 checksum。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:127`

### 15.5 暴露功能
- 暴露数据库自动初始化、升级、schema 演进和校验能力。
- 不暴露用户 API，但直接影响所有业务持久化。

### 15.6 HUAKAI 升级点
- HUAKAI 应把 migration 纳入 release gate：前滚、回滚方案、数据 backfill、checksum、dry run、生产锁策略。
- HUAKAI 不应复制 SQL；应根据 HUAKAI schema 设计自己的 migrations。
- HUAKAI 对 billing/auth/quota schema 变更必须走高风险 Owner 确认。

## 16. `backend/internal/setup/`
### 16.1 用途
- `setup/` 支撑首次安装、Web setup wizard、CLI setup、Docker auto setup、配置生成和安装锁。

### 16.2 关键文件
- `setup.go`: setup 状态、连接测试、安装、auto setup 和配置写入。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:147`
- `handler.go`: setup HTTP 路由、输入校验、并发安装保护和重启触发。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/handler.go:21`
- `cli.go`: CLI wizard 和终端输入校验。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/cli.go:48`

### 16.3 入口
- Main 进程根据 setup 状态进入 setup server。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/cmd/server/main.go:99`
- Web setup 路由位于 `/setup` 分组。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/handler.go:21`
- Docker auto setup 由环境变量触发。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:510`

### 16.4 logic
- Setup 状态需要配置文件和安装锁都不存在才允许进入。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:147`
- Web setup 对主机、端口、数据库名、用户名、邮箱、密码、SSL mode 做输入校验。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/handler.go:65`
- 安装流程测试数据库和 Redis、应用 migration、创建 admin、写配置、创建安装锁。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:275`
- Auto setup 从环境变量组装数据库、Redis、admin、server、JWT 和 timezone。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:534`

### 16.5 暴露功能
- 暴露 setup status、数据库测试、Redis 测试、安装 API、CLI wizard 和 auto setup。

### 16.6 HUAKAI 升级点
- HUAKAI setup 应加入 bootstrap audit、secret rotation 指引、禁止公网 setup 暴露和 owner lock。
- HUAKAI 应把 setup 安装锁、防重入、输入校验、服务重启和失败恢复写成 acceptance tests。
- HUAKAI 应把 Docker auto setup 的自动密码展示改成更安全的一次性 secret retrieval。

## 17. `backend/internal/web/`
### 17.1 用途
- `web/` 控制前端是否嵌入后端二进制，以及嵌入时如何服务 SPA 与注入公开设置。

### 17.2 关键文件
- `embed_on.go`: 嵌入前端、HTML 缓存、CSP nonce、公开设置注入和静态资源服务。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_on.go:27`
- `embed_off.go`: 未嵌入时返回 404 提示。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_off.go:1`
- `html_cache.go`: HTML 注入缓存。

### 17.3 入口
- Router 在可用时把前端 middleware 插入请求链。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/router.go:53`

### 17.4 logic
- 嵌入模式会跳过 API 路由，对 SPA route 返回注入后的 index HTML。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_on.go:84`
- HTML 注入会把公开设置写入 window 配置，并替换 nonce placeholder。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_on.go:142`
- 本地 override 文件可优先于嵌入文件服务。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_on.go:126`

### 17.5 暴露功能
- 暴露单服务前端托管、公开设置预注入、站点标题预渲染、ETag 和 no-cache revalidation。

### 17.6 HUAKAI 升级点
- HUAKAI 应把公开设置注入做成严格 JSON escaping、安全头和 CSP 测试。
- HUAKAI 应确认本地 override 是否会扩大供应链风险；若保留，必须加审计和只读部署策略。
- HUAKAI 应把前端版本与后端 API schema 兼容性纳入 release gate。

## 18. `frontend/`
### 18.1 用途
- `frontend/` 是 Vue 管理台与用户自助门户。
- 它覆盖 setup、登录注册、用户 dashboard、API key、用量、支付、订阅、admin dashboard、账号、ops、channel、风控和联盟等页面。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:17`

### 18.2 关键文件
- `frontend/package.json`: dev/build/lint/typecheck/test 脚本和前端依赖。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/package.json:6`
- `frontend/src/main.ts`: Vue/Pinia/router/i18n bootstrap 与公开设置预加载。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/main.ts:17`
- `frontend/src/router/index.ts`: 所有页面路由和 navigation guard。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:17`
- `frontend/src/api/client.ts`: Axios base client、token refresh、错误处理。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:12`
- `frontend/src/stores/auth.ts`: token、refresh、用户状态和 run mode。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/auth.ts:71`
- `frontend/src/views/admin/ops/OpsDashboard.vue`: ops dashboard 主页面。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/ops/OpsDashboard.vue:1`
- `frontend/src/views/admin/AccountsView.vue`: 账号管理主页面。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/AccountsView.vue:1`
- `frontend/src/views/user/PaymentView.vue`: 用户充值/订阅购买路径。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/user/PaymentView.vue:1`

### 18.3 入口
- Frontend bootstrap 会先应用主题、安装 Pinia、读取服务端注入公开设置，再挂载 router 和 i18n。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/main.ts:17`
- Router 定义 setup、public、user、admin、payment admin 和 404。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:17`
- Axios client 默认调用 `/api/v1`。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:12`

### 18.4 logic
- Navigation guard 恢复 auth、设置标题、执行 auth/admin/payment/risk/simple/backend mode 限制。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:685`
- 前端对 chunk load 错误有一次 reload 防护。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:830`
- Axios client 会附加 bearer token、语言和 GET timezone；401 时用 refresh token 合并并发刷新。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:56`
- App store 优先使用服务端注入公开配置，避免页面闪烁。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/app.ts:287`

### 18.5 暴露功能
- 暴露完整用户自助界面：API key、usage、兑换、订阅、支付、订单、profile、可用 channel、监控状态。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:157`
- 暴露完整 admin 界面：dashboard、ops、用户、分组、渠道、账号、公告、代理、设置、风控、用量、联盟和支付管理。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:342`

### 18.6 HUAKAI 升级点
- HUAKAI 前端应以 Admin Ops Platform 为主，不应只做 reference UI parity。
- HUAKAI 应把 route meta、feature flags、run mode、backend mode、payment/risk guard 变成统一权限策略。
- HUAKAI 应对 token refresh 并发、401 清理、backend mode 跳转、chunk reload 做浏览器回归测试。

## 19. `frontend/src/api/`
### 19.1 用途
- `api/` 是前端到后端 API 的 typed client 层。

### 19.2 关键文件
- `api/client.ts`: base client、interceptor、token refresh。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:84`
- `api/auth.ts`: 登录、注册、2FA、refresh、OAuth pending、公开设置。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/auth.ts:91`
- `api/payment.ts`: 用户支付配置、订单、公开验证、退款请求。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/payment.ts:19`
- `api/setup.ts`: setup 状态、数据库/Redis 测试、安装。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/setup.ts:1`
- `api/admin/accounts.ts`: 账号 CRUD、批量、OAuth、导入导出、状态恢复。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/admin/accounts.ts:32`
- `api/admin/ops.ts`: ops dashboard、并发、availability、实时 WebSocket、alerts 等 client。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/admin/ops.ts:397`

### 19.3 入口
- 页面和 store 通过 `api/` 调用后端。
- Axios client 是 API layer 共同入口。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:14`

### 19.4 logic
- 401 refresh 使用单飞队列，避免多个请求同时刷新 token。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/client.ts:151`
- Ops WebSocket 避免把 admin token 放在 URL query，而用浏览器支持的子协议传递。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/admin/ops.ts:510`
- Ops WebSocket 有 reconnect、offline、stale close 和 fatal close 处理。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/admin/ops.ts:613`
- Accounts client 支持 ETag、批量修改、今日统计、临时不可调度、CRS 同步、数据导入导出和 token refresh。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/api/admin/accounts.ts:67`

### 19.5 暴露功能
- 暴露前端调用 contract、错误模型、refresh 语义、WebSocket 订阅和 admin 操作入口。

### 19.6 HUAKAI 升级点
- HUAKAI 应生成或校验 OpenAPI/typed client，减少手写 contract 漂移。
- HUAKAI 应把 WebSocket admin auth、reconnect、stale detection 和 feature-flag关闭写进测试。
- HUAKAI 应统一处理 sensitive errors，避免前端 toast 泄漏后端细节。

## 20. `frontend/src/router/`
### 20.1 用途
- Router 定义所有前端页面、权限元信息、标题、feature-flag 门和导航错误恢复。

### 20.2 关键文件
- `index.ts`: 主路由表和 guard。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:17`
- `title.ts`: 页面标题解析。
- `__tests__/guards.spec.ts`: guard 测试。

### 20.3 入口
- `main.ts` 在 app 装配时安装 router，并等待首次导航完成后挂载。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/main.ts:37`

### 20.4 logic
- Guard 首次导航恢复本地 auth 状态。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:685`
- Backend mode 会限制未认证 public route，admin 才能进入受保护区域。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:798`
- Simple mode 会限制分组、订阅、兑换等部分页面。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:781`
- Payment 与 risk 页面受公开设置开关控制。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:764`

### 20.5 暴露功能
- 暴露访问控制、页面发现、深链和 feature flag 级 UI gating。

### 20.6 HUAKAI 升级点
- HUAKAI 应把前端 route guard 与后端 RBAC/feature flags 双向校验。
- HUAKAI 应加入“前端隐藏但后端仍可访问”的安全测试。

## 21. `frontend/src/stores/`
### 21.1 用途
- Stores 管理 auth、app settings、payment、subscriptions、announcements、onboarding 和 admin settings。

### 21.2 关键文件
- `auth.ts`: 用户、token、refresh、run mode、pending OAuth session。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/auth.ts:71`
- `app.ts`: 主题/UI/toast/version/公开设置缓存。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/app.ts:17`
- `payment.ts`: 支付配置、当前订单、计划。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/payment.ts:11`
- `subscriptions.ts`: 订阅缓存、去重请求和轮询。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/subscriptions.ts:11`

### 21.3 入口
- Router guard、页面和组件调用 stores。
- App bootstrap 先初始化 app store 中的公开配置。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/main.ts:25`

### 21.4 logic
- Auth store 从 localStorage 恢复 token/user/refresh token，并启动用户自动刷新和 token proactive refresh。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/auth.ts:103`
- Auth store 支持 2FA 结果分支、OAuth 回调 token 设置和 pending session 保留。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/auth.ts:240`
- App store 用服务端注入配置填充站点名称、logo、API base、文档 URL 和 feature flags。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/app.ts:290`
- Subscription store 有 60 秒缓存、in-flight 去重和 5 分钟轮询。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/stores/subscriptions.ts:11`

### 21.5 暴露功能
- 暴露前端 session、公开设置、toast、版本检查、支付状态和订阅状态。

### 21.6 HUAKAI 升级点
- HUAKAI 应把 token 存储风险评估清楚：localStorage、cookie、refresh token、XSS、退出登录和 session revocation。
- HUAKAI 应把 run mode、feature flags 和版本缓存做成统一前端 config contract。

## 22. `frontend/src/views/`
### 22.1 用途
- Views 是用户和管理员的具体页面集合。

### 22.2 关键文件
- `views/setup/SetupWizardView.vue`: 首次安装 wizard。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/setup/SetupWizardView.vue:1`
- `views/auth/*.vue`: 登录、注册、OAuth callback、密码流程。
- `views/user/KeysView.vue`: 用户 API key 管理和分组选择。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/user/KeysView.vue:1`
- `views/user/PaymentView.vue`: 充值/订阅、支付方式、恢复和跳转。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/user/PaymentView.vue:1`
- `views/admin/AccountsView.vue`: 账号表、筛选、批量、导入导出、调度状态。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/AccountsView.vue:1`
- `views/admin/ops/OpsDashboard.vue`: 运维 dashboard。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/ops/OpsDashboard.vue:1`

### 22.3 入口
- Router lazy-load 各视图。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:17`

### 22.4 logic
- Setup wizard 分步骤验证数据库、Redis、admin，再调用 install 并轮询服务重启。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/setup/SetupWizardView.vue:559`
- Accounts view 支持筛选、排序、列隐藏、自动刷新、ETag、批量操作、今日统计和工具菜单。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/AccountsView.vue:532`
- Ops dashboard 支持 URL query 同步、深链、自动刷新、并发、吞吐、错误、延迟、OpenAI token stats、系统日志和配置弹窗。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/ops/OpsDashboard.vue:42`
- Payment view 支持充值与订阅两个 tab、支付方式限制、手续费预览、WeChat OAuth/JSAPI、Stripe 路径和恢复快照。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/user/PaymentView.vue:656`

### 22.5 暴露功能
- 暴露完整 admin ops UI、账号池 UI、用户 API key UI、订阅/充值 UI、setup UI 和 auth UI。

### 22.6 HUAKAI 升级点
- HUAKAI 的 ops UI 应以运营动作闭环为核心：发现、定位、重试、隔离、恢复、审计。
- HUAKAI 账号池 UI 应显示调度解释、容量原因、健康原因、quota 原因和最近 failover 证据。
- HUAKAI 支付 UI 应支持订单恢复、失败原因解释、人工处理入口和账务审计。

## 23. `frontend/src/components/`
### 23.1 用途
- Components 复用 UI 构件：账号单元格、admin 表格弹窗、channel monitor、payment、layout、common controls、charts、keys 和用户 profile。

### 23.2 关键文件
- `components/account/*`: 账号容量、quota、状态、测试、创建、编辑、批量、OAuth、临时不可调度。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/components/account/AccountStatusIndicator.vue:1`
- `components/admin/account/*`: admin 账号表筛选、动作、批量、测试、调度测试面板。
- `components/admin/payment/*`: 订单、退款、收入图、支付方式图。
- `components/admin/monitor/*`: channel monitor 表单、模板、运行结果。
- `components/common/*`: DataTable、dialog、pagination、selector、toast、status badge。
- `components/layout/*`: AppLayout、Sidebar、Header、TablePageLayout。

### 23.3 入口
- Views 按需组合 components。
- Accounts view 和 Ops dashboard 是最大组件聚合入口。`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/AccountsView.vue:377`

### 23.4 logic
- Components 把复杂页面拆成状态 badge、容量 cell、批量条、弹窗、图表和表格。
- 账号组件覆盖调度可见性、容量、quota、usage、OAuth 重新授权和同步导入。

### 23.5 暴露功能
- 暴露可复用 admin 操作体验、数据表、批量操作、图表和支付控件。

### 23.6 HUAKAI 升级点
- HUAKAI 应复用自己的 design system，不复制 upstream component 结构。
- HUAKAI 应把 ops、账号、支付、usage 组件的 loading/error/empty/permission states 标准化。
- HUAKAI 应对表格虚拟滚动、列隐藏、批量操作和导入导出做 E2E 测试。

## 24. `deploy/`
### 24.1 用途
- `deploy/` 提供 Docker Compose、二进制安装、systemd、示例配置、数据管理守护进程说明、Caddy 和入口脚本。

### 24.2 关键文件
- `deploy/README.md`: 部署方式、auto setup、migration、数据迁移和环境变量。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:5`
- `deploy/docker-compose.yml`: app、PostgreSQL、Redis、healthcheck、env 配置。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-compose.yml:14`
- `deploy/docker-compose.local.yml`: local directory persistence 变体。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:100`
- `deploy/docker-deploy.sh`: 下载 compose/env、生成 secret、创建 data 目录。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-deploy.sh:53`
- `deploy/install.sh`: release 二进制安装、systemd、版本安装/回滚。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/install.sh:18`
- `deploy/docker-entrypoint.sh`: 数据目录权限修复和非 root 重入。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-entrypoint.sh:1`
- `deploy/sub2api.service`: systemd service hardening。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/sub2api.service:1`
- `deploy/config.example.yaml`: 生产配置样例。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/config.example.yaml:13`

### 24.3 入口
- Docker 推荐路径是运行一键准备脚本，然后 compose up。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:30`
- 二进制路径是运行安装脚本，然后通过 setup wizard 完成配置。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:5`
- Docker app container 通过 `AUTO_SETUP=true` 自动初始化。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-compose.yml:38`

### 24.4 logic
- Docker Compose 使用 app、PostgreSQL、Redis 三服务，并等待 DB/Redis healthcheck。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-compose.yml:156`
- Compose 注入数据库、Redis、admin、JWT、TOTP、timezone、OAuth、安全和图片并发相关环境变量。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-compose.yml:38`
- Entrypoint 在 root 启动时修正数据目录权限，然后以应用用户运行。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/docker-entrypoint.sh:4`
- 安装脚本检测平台、拉 release、校验 checksum、创建系统用户、写 systemd 和启动服务。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/install.sh:415`
- Systemd service 使用非 root 用户、严格系统保护、私有临时目录和写路径限制。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/sub2api.service:19`

### 24.5 暴露功能
- 暴露一键 Docker、local directory migration、named volume、二进制安装、升级、回滚、卸载、systemd、数据管理守护进程和配置样例。

### 24.6 HUAKAI 升级点
- HUAKAI 应把部署方式分为 dev、single-node prod、multi-node prod、managed SaaS。
- HUAKAI 应加 secret handling：生成、保存、轮换、不可日志泄漏、最小权限。
- HUAKAI 应把 backup/restore、migration rollback、版本回滚和 health gate 写入运维手册。
- HUAKAI 的部署脚本属于高风险文件域，修改前需要 Owner 确认。

## 25. `docs/`
### 25.1 用途
- Reference repo 的 `docs/` 主要是支付和 admin payment integration 文档。

### 25.2 关键文件
- `docs/PAYMENT.md`: 支付功能英文说明。
- `docs/PAYMENT_CN.md`: 支付功能中文说明。
- `docs/ADMIN_PAYMENT_INTEGRATION_API.md`: 管理端支付集成 API 说明。

### 25.3 入口
- 用户和管理员通过 README 或前端支付/管理入口间接需要这些文档。
- 本轮只列出 docs 文件，没有展开逐段支付文档，因为支付路径已从源码和路由读取。

### 25.4 logic
- Docs 目录承载功能说明，不执行运行逻辑。

### 25.5 暴露功能
- 暴露支付集成说明、支付配置说明和管理端支付 API 使用说明。

### 25.6 HUAKAI 升级点
- HUAKAI 需要自己的支付合规文档、账务语义、退款流程、webhook retry 策略和人工处理 SOP。
- HUAKAI 不应直接复用 reference 文档文本。

## 26. `tools/`
### 26.1 用途
- `tools/` 当前观察到主要承担前端 audit exception 校验。

### 26.2 关键文件
- `tools/check_pnpm_audit_exceptions.py`: 读取 audit JSON 和 exception YAML，校验高危/严重漏洞是否有未过期例外。`Wei-Shaw/sub2api@dbc8ae658cfc:tools/check_pnpm_audit_exceptions.py:142`

### 26.3 入口
- Security scan workflow 调用该脚本。`Wei-Shaw/sub2api@dbc8ae658cfc:.github/workflows/security-scan.yml:54`

### 26.4 logic
- 脚本轻量解析 exception 文件，要求包名、advisory、severity、mitigation 和 expires_on 等字段。`Wei-Shaw/sub2api@dbc8ae658cfc:tools/check_pnpm_audit_exceptions.py:8`
- 它从 pnpm audit 的不同 JSON 结构中提取高危/严重漏洞并匹配例外。`Wei-Shaw/sub2api@dbc8ae658cfc:tools/check_pnpm_audit_exceptions.py:64`
- 它会拒绝缺少例外、过期例外或 severity 不匹配。`Wei-Shaw/sub2api@dbc8ae658cfc:tools/check_pnpm_audit_exceptions.py:191`

### 26.5 暴露功能
- 暴露安全异常治理能力。
- 不暴露用户 API。

### 26.6 HUAKAI 升级点
- HUAKAI 应把该类脚本升级为 dependency/license/security gate。
- HUAKAI 应同时审计 MIT 兼容性、transitive license、CVE、abandonware 和 exception 到期。
- HUAKAI 的 release gate 应禁止无 Owner、无到期日、无替代计划的漏洞例外。

## 27. 横向工作流观察
本节只汇总正文已展开的跨目录链路，不新增无证据事实：首次安装链路由部署文档、setup 服务和前端嵌入组成。`Wei-Shaw/sub2api@dbc8ae658cfc:deploy/README.md:109` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/setup/setup.go:275` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/web/embed_on.go:142`

Gateway 请求链路由 route middleware、handler 生命周期、service 调度/failover 和异步 usage 写入组成。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:25` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_handler.go:113` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/gateway_service.go:1401` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/usage_record_worker_pool.go:143`

OpenAI 专属链路覆盖 Responses/Chat/Images/alias routes、handler 特化和 WS/HTTP adapter 行为。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:43` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/openai_gateway_handler.go:82` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_gateway_service.go:2164`

生产运营链路集中在调度快照/outbox、并发等待队列、billing/usage、token refresh、支付 webhook 和 Ops dashboard；HUAKAI 应把这些拆成可审计 trace、状态机、幂等账务、人工恢复和 dashboard action，而不是复制 reference 的局部结构。`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/scheduler_snapshot_service.go:67` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/concurrency_service.go:15` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/billing_cache_service.go:86` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/token_refresh_service.go:155` `Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/payment_webhook_handler.go:63` `Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/ops/OpsDashboard.vue:42`

## 28. HUAKAI 升级 punch list
| 优先级 | HUAKAI 升级项 | Reference 观察证据 | clean-room 实现方式 |
|---|---|---|---|
| P0 | Gateway 请求生命周期 trace | 路由链、handler 循环、service 调度分层存在 | 自研 event model + trace span + acceptance tests |
| P0 | 调度快照 + outbox + fallback 限流 | scheduler snapshot 启动、outbox 和 fallback 限制 | 设计 HUAKAI 自有 cache/outbox schema，不复制字段 |
| P0 | Failover 状态机 | failover loop 管理 retry、切换和临时禁用 | 写状态图、测试矩阵、实现自有状态结构 |
| P0 | 用户/账号并发等待队列 | Redis 槽位、等待计数、batch load | 自研 Redis key 规范和 Lua/事务策略，配故障测试 |
| P0 | Usage 与 billing 分层 | billing cache、usage worker pool、计费价格维度 | Ledger、usage display、billing cache 分三层 |
| P0 | Admin Ops 操作闭环 | ops routes 和 dashboard 覆盖 alerts/errors/retry/logs | 以操作恢复为中心设计 UI，不复制组件 |
| P0 | Auth/OAuth session 安全 | 登录、2FA、OAuth pending、refresh token | 自研 session model，XSS/CSRF/refresh replay 测试 |
| P1 | OpenAI WS/HTTP adapter | OpenAI service 分 WS 和 HTTP 路径 | Provider capability contract + per-protocol tests |
| P1 | Multi-provider payment plugin | payment provider registry/load balancing | 高风险 Owner 确认后自研 plugin contract |
| P1 | Webhook 幂等和未知订单处理 | webhook 对未知订单 ack | 自研 webhook event ledger + replay guard |
| P1 | Setup wizard 安全硬化 | setup guard、install lock、input validation | 加 bootstrap owner lock、public exposure warning |
| P1 | Single binary + embedded frontend | Dockerfile 和 web embed | 可选实现，严格 CSP/JSON escaping/版本校验 |
| P1 | Secret lifecycle | Docker deploy 生成 secret | 加 secret rotation、不可日志泄漏和 recovery |
| P1 | Migration release gate | embedded SQL + checksum | HUAKAI 自有 migration framework + dry run |
| P1 | Feature flag 与 run mode 权限 | router guard、Simple/backend mode | 统一 policy engine，前后端双校验 |
| P1 | Channel monitor 与可用渠道 | routes/components/api 存在 | 作为 Ops plugin，实现可观测检查模板 |
| P2 | Partner/assets 授权治理 | assets/logo 目录存在 | 建素材授权清单，不复制资源 |
| P2 | Audit exception 治理 | pnpm audit exceptions 脚本 | 扩展为 license/security/SBOM gate |
| P2 | Frontend route contract snapshot | router 大量 lazy routes | 自动生成 route manifest，与后端 feature flags 对齐 |
| P2 | Data management daemon | deploy docs 提到 socket 联动 | 明确 operator 权限、host boundary、审计和隔离 |

## 29. Open Questions
- OQ-1: reference 的多节点部署下，调度 outbox watermark、Redis leader lock 和 cache lag 的实际生产表现没有从运行记录验证。
- OQ-2: 支付 provider 的合规边界、资金托管责任和退款账务细节需要单独审计。
- OQ-3: 数据管理守护进程通过宿主机 socket 联动，权限边界需要进一步 source-read 和 threat model。
- OQ-4: 前端 localStorage token 策略在高 XSS 风险场景下的安全性需要 HUAKAI 自行评估。
- OQ-5: Simple Mode 与 HUAKAI dual-edition 的商业/权限语义不能直接等同。
- OQ-6: Web search emulation 的数据外流边界需要单独产品决策。
- OQ-7: Reference 中大量 provider/model 特例变化快，HUAKAI 应以 contract + plugin 更新，而不是硬编码追随。

## 30. Source Coverage Proof
逐文件 contribution 已压缩列出，正文每个行为判断仍保留 file:line anchor。根层: `README.md` 用于定位、功能、安装、安全、模式和 license；`DEV_GUIDE.md` 用于技术栈、CI、lockfile 和生成约束；`Dockerfile`/`Makefile` 用于构建、嵌入前端、镜像和测试入口。

CI/工具层: `.github/workflows/backend-ci.yml`、`release.yml`、`security-scan.yml` 用于 CI、release 和扫描；`.github/audit-exceptions.yml` 与 `tools/check_pnpm_audit_exceptions.py` 用于依赖漏洞例外治理。

后端入口/配置/路由层: `backend/go.mod`、`backend/cmd/server/main.go`、`wire.go`、`backend/internal/config/config.go`、`backend/internal/server/http.go`、`router.go`、`routes/common.go`、`routes/gateway.go`、`routes/admin.go`、`routes/auth.go`、`routes/user.go`、`routes/payment.go` 用于依赖、启动、DI、HTTP server、middleware 和 API surface。

后端核心链路层: `backend/internal/handler/gateway_handler.go`、`openai_gateway_handler.go`、`gateway_handler_responses.go`、`failover_loop.go`、`gateway_helper.go`、`payment_webhook_handler.go`、`backend/internal/service/gateway_service.go`、`openai_gateway_service.go`、`openai_account_scheduler.go`、`scheduler_snapshot_service.go`、`scheduler_outbox.go`、`concurrency_service.go`、`billing_service.go`、`billing_cache_service.go`、`usage_record_worker_pool.go`、`token_refresh_service.go`、`payment_order.go`、`ops_metrics_collector.go`、`backend/internal/repository/concurrency_cache.go`、`backend/internal/payment/types.go`、`load_balancer.go`、`registry.go`、`provider/factory.go` 用于 gateway、OpenAI adapter、调度、并发、usage、billing、refresh、payment、ops 和 repository 行为。

后端安装/数据/前端嵌入层: `backend/internal/setup/setup.go`、`handler.go`、`cli.go` 用于 setup；`backend/internal/web/embed_on.go`、`embed_off.go` 用于嵌入前端；`backend/ent/schema/account.go` 和 `backend/migrations/migrations.go` 用于账号数据行为与 migration 机制。

部署层: `deploy/README.md`、`docker-compose.yml`、`docker-deploy.sh`、`docker-entrypoint.sh`、`install.sh`、`sub2api.service`、`config.example.yaml` 用于 Docker、systemd、secret、healthcheck、env、migration 和运行配置证据。

前端层: `frontend/package.json`、`src/main.ts`、`src/router/index.ts`、`src/api/client.ts`、`auth.ts`、`payment.ts`、`setup.ts`、`admin/accounts.ts`、`admin/ops.ts`、`src/stores/auth.ts`、`app.ts`、`payment.ts`、`subscriptions.ts`、`src/views/setup/SetupWizardView.vue`、`admin/AccountsView.vue`、`admin/ops/OpsDashboard.vue`、`user/KeysView.vue`、`user/PaymentView.vue` 用于 bootstrap、路由守卫、API client、状态、setup/admin/user/payment UI 行为。

目录清单层: `backend/internal`、`backend/ent/schema`、`backend/migrations`、`frontend/src`、`deploy`、`assets`、`docs` 的目录清单用于 skeleton；`frontend/src/components/*` 和 `assets/partners/logos/*` 只用于目录能力梳理；`docs/PAYMENT.md`、`docs/PAYMENT_CN.md`、`docs/ADMIN_PAYMENT_INTEGRATION_API.md` 仅列名，未作为细节事实来源。

## 31. 中文 Owner 摘要
本轮真实观察主要来自 `sub2api` 当前 HEAD 的 README、部署脚本、后端入口、路由、handler、service、repository、migration、前端 router/store/API/view、CI 和工具脚本；合理推断集中在 HUAKAI 如何把这些能力 clean-room 升级为可审计、可测试、MIT 自有实现；open questions 共 7 个，主要是多节点调度、支付合规、数据管理守护进程、token 存储和商业模式映射。没有功能缩水：reference 中观察到的网关、多账号、调度、计费、支付、ops、setup、frontend、deploy 能力都已映射到 punch list。Clean-room 风险存在于 license、schema、目录结构和 UI 源码复用，本报告只做行为证据总结，不提供可复制实现。安全风险主要集中在支付、auth/session、secret、setup 暴露、migration、WebSocket admin auth 和数据管理守护进程。需要 Owner 确认的高风险项是支付/账务、auth core、quota/billing ledger、database schema 和部署脚本落地方式。下一步建议先把 P0 punch list 转成 HUAKAI acceptance tests，再由实现 lane 按 vertical slice 做 MIT clean-room 实现。

Source files read: README.md; DEV_GUIDE.md; Dockerfile; Makefile; .github/workflows/backend-ci.yml; .github/workflows/release.yml; .github/workflows/security-scan.yml; .github/audit-exceptions.yml; tools/check_pnpm_audit_exceptions.py; backend/go.mod; backend/cmd/server/main.go; backend/cmd/server/wire.go; backend/internal/config/config.go; backend/internal/server/http.go; backend/internal/server/router.go; backend/internal/server/routes/common.go; backend/internal/server/routes/gateway.go; backend/internal/server/routes/admin.go; backend/internal/server/routes/auth.go; backend/internal/server/routes/user.go; backend/internal/server/routes/payment.go; backend/internal/handler/gateway_handler.go; backend/internal/handler/openai_gateway_handler.go; backend/internal/handler/gateway_handler_responses.go; backend/internal/handler/failover_loop.go; backend/internal/handler/gateway_helper.go; backend/internal/handler/payment_webhook_handler.go; backend/internal/service/gateway_service.go; backend/internal/service/openai_gateway_service.go; backend/internal/service/openai_account_scheduler.go; backend/internal/service/scheduler_snapshot_service.go; backend/internal/service/scheduler_outbox.go; backend/internal/service/concurrency_service.go; backend/internal/service/billing_service.go; backend/internal/service/billing_cache_service.go; backend/internal/service/usage_record_worker_pool.go; backend/internal/service/token_refresh_service.go; backend/internal/service/payment_order.go; backend/internal/service/ops_metrics_collector.go; backend/internal/repository/concurrency_cache.go; backend/internal/payment/types.go; backend/internal/payment/load_balancer.go; backend/internal/payment/registry.go; backend/internal/payment/provider/factory.go; backend/internal/setup/setup.go; backend/internal/setup/handler.go; backend/internal/setup/cli.go; backend/internal/web/embed_on.go; backend/internal/web/embed_off.go; backend/ent/schema/account.go; backend/migrations/migrations.go; deploy/README.md; deploy/docker-compose.yml; deploy/docker-deploy.sh; deploy/docker-entrypoint.sh; deploy/install.sh; deploy/sub2api.service; deploy/config.example.yaml; frontend/package.json; frontend/src/main.ts; frontend/src/router/index.ts; frontend/src/api/client.ts; frontend/src/api/auth.ts; frontend/src/api/payment.ts; frontend/src/api/setup.ts; frontend/src/api/admin/accounts.ts; frontend/src/api/admin/ops.ts; frontend/src/stores/auth.ts; frontend/src/stores/app.ts; frontend/src/stores/payment.ts; frontend/src/stores/subscriptions.ts; frontend/src/views/setup/SetupWizardView.vue; frontend/src/views/admin/AccountsView.vue; frontend/src/views/admin/ops/OpsDashboard.vue; frontend/src/views/user/KeysView.vue; frontend/src/views/user/PaymentView.vue; directory listings for backend/internal, backend/ent/schema, backend/migrations, frontend/src, deploy, assets, docs.
Lane: specifier
Agent: GPT-5 Codex / Codex lane
UTC timestamp: 2026-05-13T08:29:41Z
