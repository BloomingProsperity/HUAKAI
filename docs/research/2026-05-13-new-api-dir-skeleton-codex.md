# 2026-05-13 new-api 顶层目录骨架拆解（Codex lane）

| 字段 | 值 |
|---|---|
| Ref | new-api |
| Ref path | `~/refs/new-api/` |
| Upstream evidence anchor | `Calcium-Ion/new-api@d146e45e2f95` |
| Last commit date | 2026-05-09 |
| Lane | specifier / codex |
| Mining started | 2026-05-13T07:52:51Z |
| Mining done | 2026-05-13T08:21:30Z |
| Output LoC | 942 |
| Observed regions | 92 |
| Inferences | 44 |
| Open questions | 8 |

## Clean-room 声明

本报告只描述从 `~/refs/new-api/` 观察到的行为、入口、模块边界和可见能力。

引用格式使用 `Calcium-Ion/new-api@d146e45e2f95:<file>:<line>` 作为证据锚点。

除路径、协议名、产品名和端点名外，正文避免复用上游函数名、结构字段名、配置常量名。

不把上游目录结构当作 HUAKAI 实现建议；HUAKAI 升级点只输出能力目标、边界建议和测试场景。

本轮没有读取其他 ref project，没有读取 HUAKAI backend/frontend 代码，没有读取旧 sub2api decomp。

## 顶层快照

new-api 当前提交为 `d146e45e2f95`，最后提交日期为 2026-05-09。

顶层目录包括 `.agents`、`.github`、`bin`、`common`、`constant`、`controller`、`docs`、`dto`、`electron`、`i18n`、`logger`、`middleware`、`model`、`oauth`、`pkg`、`relay`、`router`、`service`、`setting`、`types`、`web`。

启动入口在根目录的 Go 文件中，先初始化环境、日志、数据库、Redis、配置、模型价格、i18n 和自定义登录提供方，再启动 HTTP server；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:50`、`Calcium-Ion/new-api@d146e45e2f95:main.go:258`、`Calcium-Ion/new-api@d146e45e2f95:main.go:281`、`Calcium-Ion/new-api@d146e45e2f95:main.go:305`、`Calcium-Ion/new-api@d146e45e2f95:main.go:327`。

启动后会开启缓存同步、选项同步、额度看板、通道检测、订阅重置、异步任务轮询和通道上游模型检测等后台任务；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:74`、`Calcium-Ion/new-api@d146e45e2f95:main.go:100`、`Calcium-Ion/new-api@d146e45e2f95:main.go:103`、`Calcium-Ion/new-api@d146e45e2f95:main.go:114`、`Calcium-Ion/new-api@d146e45e2f95:main.go:119`、`Calcium-Ion/new-api@d146e45e2f95:main.go:131`。

HTTP 服务把全局中间件、session、双主题前端静态资源和 API/relay/dashboard/video/web 路由集中挂载；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:161`、`Calcium-Ion/new-api@d146e45e2f95:main.go:174`、`Calcium-Ion/new-api@d146e45e2f95:main.go:187`、`Calcium-Ion/new-api@d146e45e2f95:main.go:193`、`Calcium-Ion/new-api@d146e45e2f95:router/main.go:15`。

核心请求路径是：`router` 暴露协议端点，`middleware` 鉴权并选择通道，`controller` 编排请求、预扣、重试和错误处理，`relay` 做协议适配和上游请求，`service` 做计费/转换/缓存/任务，`model` 读写持久化对象。

HUAKAI 升级点总览：new-api 的价值不在单个 adapter，而在“账号/通道管理 + 协议转换 + 预扣/结算 + 运维 UI + 发布工件”被打成一个可运营平台；HUAKAI 应吸收能力面，但重新设计模块边界、审计账本和多租户隔离。

## 根文件（补充，不计入一级目录）

1. **用途**

- 根文件承担单进程启动、嵌入前端、环境/存储初始化、后台任务拉起和路由注册；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:38`、`Calcium-Ion/new-api@d146e45e2f95:main.go:50`、`Calcium-Ion/new-api@d146e45e2f95:main.go:193`。
- 容器、compose、service unit 和 makefile 是部署包装，不是业务域边界；本轮只做文件级存在和 line-count 观察，没有深读这些部署文件。

2. **关键文件**

- `main.go:334 LoC`：启动编排、后台任务和路由注入。
- `go.mod:165 LoC`：Go 依赖约束，证明后端是 Go/Gin/GORM/Redis 生态。
- `Dockerfile:56 LoC`、`Dockerfile.dev:44 LoC`：容器构建入口。
- `docker-compose.yml:114 LoC`、`docker-compose.dev.yml:61 LoC`：部署组合样例。
- `README*.md:459-476 LoC`：多语言产品说明，本报告未用 README 作为能力主证据。

3. **入口 / 调用关系**

- 根入口先装载 `.env`、环境变量、日志、配置、HTTP client、tokenizer、数据库、Redis、指标和 i18n，再回到主服务启动；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:261`、`Calcium-Ion/new-api@d146e45e2f95:main.go:269`、`Calcium-Ion/new-api@d146e45e2f95:main.go:271`、`Calcium-Ion/new-api@d146e45e2f95:main.go:274`、`Calcium-Ion/new-api@d146e45e2f95:main.go:276`、`Calcium-Ion/new-api@d146e45e2f95:main.go:278`、`Calcium-Ion/new-api@d146e45e2f95:main.go:281`、`Calcium-Ion/new-api@d146e45e2f95:main.go:305`。
- 根入口通过 embed 把两套 web build 产物带进二进制，服务端按主题返回不同静态资源；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:38`、`Calcium-Ion/new-api@d146e45e2f95:main.go:44`、`Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:24`。

4. **核心 logic / 算法**

- 根层不是算法层，而是生命周期 orchestrator：初始化顺序、异步任务启动顺序和 web/API 路由边界共同决定运行时形态。
- 主进程对缓存、通道测试、订阅重置、任务轮询和上游模型检测采用后台 goroutine 风格；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:97`、`Calcium-Ion/new-api@d146e45e2f95:main.go:101`、`Calcium-Ion/new-api@d146e45e2f95:main.go:114`、`Calcium-Ion/new-api@d146e45e2f95:main.go:120`、`Calcium-Ion/new-api@d146e45e2f95:main.go:132`。

5. **暴露功能**

- 用户看到一个单体化 AI API gateway：API relay、管理后台、用户/令牌/通道/日志/订阅/支付/模型管理和前端静态页面由同一服务暴露。

6. **HUAKAI 升级点**

- 架构升级：把启动生命周期拆成可观测的 boot phases，每个 phase 有 readiness、失败原因和 operator 可见状态。
- 生态升级：后台任务统一进入 scheduler registry，支持暂停、重跑、租户隔离和审计。
- 安全升级：嵌入式前端与 API gateway 可拆部署，避免静态 UI 与敏感 relay worker 绑定成单一信任域。

## 01 `.agents/`

1. **用途**

- 该目录是 ref 自己给 AI/agent 的本地工作流说明，不是 runtime 业务代码；已观察到 classic/default 前端同步、i18n、shadcn 和 React 性能规则类材料。
- 该目录说明 ref 已把“前端双栈同步”和“UI 组件约束”做成 agent 可复用流程；证据见 `Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:2`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:8`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/i18n-translate/SKILL.md:2`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/shadcn-ui/SKILL.md:2`。

2. **关键文件**

- `.agents/skills/classic-to-default-sync/SKILL.md:84 LoC`：定义 classic 到 default 的功能同步工作流。
- `.agents/skills/i18n-translate/SKILL.md:252+ LoC`：定义多语言同步和缺失 key 检测流程。
- `.agents/skills/shadcn-ui/SKILL.md:105 LoC`：定义 default 前端使用组件系统时的项目上下文。
- `.agents/skills/vercel-react-best-practices/AGENTS.md:2663+ LoC`：引入 React/Next 性能规则库。

3. **入口 / 调用关系**

- 这些文件由人工或 agent 调用，不被 Go runtime import；它们通过路径约定指向 `web/classic` 和 `web/default` 的同步、构建、i18n 操作。
- classic/default 同步文件要求读 classic 变更并映射到 default 实现，说明 ref 内部承认前端双栈有 feature parity 风险；证据见 `Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:22`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:26`。

4. **核心 logic / 算法**

- `.agents` 的核心不是代码算法，而是“人工/agent 操作守则”：先抽取行为差异，再映射目标栈，再按缺口实现或确认已覆盖。
- i18n 工作流把 key 扫描、缺失检测和同步脚本串起来，形成前端文案覆盖治理；证据见 `Calcium-Ion/new-api@d146e45e2f95:.agents/skills/i18n-translate/SKILL.md:15`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/i18n-translate/SKILL.md:18`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/i18n-translate/SKILL.md:249`。

5. **暴露功能**

- operator 看不到 `.agents`，但 contributor 能获得“如何把双前端保持一致”的流程化指导。
- 对项目治理来说，它暴露的是工程质量能力：迁移、同步、翻译、组件规则和性能规则。

6. **HUAKAI 升级点**

- 生态升级：HUAKAI 可以把 agent 工作流升级成 release gate：前端 parity、i18n 覆盖、可访问性和视觉审查必须有机器可读 verdict。
- 架构升级：把 agent skill 与真实 CI/PR check 连接，避免仅靠文档提醒。
- Clean-room 升级：任何 ref mining skill 必须内置 lane guard、引用要求和禁止复制清单。

## 02 `.github/`

1. **用途**

- `.github` 是贡献治理、CI、容器发布、桌面包发布和安全响应入口。
- 它包含 PR 质量检查、Docker multi-arch、nightly/alpha 发布、Electron build、跨平台 release、Gitee 同步和安全策略；证据见 `Calcium-Ion/new-api@d146e45e2f95:.github/workflows/pr-check.yml:1`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:1`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:1`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/release.yml:1`、`Calcium-Ion/new-api@d146e45e2f95:.github/SECURITY.md:26`。

2. **关键文件**

- `.github/workflows/release.yml:180 LoC`：Linux/macOS/Windows release。
- `.github/workflows/docker-build.yml:141 LoC`：tag/手工触发的 multi-arch 镜像发布。
- `.github/workflows/docker-image-alpha.yml:179 LoC`：alpha 镜像发布，含 GHCR/Docker Hub。
- `.github/workflows/electron-build.yml:140 LoC`：桌面应用构建。
- `.github/workflows/pr-check.yml:33 LoC`：PR 模板和 AI 垃圾提交检查。
- `.github/SECURITY.md:95 LoC`：漏洞报告和部署安全建议。

3. **入口 / 调用关系**

- tag、workflow dispatch 或 PR 事件触发 workflow；发布链会构建前端、Go 二进制、容器镜像和桌面 artifact。
- Docker 发布链使用 Buildx、Docker Hub 登录、metadata、build-push、签名和 manifest 合成；证据见 `Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:61`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:64`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:70`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:76`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:92`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/docker-build.yml:122`。

4. **核心 logic / 算法**

- CI/CD logic 是“版本输入 -> 构建矩阵 -> artifact/signature -> manifest/release summary”。
- 安全策略强调私密漏洞报告、批量报告协调、最小开放端口和独立数据库/Redis；证据见 `Calcium-Ion/new-api@d146e45e2f95:.github/SECURITY.md:30`、`Calcium-Ion/new-api@d146e45e2f95:.github/SECURITY.md:65`、`Calcium-Ion/new-api@d146e45e2f95:.github/SECURITY.md:83`、`Calcium-Ion/new-api@d146e45e2f95:.github/SECURITY.md:88`。

5. **暴露功能**

- 用户/部署者获得容器镜像、release 二进制和桌面包。
- 贡献者看到 PR 质量门禁、issue 模板和安全报告入口。

6. **HUAKAI 升级点**

- 生态升级：HUAKAI 发布链应输出 SBOM、签名、镜像 provenance、数据库迁移兼容报告和 gateway smoke test。
- 安全升级：漏洞报告流程接入内部 risk register，按多租户/计费/认证/额度路径自动标 severity。
- 架构升级：release matrix 需要区分 Community/Enterprise edition，避免功能开关在打包时不可追溯。

## 03 `bin/`

1. **用途**

- `bin` 放历史迁移脚本和简单性能探测脚本。
- 迁移脚本观察到用户额度和通道能力关联数据的历史修补；探测脚本向聊天端点发请求并统计时延；证据见 `Calcium-Ion/new-api@d146e45e2f95:bin/migration_v0.2-v0.3.sql:1`、`Calcium-Ion/new-api@d146e45e2f95:bin/migration_v0.3-v0.4.sql:1`、`Calcium-Ion/new-api@d146e45e2f95:bin/time_test.sh:17`、`Calcium-Ion/new-api@d146e45e2f95:bin/time_test.sh:40`。

2. **关键文件**

- `bin/time_test.sh:40 LoC`：命令行压测样例，面向 `/v1/chat/completions`。
- `bin/migration_v0.2-v0.3.sql:6 LoC`：历史额度数据修补。
- `bin/migration_v0.3-v0.4.sql:17 LoC`：历史通道能力补全。

3. **入口 / 调用关系**

- 这些脚本不由 runtime 自动调用；属于手工运行或历史迁移辅助。
- 性能脚本要求 domain、key、count 和可选 model，直接调用兼容聊天端点；证据见 `Calcium-Ion/new-api@d146e45e2f95:bin/time_test.sh:3`、`Calcium-Ion/new-api@d146e45e2f95:bin/time_test.sh:18`。

4. **核心 logic / 算法**

- 历史迁移 logic 是把旧数据补齐到新能力模型：用户剩余额度聚合、通道与模型能力关系补种。
- 性能脚本 logic 是串行请求、累计耗时、计算平均值和标准差。

5. **暴露功能**

- operator 可以手工做兼容迁移和基础 latency 验证。
- 没看到正式迁移框架、回滚策略或 migration registry。

6. **HUAKAI 升级点**

- 架构升级：所有迁移进入版本化 migration runner，不允许孤立 SQL 脚本成为生产步骤。
- 生态升级：把 latency 探测升级为多模型、多通道、P50/P95/P99、错误率、首 token 时间和区域维度。
- 安全升级：压测脚本不应要求明文 key 出现在 shell 历史；用短期 token 或 env 文件加载。

## 04 `common/`

1. **用途**

- `common` 是跨层工具箱：环境、JSON、Redis、请求体复用、磁盘缓存、SSRF 保护、系统监控、限流辅助、加密、邮件、嵌入文件系统、音频时长和通用响应。
- 请求体存储和磁盘缓存支撑 relay retry 与大请求处理；证据见 `Calcium-Ion/new-api@d146e45e2f95:common/body_storage.go:14`、`Calcium-Ion/new-api@d146e45e2f95:common/body_storage.go:241`、`Calcium-Ion/new-api@d146e45e2f95:common/disk_cache.go:13`、`Calcium-Ion/new-api@d146e45e2f95:common/disk_cache_config.go:9`。

2. **关键文件**

- `common/gin.go:366 LoC`：HTTP body 复用、响应辅助和 Gin 相关工具。
- `common/ssrf_protection.go:355 LoC`：远程文件/URL 安全检查。
- `common/audio.go:347 LoC`：音频时长估算。
- `common/redis.go:327 LoC`：Redis client 和缓存辅助。
- `common/body_storage.go:315 LoC`：大请求 body 的内存/磁盘存储。
- `common/disk_cache.go:176 LoC`、`common/disk_cache_config.go:177 LoC`：磁盘缓存文件与统计。

3. **入口 / 调用关系**

- `main.go` 初始化环境、Redis、磁盘缓存清理和系统监控时调用该目录能力；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:269`、`Calcium-Ion/new-api@d146e45e2f95:main.go:293`、`Calcium-Ion/new-api@d146e45e2f95:main.go:305`、`Calcium-Ion/new-api@d146e45e2f95:main.go:313`。
- relay retry 会从上下文拿可复用 body，再把请求体重置给后续上游请求；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:199`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:210`。

4. **核心 logic / 算法**

- 请求体存储按大小和配置选择内存或磁盘，并暴露统一 reader/seek/close 语义，服务 retry 和 multipart/大文件场景。
- Redis 与内存缓存组合被多个模块拿来做限流、通道缓存、订阅 plan cache、channel affinity cache 和指标热桶。

5. **暴露功能**

- 用户间接感知为：大请求可重试、远程文件受保护、系统过载可拒绝、Redis 开启后行为更稳定。
- operator 间接感知为：可清理磁盘缓存、查看缓存统计、配置性能阈值。

6. **HUAKAI 升级点**

- 架构升级：将 common 拆成 `request-store`、`cache-runtime`、`network-guard`、`runtime-health` 四类明确模块，减少 shared bag 风险。
- 安全升级：远程文件获取需要统一 SSRF policy、allowlist/denylist、DNS rebinding 防护和审计日志。
- 生态升级：把 body/disk cache 指标暴露给 Admin Ops，含命中率、泄漏清理、磁盘上限和租户归属。

## 05 `constant/`

1. **用途**

- `constant` 集中保存 API 类型、通道类型、上下文 key、端点类型、任务类型、结束原因和支付方法等静态枚举。
- relay 适配器选择依赖 API 类型与通道类型映射；证据见 `Calcium-Ion/new-api@d146e45e2f95:constant/api_type.go:3`、`Calcium-Ion/new-api@d146e45e2f95:constant/channel.go:3`、`Calcium-Ion/new-api@d146e45e2f95:constant/context_key.go:3`、`Calcium-Ion/new-api@d146e45e2f95:constant/endpoint_type.go:3`。

2. **关键文件**

- `constant/channel.go:209 LoC`：通道/provider 类型常量。
- `constant/context_key.go:69 LoC`：跨 middleware/controller/relay 的上下文 key。
- `constant/api_type.go:40 LoC`：上游协议族类型。
- `constant/midjourney.go:48 LoC`：异步绘图动作映射。
- `constant/task.go:24 LoC`：任务平台/状态相关常量。
- `constant/README.md:25 LoC`：常量目录说明。

3. **入口 / 调用关系**

- `router`、`middleware`、`controller`、`relay`、`service` 都引用常量目录作为协议/通道/上下文边界。
- relay adapter registry 根据 API 类型选择具体 provider 适配器；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/relay_adaptor.go:53`、`Calcium-Ion/new-api@d146e45e2f95:relay/relay_adaptor.go:121`。

4. **核心 logic / 算法**

- 常量目录本身无复杂算法，但它定义了“请求上下文协议”：token、group、通道、模型、重试、stream、header override 等运行时数据被放入 context 后跨层传递。

5. **暴露功能**

- 用户不直接看到该目录，但它决定可选 provider、端点族、任务类型、支付方法和 dashboard 能力开关。

6. **HUAKAI 升级点**

- 架构升级：把静态 provider 枚举升级成 plugin capability registry，避免每加 provider 都改核心枚举。
- 算法升级：上下文 key 应收敛为 typed request envelope，降低字符串 key 跨层漂移风险。
- 生态升级：常量变化必须自动生成 docs、admin schema、OpenAPI 和测试 fixtures。

## 06 `controller/`

1. **用途**

- `controller` 是 HTTP handler 层，覆盖用户、令牌、通道、relay、日志、计费、订阅、充值、OAuth、2FA/passkey、模型同步、ratio 同步、部署管理、任务和性能接口。
- 最大文件是通道管理、用户管理、ratio 同步、通道上游更新、通道测试、部署管理和 relay 编排；line-count 观察见 `controller/channel.go:1954`、`controller/user.go:1268`、`controller/ratio_sync.go:1029`、`controller/channel_upstream_update.go:999`、`controller/channel-test.go:984`、`controller/deployment.go:810`、`controller/relay.go:653`。

2. **关键文件**

- `controller/relay.go:653 LoC`：主 relay 生命周期：请求校验、计费、通道选择、重试、错误日志、任务提交。
- `controller/channel.go:1954 LoC`：通道 CRUD、模型抓取、余额检测、标签、多密钥、本地模型操作和某类 OAuth credential 刷新。
- `controller/user.go:1268 LoC`：登录、注册、用户管理、自助设置、充值入口和权限视图。
- `controller/token.go:359 LoC`：用户 API key 管理和 usage 查询。
- `controller/subscription.go:383 LoC`、`controller/topup.go:503 LoC`：订阅计划、订单和充值回调。
- `controller/deployment.go:810 LoC`：外部 GPU/容器部署管理 API。

3. **入口 / 调用关系**

- `router/api-router.go` 把 setup/status、pricing、OAuth、用户、订阅、option、性能、ratio sync、channel、token、usage、log、vendor、models、deployments 等接口映射到 controller；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:48`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:181`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:251`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:294`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:364`。
- relay controller 从 helper 解析请求，生成 relay info，做敏感词检查、token 估算、价格计算、预扣、重试和错误处理；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:109`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:120`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:126`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:145`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:153`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:164`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:190`。

4. **核心 logic / 算法**

- relay 生命周期采用“预估 -> 预扣 -> 通道选择 -> 上游调用 -> 失败处理 -> 可重试则换通道 -> 成功结算/失败退款”的 request hop 模型。
- 通道错误会根据状态码/错误类型触发禁用、错误日志和 retry 决策；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:231`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:324`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:356`。
- ratio sync 能从上游拉取价格/倍率并构造 diff，支持 OpenRouter 和 models.dev 数据形态；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/ratio_sync.go:142`、`Calcium-Ion/new-api@d146e45e2f95:controller/ratio_sync.go:536`、`Calcium-Ion/new-api@d146e45e2f95:controller/ratio_sync.go:724`、`Calcium-Ion/new-api@d146e45e2f95:controller/ratio_sync.go:906`。

5. **暴露功能**

- 管理员能管理通道、模型、vendor、日志、订阅、充值、部署、系统配置和性能。
- 用户能注册登录、管理 token、查看日志/用量、购买订阅/充值、使用 playground 和 relay API。

6. **HUAKAI 升级点**

- 架构升级：controller 过宽，HUAKAI 应拆成 handler + application service + domain policy，避免 money path 和 HTTP 细节混在一个文件。
- 算法升级：retry 决策应沉淀成可配置 policy engine，输入包括通道健康、错误分类、租户 SLA、预算和上游限流。
- 生态升级：Admin Ops 需要展示每次 request hop 的预扣、通道选择、错误分类、退款和最终账本。

## 07 `docs/`

1. **用途**

- `docs` 存 OpenAPI JSON、安装说明、通道额外设置说明、外部部署 API 笔记、翻译 glossary 和图片资产。
- 管理 API 和 relay API 均有 OpenAPI JSON；证据见 `Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:2`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:4`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/relay.json:2`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/relay.json:73`。

2. **关键文件**

- `docs/openapi/api.json:7817 LoC`：后台管理接口文档。
- `docs/openapi/relay.json:7294 LoC`：relay 协议接口文档。
- `docs/installation/BT.md:151 LoC`：安装路径说明。
- `docs/channel/other_setting.md:33 LoC`：通道附加行为配置说明。
- `docs/ionet-client.md:3 LoC`：外部部署 API 片段。
- `docs/translation-glossary*.md:86-107 LoC`：翻译术语表。

3. **入口 / 调用关系**

- docs 不被 runtime 自动调用，但 OpenAPI JSON 能被前端、SDK、测试或文档站消费。
- 通道附加设置说明覆盖格式化、代理和思考内容转换等 operator 可调项；证据见 `Calcium-Ion/new-api@d146e45e2f95:docs/channel/other_setting.md:5`、`Calcium-Ion/new-api@d146e45e2f95:docs/channel/other_setting.md:9`、`Calcium-Ion/new-api@d146e45e2f95:docs/channel/other_setting.md:13`。

4. **核心 logic / 算法**

- docs 目录的核心是 contract surface：把后台接口分为系统、用户、OAuth、充值、安全验证、通道、令牌、日志、数据统计、供应商、模型和系统设置等标签；证据见 `Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:31`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:34`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:55`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:61`。

5. **暴露功能**

- operator/开发者能看到 API contract、通道附加设置和部署说明。
- OpenAPI 也暴露了管理后台能力面，对权限建模和 SDK 生成很关键。

6. **HUAKAI 升级点**

- 架构升级：OpenAPI 应从代码/route schema 自动生成并带权限、租户、审计和 edition 标记。
- 生态升级：为每个 Admin Ops 页面生成“UI action -> API -> audit event -> scenario test”四联表。
- 安全升级：通道附加设置文档必须带风险标签，例如 header 透传、代理、内容改写、隐藏字段过滤。

## 08 `dto/`

1. **用途**

- `dto` 定义跨协议请求/响应 shape：OpenAI chat/responses/image/audio、Claude、Gemini、embedding、rerank、task、pricing、playground、channel settings、用户设置等。
- DTO 提供 token-count meta、stream 判断、模型名重写、媒体/file source 抽取和错误转换；证据见 `Calcium-Ion/new-api@d146e45e2f95:dto/openai_request.go:29`、`Calcium-Ion/new-api@d146e45e2f95:dto/openai_request.go:111`、`Calcium-Ion/new-api@d146e45e2f95:dto/claude.go:206`、`Calcium-Ion/new-api@d146e45e2f95:dto/claude.go:244`、`Calcium-Ion/new-api@d146e45e2f95:dto/gemini.go:14`、`Calcium-Ion/new-api@d146e45e2f95:dto/gemini.go:68`。

2. **关键文件**

- `dto/openai_request.go:1059 LoC`：OpenAI-style 请求、媒体内容、responses input 解析。
- `dto/claude.go:600 LoC`：Claude-style 请求、工具、thinking、usage 和响应事件。
- `dto/gemini.go:582 LoC`：Gemini-style 请求、generation config、inline media、usage。
- `dto/openai_response.go:446 LoC`：OpenAI-style 响应、stream chunk、usage、responses output。
- `dto/openai_image.go:184 LoC`：图像请求和价格 token meta。
- `dto/channel_settings.go:46 LoC`：通道级附加设置。

3. **入口 / 调用关系**

- `relay/helper` 根据 relay format 读取和验证 DTO；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/helper/valid_request.go:19`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/valid_request.go:23`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/valid_request.go:33`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/valid_request.go:35`。
- `service/convert` 在协议之间转换 DTO；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/convert.go:17`、`Calcium-Ion/new-api@d146e45e2f95:service/convert.go:607`、`Calcium-Ion/new-api@d146e45e2f95:service/convert.go:658`、`Calcium-Ion/new-api@d146e45e2f95:service/convert.go:828`。

4. **核心 logic / 算法**

- DTO 层把“协议输入”转成统一 token-count meta：文本、媒体、最大输出、图片/音频/file/video source 被收集给估算与计费。
- DTO 层还负责宽松解析：例如 responses input 可以是字符串、数组或对象，Gemini generation config 保留显式零值。

5. **暴露功能**

- 用户可用多个协议形态访问同一 gateway：OpenAI chat/responses、Claude messages、Gemini native、image/audio/embedding/rerank/task。
- operator 可通过 channel settings 控制上游特性、key 类型、企业模式等 provider 行为。

6. **HUAKAI 升级点**

- 架构升级：DTO 应从 public protocol schema 与 internal canonical envelope 分离，避免跨协议字段互相污染。
- 算法升级：token-count meta 需要带 provenance，区分 observed/estimated/provider-reported。
- 生态升级：为每个 DTO 形态生成 fuzz/roundtrip/zero-value acceptance tests。

## 09 `electron/`

1. **用途**

- `electron` 是桌面壳，把本地 Go 二进制作为子进程启动，并通过窗口和系统托盘呈现服务。
- README 明确该目录提供 Windows/macOS/Linux 的原生桌面包装和 tray 支持；证据见 `Calcium-Ion/new-api@d146e45e2f95:electron/README.md:3`、`Calcium-Ion/new-api@d146e45e2f95:electron/README.md:35`。

2. **关键文件**

- `electron/main.js:589 LoC`：进程启动、端口探测、窗口、tray、日志和生命周期。
- `electron/package.json:100 LoC`：Electron build 脚本和打包配置。
- `electron/preload.js:17 LoC`：窗口 preload 边界。
- `electron/build.sh:40 LoC`：构建辅助。
- `electron/create-tray-icon.js:59 LoC`：tray icon 生成。
- `electron/icon.png` 与 tray PNG：桌面图标资产。

3. **入口 / 调用关系**

- CI 的桌面 workflow 会构建前端、Go Windows 二进制，再进入 Electron 目录安装依赖并打包；证据见 `Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:44`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:60`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:88`、`Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:101`。
- desktop runtime 创建窗口、tray 菜单和本地地址连接；证据见 `Calcium-Ion/new-api@d146e45e2f95:electron/main.js:388`、`Calcium-Ion/new-api@d146e45e2f95:electron/main.js:428`、`Calcium-Ion/new-api@d146e45e2f95:electron/main.js:461`、`Calcium-Ion/new-api@d146e45e2f95:electron/main.js:480`。

4. **核心 logic / 算法**

- 桌面壳 logic 是“启动/监控后端进程 -> 等待本地端口可用 -> 打开窗口 -> 关闭时驻留 tray -> 明确退出才终止”。
- 该路径让单机 operator 能本地运行 gateway，但也引入本地端口、日志、升级和 key 存储风险。

5. **暴露功能**

- 用户可以像桌面 App 一样启动/关闭/隐藏 gateway。
- Windows artifact 是当前 workflow 明确上传的桌面产物；证据见 `Calcium-Ion/new-api@d146e45e2f95:.github/workflows/electron-build.yml:116`。

6. **HUAKAI 升级点**

- 生态升级：HUAKAI 如果做桌面版，应把本地 keyring、自动更新、日志导出和诊断包作为一等能力。
- 安全升级：本地端口必须默认 bind loopback，并有 CSRF/token guard。
- 架构升级：桌面壳不要直接携带生产管理权限；采用 local admin session + short-lived operator token。

## 10 `i18n/`

1. **用途**

- `i18n` 提供后端消息 key、语言检测、locale 文件加载和用户语言懒加载。
- 启动时初始化语言包，并把用户语言 loader 注册到 i18n 层；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:315`、`Calcium-Ion/new-api@d146e45e2f95:main.go:324`。

2. **关键文件**

- `i18n/i18n.go:231 LoC`：语言初始化、翻译、支持语言和用户语言 loader。
- `i18n/keys.go:331 LoC`：后端消息 key。
- `i18n/locales/en.yaml:278 LoC`：英文 locale。
- `i18n/locales/zh-CN.yaml:279 LoC`：简体中文 locale。
- `i18n/locales/zh-TW.yaml:279 LoC`：繁体中文 locale。

3. **入口 / 调用关系**

- `middleware/i18n.go` 从请求中检测语言并设置上下文；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/i18n.go:13`、`Calcium-Ion/new-api@d146e45e2f95:middleware/i18n.go:23`。
- auth/rate-limit/controller error path 会通过 i18n key 输出用户可读消息；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:47`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:63`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:36`。

4. **核心 logic / 算法**

- 后端 i18n logic 是“请求语言 -> 上下文 -> key 翻译 -> fallback”；用户语言可从持久化设置懒加载。

5. **暴露功能**

- API 错误、登录/权限/限流提示可本地化。
- operator 可让不同用户看到本地化错误，而非固定中文或英文。

6. **HUAKAI 升级点**

- 生态升级：后端错误码、OpenAPI error schema、前端 locale 和 runbook 文案统一出自一个 message catalog。
- 安全升级：错误文案按 public/internal 分级，避免把 provider key、通道名或账本细节泄漏给普通用户。
- 架构升级：所有 policy 拒绝必须输出稳定 machine code + localized user message。

## 11 `logger/`

1. **用途**

- `logger` 封装运行日志、文件日志、debug gating 和额度展示格式。
- 启动时 setup logger；超过阈值会进入文件记录路径；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:271`、`Calcium-Ion/new-api@d146e45e2f95:logger/logger.go:42`、`Calcium-Ion/new-api@d146e45e2f95:logger/logger.go:97`。

2. **关键文件**

- `logger/logger.go:181 LoC`：日志级别、文件日志、quota 格式化、JSON 日志。

3. **入口 / 调用关系**

- controller、middleware、service、relay 在错误、通道选择、请求处理和后台任务中调用 logger。
- logger 依赖运营设置来决定额度显示币种/单位；证据见 `Calcium-Ion/new-api@d146e45e2f95:logger/logger.go:120`、`Calcium-Ion/new-api@d146e45e2f95:logger/logger.go:147`。

4. **核心 logic / 算法**

- 日志 helper 按 level 输出，debug 日志受开关控制，quota 格式化按显示类型转换。
- 日志文件滚动是简单计数触发，不是完整日志管道。

5. **暴露功能**

- operator 能看到启动、通道错误、请求错误、额度展示和性能日志。
- 用户间接看到额度单位格式化结果。

6. **HUAKAI 升级点**

- 生态升级：HUAKAI 需要结构化日志、trace id、tenant/account/channel/user 维度、redaction 和 log sampling。
- 安全升级：所有 provider error 进入 redaction pipeline，再决定 user-visible/admin-visible。
- 架构升级：日志与审计分离，计费/鉴权/额度事件必须进入 append-only audit trail。

## 12 `middleware/`

1. **用途**

- `middleware` 负责 session/access-token/API-key 鉴权、CORS、gzip、body cleanup、route tag、日志、request id、全局/用户/模型限流、系统过载保护、安全验证、turnstile、请求适配和通道分发。
- token 鉴权会兼容多种协议 key 传入方式，随后设置用户、token、group、quota、model limit 等上下文；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:276`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:294`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:300`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:332`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:367`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:409`。

2. **关键文件**

- `middleware/auth.go:439 LoC`：用户 session、access token、API key 和 context setup。
- `middleware/distributor.go:435 LoC`：模型请求解析、group 选择、通道选择和通道 context setup。
- `middleware/rate-limit.go:205 LoC`：IP/用户维度限流。
- `middleware/model-rate-limit.go:200 LoC`：模型请求成功/总请求限流。
- `middleware/secure_verification.go:133 LoC`：敏感操作二次验证。
- `middleware/performance.go:71 LoC`：系统过载拒绝。

3. **入口 / 调用关系**

- API 路由挂全局 API rate limit、body cleanup 和 gzip；relay 路由挂 CORS、解压、body cleanup、stats、token auth、模型限流和分发；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:50`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:52`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:14`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:17`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:72`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:85`。
- 分发中间件调用模型请求解析、token model limit、group access、channel selection 和 selected channel context；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:30`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:57`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:82`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:345`。

4. **核心 logic / 算法**

- 分发算法先识别请求模型和可选 group，再根据 token 限制、用户可用 group、auto group 和 retry 参数挑选通道。
- 限流算法有 Redis list/window、内存 limiter、token bucket 和成功请求计数；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/rate-limit.go:21`、`Calcium-Ion/new-api@d146e45e2f95:middleware/rate-limit.go:67`、`Calcium-Ion/new-api@d146e45e2f95:middleware/model-rate-limit.go:25`、`Calcium-Ion/new-api@d146e45e2f95:middleware/model-rate-limit.go:78`、`Calcium-Ion/new-api@d146e45e2f95:middleware/model-rate-limit.go:167`。
- 系统过载保护按 CPU/内存/磁盘阈值返回协议化错误；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/performance.go:14`、`Calcium-Ion/new-api@d146e45e2f95:middleware/performance.go:41`。

5. **暴露功能**

- 用户看到鉴权、模型权限、分组权限、限流、系统过载、敏感操作验证和协议兼容 key 输入。
- operator 看到通道分发、group policy、模型级限流和二次验证能力。

6. **HUAKAI 升级点**

- 架构升级：鉴权、额度、分发、限流分成独立 gate chain，每个 gate 产出结构化 verdict。
- 算法升级：channel selection 应使用健康分、延迟、成本、quota、租户 SLA、错误冷却、账号亲和性和缓存命中信号。
- 安全升级：API key 解析路径要有统一 credential parser，避免各协议 header/query 特例分散。

## 13 `model/`

1. **用途**

- `model` 是 GORM 持久化层，覆盖数据库初始化/迁移、用户、token、channel、ability、log、pricing、subscription、topup、task、passkey、2FA、vendor/model metadata、性能指标等。
- 数据库初始化支持多种 SQL 后端和独立日志库；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/main.go:118`、`Calcium-Ion/new-api@d146e45e2f95:model/main.go:177`、`Calcium-Ion/new-api@d146e45e2f95:model/main.go:213`、`Calcium-Ion/new-api@d146e45e2f95:model/main.go:250`、`Calcium-Ion/new-api@d146e45e2f95:model/main.go:370`。

2. **关键文件**

- `model/main.go:706 LoC`：DB 选择、初始化、迁移、关闭和连接检测。
- `model/channel.go:1060 LoC`：通道对象、key 轮转、状态更新、多密钥、标签、能力和查询。
- `model/user.go:1056 LoC`：用户、quota、登录绑定、缓存和额度更新。
- `model/subscription.go:1206 LoC`：订阅计划、订单、用户订阅、预扣、重置和失效。
- `model/log.go:533 LoC`：消费、错误、充值、任务和查询统计日志。
- `model/token.go:511 LoC`：API key、剩余额度、模型限制、缓存失效。

3. **入口 / 调用关系**

- main 初始化 DB、检查 setup、加载 option map、初始化 log DB，并在退出时关闭 DB；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:281`、`Calcium-Ion/new-api@d146e45e2f95:main.go:287`、`Calcium-Ion/new-api@d146e45e2f95:main.go:290`、`Calcium-Ion/new-api@d146e45e2f95:main.go:299`、`Calcium-Ion/new-api@d146e45e2f95:main.go:68`。
- middleware 读取 token/user/channel，controller 管理 CRUD，service 做 quota/订阅/日志变更。

4. **核心 logic / 算法**

- 通道层包含 key 列表解析、下一把可用 key、状态更新、多 key 禁用理由和 tag 批量操作；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/channel.go:142`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:166`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:625`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:663`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:752`。
- ability 层按 group/model/retry 查可用通道，构成分发基础；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/ability.go:61`、`Calcium-Ion/new-api@d146e45e2f95:model/ability.go:91`、`Calcium-Ion/new-api@d146e45e2f95:model/ability.go:106`。
- subscription 层有计划缓存、订单完成、预扣/退款、到期处理和周期重置；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:85`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:511`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:970`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:1074`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:1100`。

5. **暴露功能**

- 用户/管理员看到的账号、token、通道、模型能力、日志、订阅、充值、性能指标和部署记录都落在该层。

6. **HUAKAI 升级点**

- 架构升级：把 model 从 active record 风格升级为 repository + transaction script + domain event，尤其是账本和 quota。
- 安全升级：key、OAuth refresh token、支付订单和订阅预扣必须加密/脱敏/审计，不能只靠普通 ORM 字段。
- 算法升级：通道 key 轮转、禁用和恢复要建模为 account health state machine。

## 14 `oauth/`

1. **用途**

- `oauth` 提供内置和自定义 OAuth provider 的统一接口、注册表、授权码交换、用户信息读取、访问策略解析和用户绑定。
- 自定义 provider 支持从数据库加载，启动时会加载；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:327`、`Calcium-Ion/new-api@d146e45e2f95:oauth/registry.go:1`、`Calcium-Ion/new-api@d146e45e2f95:oauth/provider.go:11`、`Calcium-Ion/new-api@d146e45e2f95:oauth/generic.go:74`。

2. **关键文件**

- `oauth/generic.go:673 LoC`：自定义 OAuth、token 交换、用户信息、访问策略。
- `oauth/github.go:178 LoC`、`oauth/discord.go:172 LoC`、`oauth/oidc.go:177 LoC`、`oauth/linuxdo.go:195 LoC`：内置 provider。
- `oauth/registry.go:134 LoC`：provider registry。
- `oauth/types.go:68 LoC`、`oauth/provider.go:36 LoC`：公共接口和类型。

3. **入口 / 调用关系**

- API 路由暴露统一 OAuth provider 路由和自定义 provider 管理接口；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:78`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:87`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:224`。
- controller 自定义 provider 管理会调用 model 层持久化，再由 oauth registry 加载。

4. **核心 logic / 算法**

- generic OAuth logic 是：构造授权流、交换 token、读取用户信息、按访问策略检查响应体、映射用户身份、绑定或创建用户。
- 访问策略支持条件列表和比较操作，用于限制哪些外部账号可进入系统；证据见 `Calcium-Ion/new-api@d146e45e2f95:oauth/generic.go:333`、`Calcium-Ion/new-api@d146e45e2f95:oauth/generic.go:344`、`Calcium-Ion/new-api@d146e45e2f95:oauth/generic.go:401`、`Calcium-Ion/new-api@d146e45e2f95:oauth/generic.go:454`。

5. **暴露功能**

- 用户可通过多个身份源登录或绑定；管理员/root 可配置自定义 OAuth provider 和访问规则。

6. **HUAKAI 升级点**

- 安全升级：OAuth provider 必须有 issuer pinning、PKCE、state nonce、redirect allowlist、JWK rotation 和 login audit。
- 架构升级：自定义 provider 作为 enterprise plugin，policy DSL 与 auth core 隔离。
- 生态升级：Admin Ops 展示 provider 健康、失败原因、最近登录和绑定冲突。

## 15 `pkg/`

1. **用途**

- `pkg` 放较独立的内部库：表达式计费、混合缓存、性能指标聚合和外部部署 API client。
- 这些包被 service/controller/model 使用，但比 `common` 更像可复用子系统。

2. **关键文件**

- `pkg/billingexpr/compile.go:175 LoC`、`run.go:140 LoC`、`settle.go:35 LoC`、`types.go:66 LoC`：分层/表达式计费引擎。
- `pkg/cachex/hybrid_cache.go:285 LoC`：Redis + memory 混合缓存。
- `pkg/perf_metrics/metrics.go:358 LoC`、`flush.go:98 LoC`、`types.go:152 LoC`：relay 性能样本聚合、查询和 flush。
- `pkg/ionet/client.go:219 LoC`、`deployment.go:377 LoC`、`hardware.go:202 LoC`、`container.go:302 LoC`、`types.go:353 LoC`：外部 GPU/容器部署 API client。

3. **入口 / 调用关系**

- price helper 调用表达式计费包做预扣估算和 snapshot；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:67`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:241`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:257`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:278`。
- relay 失败/成功后会记录性能样本，main 初始化指标包；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:310`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:244`、`Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/metrics.go:23`、`Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/metrics.go:27`。
- deployment controller 调用外部部署 client 做连接测试、列表、创建、容器和日志；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/deployment.go:42`、`Calcium-Ion/new-api@d146e45e2f95:controller/deployment.go:206`、`Calcium-Ion/new-api@d146e45e2f95:controller/deployment.go:494`、`Calcium-Ion/new-api@d146e45e2f95:controller/deployment.go:646`。

4. **核心 logic / 算法**

- 表达式计费包把表达式编译缓存、运行时 token/request/header 探针、分层 trace、四舍五入和结算一致性组合起来；证据见 `Calcium-Ion/new-api@d146e45e2f95:pkg/billingexpr/compile.go:77`、`Calcium-Ion/new-api@d146e45e2f95:pkg/billingexpr/run.go:24`、`Calcium-Ion/new-api@d146e45e2f95:pkg/billingexpr/run.go:66`、`Calcium-Ion/new-api@d146e45e2f95:pkg/billingexpr/settle.go:15`、`Calcium-Ion/new-api@d146e45e2f95:pkg/billingexpr/round.go:8`。
- 性能指标包把 relay 样本聚合到 bucket，查询时合并内存热桶、Redis 活跃桶和 DB 历史桶；证据见 `Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/metrics.go:57`、`Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/metrics.go:79`、`Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/metrics.go:125`、`Calcium-Ion/new-api@d146e45e2f95:pkg/perf_metrics/flush.go:26`。

5. **暴露功能**

- operator 可做复杂模型定价、查看性能指标、创建/管理外部部署。
- 用户间接受益于更准确的预扣/结算、性能排序和 provider 部署入口。

6. **HUAKAI 升级点**

- 架构升级：表达式计费必须被隔离到安全 sandbox，带版本、review、dry-run、rollback 和 audit。
- 算法升级：性能指标引入 error budget、SLO burn rate、成本/延迟/成功率联合评分。
- 生态升级：外部部署 client 插件化，支持多云 GPU vendor，统一 quota、成本和生命周期审计。

## 16 `relay/`

1. **用途**

- `relay` 是 AI API proxy 核心：协议入口 helper、provider adapter、request/response 转换、stream 处理、任务提交/查询、参数/header override、模型映射、计费 helper 和 websocket。
- adapter registry 覆盖多种 provider 和任务平台；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/relay_adaptor.go:53`、`Calcium-Ion/new-api@d146e45e2f95:relay/relay_adaptor.go:135`。

2. **关键文件**

- `relay/relay_adaptor.go:165 LoC`：provider/task adapter registry。
- `relay/common/relay_info.go:896 LoC`：请求上下文汇总、通道 meta、计费 session 指针、协议转换链。
- `relay/common/override.go:2058 LoC`：参数和 header 改写引擎。
- `relay/channel/api_request.go:554 LoC`：上游 HTTP/WebSocket 请求、header override、ping。
- `relay/compatible_handler.go:217 LoC`、`responses_handler.go:151 LoC`、`gemini_handler.go:293 LoC`：主协议 helper。
- `relay/relay_task.go:564 LoC`、`mjproxy_handler.go:679 LoC`：异步任务和 MJ-style proxy。

3. **入口 / 调用关系**

- `controller/relay.go` 根据 relay format 调用 text、image、audio、rerank、embedding、responses、Gemini、Claude、realtime 或 task helper；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:35`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:58`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:212`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:485`。
- `relay/common/relay_info.go` 从请求和上下文生成统一 relay info，覆盖 OpenAI、Claude、Gemini、embedding、responses、task 等格式；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:333`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:343`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:377`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:529`。

4. **核心 logic / 算法**

- relay info 汇总用户、token、group、stream、模型、通道 meta、price data、计费 snapshot、request id、retry index 和协议转换链；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:87`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:124`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:143`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:156`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:158`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/relay_info.go:598`。
- 参数/header override 支持条件、JSON path、header passthrough、runtime header sync 和审计；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/common/override.go:131`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/override.go:172`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/override.go:501`、`Calcium-Ion/new-api@d146e45e2f95:relay/common/override.go:1478`。
- provider request path 会装配 header、执行 HTTP/WebSocket 请求、支持 header passthrough regex 和 keepalive ping；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:28`、`Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:173`、`Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:290`、`Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:354`、`Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:384`。

5. **暴露功能**

- 用户看到多协议、多 provider、stream、realtime、image/audio/embedding/rerank/responses/task/MJ-style API 的统一 gateway。
- operator 看到 provider adapter、通道 override、模型映射、重试、计费和异步任务状态。

6. **HUAKAI 升级点**

- 架构升级：relay core 应分为 protocol parser、canonical request、provider adapter、response normalizer、billing hook、policy hook。
- 算法升级：override engine 需要安全类型系统、schema validation、dry-run preview、blast-radius estimate 和 per-tenant allowlist。
- 生态升级：每个 provider adapter 产出 capability manifest，包括支持端点、stream、usage 质量、错误映射、retry 安全性。

## 17 `router/`

1. **用途**

- `router` 是 HTTP route 装配层：API、relay、dashboard legacy、video、web fallback 和主题静态资源。
- 主路由把 API/dashboard/relay/video/web 组合成一个 Gin engine；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/main.go:15`、`Calcium-Ion/new-api@d146e45e2f95:router/main.go:16`、`Calcium-Ion/new-api@d146e45e2f95:router/main.go:18`、`Calcium-Ion/new-api@d146e45e2f95:router/main.go:26`。

2. **关键文件**

- `router/api-router.go:390 LoC`：后台 API route tree。
- `router/relay-router.go:224 LoC`：OpenAI/Claude/Gemini/MJ/Suno/task relay route tree。
- `router/web-router.go:46 LoC`：前端静态资源和 fallback。
- `router/dashboard.go:23 LoC`：legacy dashboard billing route。
- `router/video-router.go:52 LoC`：视频 proxy route。
- `router/main.go:34 LoC`：route set 汇总。

3. **入口 / 调用关系**

- API route 装配 setup/status/pricing/perf、用户、订阅、系统 option、性能、ratio sync、channel、token、usage、log、data、vendor、models、deployments；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:55`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:98`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:181`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:251`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:294`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:364`。
- relay route 装配 `/v1`、`/v1beta`、`/pg`、MJ 和 Suno/task-style 路径；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:13`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:69`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:88`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:168`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:179`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:189`。

4. **核心 logic / 算法**

- Router 主要是访问控制和协议路径分层：API route 按用户/admin/root/token 分组，relay route 按协议格式进入统一 controller。
- Web fallback 会排除 `/v1`、`/api` 和 `/assets`，其余路径返回当前主题 index；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:24`、`Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:33`、`Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:40`。

5. **暴露功能**

- API consumer 得到 OpenAI/Claude/Gemini-compatible endpoints。
- Dashboard 用户得到 SPA route fallback 和后台管理 API。
- 管理员得到 deployment/model/channel/token/log/subscription 等管理入口。

6. **HUAKAI 升级点**

- 架构升级：route tree 应导出机器可读权限矩阵，关联 OpenAPI、前端导航和审计事件。
- 安全升级：root-only/admin/user/token-only route 必须有测试覆盖，避免 middleware 漏挂。
- 生态升级：relay route 应按 protocol version 做 capability advertisement 和 deprecation policy。

## 18 `service/`

1. **用途**

- `service` 是业务服务层，覆盖协议转换、计费/预扣/退款、文本/音频/任务 quota、token 估算、channel affinity、通道选择、订阅重置、文件加载、敏感词、HTTP client、通知、rankings、passkey、外部 OAuth credential refresh。
- 该层连接 controller、model、relay、setting、pkg，是实际业务算法最密集的位置。

2. **关键文件**

- `service/convert.go:1007 LoC`：Claude/Gemini/OpenAI 形态互转和 stream 转换。
- `service/channel_affinity.go:966 LoC`：账号/请求亲和、模板 override、缓存统计和 usage cache 信号。
- `service/quota.go:548 LoC`、`service/text_quota.go:479 LoC`：预扣、后结算、文本/音频 usage 计费。
- `service/billing_session.go:434 LoC`：统一计费会话、预扣、退款、结算。
- `service/channel_select.go:162 LoC`：retry 参数和满足条件通道缓存选择。
- `service/task_polling.go:560 LoC`、`service/task_billing.go:301 LoC`：异步任务轮询和完成后结算。

3. **入口 / 调用关系**

- controller relay 调用 token 估算、预扣计费、channel retry 参数和错误处理；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:145`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:164`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:181`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:306`。
- main 启动 credential refresh、订阅重置和任务 adaptor bridge；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:117`、`Calcium-Ion/new-api@d146e45e2f95:main.go:120`、`Calcium-Ion/new-api@d146e45e2f95:main.go:122`。

4. **核心 logic / 算法**

- 计费 session 提供 reserve、pre-consume、settle、refund 和信任额度旁路判定；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:41`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:82`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:152`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:186`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:282`。
- 文本 quota 根据 usage、cache tokens、tool surcharge、分层计费和 group ratio 合成最终扣费；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/text_quota.go:159`、`Calcium-Ion/new-api@d146e45e2f95:service/text_quota.go:322`、`Calcium-Ion/new-api@d146e45e2f95:service/tiered_settle.go:21`、`Calcium-Ion/new-api@d146e45e2f95:service/tiered_settle.go:95`。
- channel affinity 从上下文/header/body 中抽取 key，命中后优先复用通道，并记录 cache/usage 信号；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/channel_affinity.go:289`、`Calcium-Ion/new-api@d146e45e2f95:service/channel_affinity.go:545`、`Calcium-Ion/new-api@d146e45e2f95:service/channel_affinity.go:639`、`Calcium-Ion/new-api@d146e45e2f95:service/channel_affinity.go:741`。

5. **暴露功能**

- 用户看到更稳定的多通道重试、准确计费、订阅扣费、任务后结算、文件/媒体支持和协议互通。
- operator 看到 channel affinity 缓存统计、ranking、任务轮询、自动 credential refresh、quota 通知和性能数据。

6. **HUAKAI 升级点**

- 架构升级：service 层应按 money path、routing path、protocol conversion、ops analytics 分包，避免循环依赖和隐式 context。
- 算法升级：计费必须支持幂等 request id、账本双分录、订阅/钱包优先级、失败补偿和 delayed settlement。
- 生态升级：channel affinity 应升级为 request-to-account stickiness，带 TTL、理由、命中率、回滚和 operator 可视化。

## 19 `setting/`

1. **用途**

- `setting` 是配置系统，覆盖 ratio、billing expression、operation、system、console、performance、perf metrics、rate limit、auto group、payment、sensitive、model 特定设置等。
- 配置 manager 支持注册、从 DB 加载、保存和导出；证据见 `Calcium-Ion/new-api@d146e45e2f95:setting/config/config.go:14`、`Calcium-Ion/new-api@d146e45e2f95:setting/config/config.go:28`、`Calcium-Ion/new-api@d146e45e2f95:setting/config/config.go:42`、`Calcium-Ion/new-api@d146e45e2f95:setting/config/config.go:71`、`Calcium-Ion/new-api@d146e45e2f95:setting/config/config.go:286`。

2. **关键文件**

- `setting/ratio_setting/model_ratio.go:755 LoC`：模型倍率、价格、completion、image/audio ratio。
- `setting/ratio_setting/cache_ratio.go:164 LoC`、`group_ratio.go:125 LoC`：缓存和 group ratio。
- `setting/billing_setting/tiered_billing.go:106 LoC`：表达式计费模式与 smoke test。
- `setting/operation_setting/channel_affinity_setting.go:121 LoC`：亲和规则和 header 模板。
- `setting/operation_setting/status_code_ranges.go:208 LoC`：自动禁用/重试状态码区间。
- `setting/rate_limit.go:69 LoC`、`performance_setting/config.go:85 LoC`：限流和性能保护。

3. **入口 / 调用关系**

- main 启动时初始化 ratio settings，model 初始化 option map 后加载配置；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:273`、`Calcium-Ion/new-api@d146e45e2f95:main.go:290`。
- middleware/model-rate-limit 和 price helper 实时读取配置；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/model-rate-limit.go:170`、`Calcium-Ion/new-api@d146e45e2f95:middleware/model-rate-limit.go:187`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:67`。

4. **核心 logic / 算法**

- ratio logic 支持 model price、model ratio、completion ratio、cache/image/audio ratio、wildcard/匹配名和默认值；证据见 `Calcium-Ion/new-api@d146e45e2f95:setting/ratio_setting/model_ratio.go:367`、`Calcium-Ion/new-api@d146e45e2f95:setting/ratio_setting/model_ratio.go:403`、`Calcium-Ion/new-api@d146e45e2f95:setting/ratio_setting/model_ratio.go:443`、`Calcium-Ion/new-api@d146e45e2f95:setting/ratio_setting/model_ratio.go:629`、`Calcium-Ion/new-api@d146e45e2f95:setting/ratio_setting/model_ratio.go:725`。
- status code policy 把自动禁用和 retry 从硬编码状态码升级为可解析区间；证据见 `Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:17`、`Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:53`、`Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:80`、`Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:117`。

5. **暴露功能**

- 管理员能配置模型价格、倍率、group ratio、缓存 ratio、工具价格、rate limit、性能保护、passkey、OIDC、主题、支付和前端 console 内容。

6. **HUAKAI 升级点**

- 架构升级：配置应有 schema registry、版本、验证、变更审计、灰度发布和回滚。
- 算法升级：价格配置要支持 dry-run 对历史 usage 重算，检测表达式导致的异常账单。
- 安全升级：支付、OAuth、代理、header passthrough、敏感词、rate limit 属高风险配置，必须二次确认和审批链。

## 20 `types/`

1. **用途**

- `types` 存跨包基础类型：错误模型、file source、request meta、relay format、price data、并发 map/set、channel error。
- 错误类型能转换为 OpenAI/Claude 风格响应，并带 status code、skip retry、是否记录错误日志等选项；证据见 `Calcium-Ion/new-api@d146e45e2f95:types/error.go:13`、`Calcium-Ion/new-api@d146e45e2f95:types/error.go:21`、`Calcium-Ion/new-api@d146e45e2f95:types/error.go:90`、`Calcium-Ion/new-api@d146e45e2f95:types/error.go:180`、`Calcium-Ion/new-api@d146e45e2f95:types/error.go:213`、`Calcium-Ion/new-api@d146e45e2f95:types/error.go:381`。

2. **关键文件**

- `types/error.go:417 LoC`：统一错误和协议错误转换。
- `types/file_source.go:232 LoC`：URL/base64/file cache source。
- `types/request_meta.go:83 LoC`：token/file meta。
- `types/price_data.go:42 LoC`：价格和 group ratio 结果。
- `types/rw_map.go:103 LoC`、`types/set.go:42 LoC`：基础集合。
- `types/relay_format.go:19 LoC`：relay format 枚举。

3. **入口 / 调用关系**

- DTO 返回 token/file meta，service 计算价格和 quota，controller/middleware/relay 用统一错误类型输出协议响应。
- file source 被 DTO 媒体字段和 service 文件加载使用；证据见 `Calcium-Ion/new-api@d146e45e2f95:types/file_source.go:13`、`Calcium-Ion/new-api@d146e45e2f95:types/file_source.go:123`、`Calcium-Ion/new-api@d146e45e2f95:types/file_source.go:134`、`Calcium-Ion/new-api@d146e45e2f95:types/file_source.go:145`。

4. **核心 logic / 算法**

- 错误模型用统一内部 error 包装，再按目标协议转换成不同 response shape。
- file source 把 URL/base64/raw data 统一成可缓存、可清理的 source，支撑多模态输入和磁盘缓存。

5. **暴露功能**

- 用户看到与协议兼容的错误响应和多媒体输入支持。
- operator 看到更统一的错误记录、价格展示和 request meta。

6. **HUAKAI 升级点**

- 架构升级：types 应作为 internal contract package，禁止业务策略散落到基础类型。
- 安全升级：error type 必须内置 redaction level，不允许调用方自行决定是否 masking。
- 生态升级：file source 要带来源审计、下载策略、租户隔离、过期清理和尺寸/类型判定指标。

## 21 `web/`

1. **用途**

- `web` 包含两套前端：default 是较新的 React/TypeScript/Rsbuild/TanStack route 体系，classic 是 React/Vite/Semi Design 体系。
- 后端 embed 两套 build 产物，web router 根据主题选择静态资源；证据见 `Calcium-Ion/new-api@d146e45e2f95:main.go:38`、`Calcium-Ion/new-api@d146e45e2f95:main.go:44`、`Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:24`、`Calcium-Ion/new-api@d146e45e2f95:router/web-router.go:40`。

2. **关键文件**

- `web/default/src/main.tsx:169 LoC`：default app boot、router、theme/font/direction/provider。
- `web/default/src/routeTree.gen.ts:1283 LoC`：default route manifest。
- `web/default/package.json:100 LoC`：default 构建与依赖。
- `web/classic/src/App.jsx:386 LoC`：classic route tree。
- `web/classic/src/pages/Playground/index.jsx:565 LoC`、`web/classic/src/pages/Setting/index.jsx:217 LoC`：classic 关键页面。
- `web/classic/package.json:96 LoC`、`web/classic/vite.config.js:107 LoC`：classic 构建与代理。

3. **入口 / 调用关系**

- default main 初始化 TanStack router，并根据状态 API 更新 document title；证据见 `Calcium-Ion/new-api@d146e45e2f95:web/default/src/main.tsx:27`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/main.tsx:96`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/main.tsx:128`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/main.tsx:155`。
- default route manifest 覆盖 pricing、oauth、wallet、users、usage logs、system settings、subscriptions、playground、models、keys、dashboard、channels 等页面；证据见 `Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:16`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:36`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:40`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:43`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:47`。
- classic App 明确包含 console models、subscription、channel、token、oauth、pricing 等 route；证据见 `Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:111`、`Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:127`、`Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:135`、`Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:143`、`Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:243`、`Calcium-Ion/new-api@d146e45e2f95:web/classic/src/App.jsx:319`。

4. **核心 logic / 算法**

- default 前端按 feature folder 和 generated route tree 组织，classic 按 route + page/component 组织。
- 两套前端共存带来 parity 风险，`.agents` 中的 classic-to-default 同步 skill 正是对此风险的治理证据。

5. **暴露功能**

- 用户看到 home、pricing、auth、dashboard、wallet、keys、channels、models、subscriptions、usage logs、playground、system settings、legal pages。
- operator 看到系统设置、通道、模型、用量、用户、订阅、充值、部署和性能相关 UI。

6. **HUAKAI 升级点**

- 架构升级：避免长期双前端；如果必须双栈，必须有 route parity matrix、API coverage matrix 和视觉/交互 golden tests。
- 生态升级：Admin Ops UI 应围绕真实 operator workflow，而不是按后端表 CRUD 组织。
- 安全升级：前端所有高危动作需要 capability check、二次确认、审计 preview 和 revoke path。

## 跨目录 workflow trace

### A. Relay 文本请求主链路

1. `router/relay-router.go` 暴露 `/v1` 和协议兼容路径，挂系统性能检查、token 鉴权、模型限流和通道分发；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:69`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:71`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:72`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:73`、`Calcium-Ion/new-api@d146e45e2f95:router/relay-router.go:85`。
2. `middleware/auth.go` 解析 API key、用户状态、group 和 token 限制；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:332`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:367`、`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:421`。
3. `middleware/distributor.go` 从请求提取模型，校验 token model limit 和 group access，再设置 selected channel context；证据见 `Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:34`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:57`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:82`、`Calcium-Ion/new-api@d146e45e2f95:middleware/distributor.go:345`。
4. `controller/relay.go` 验证请求、生成 relay info、估算 token、价格计算、预扣、循环重试；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:109`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:120`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:145`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:153`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:164`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:190`。
5. `relay` helper 选择协议处理，adapter 发送上游请求，失败时回到 controller 的 retry/error path；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:212`、`Calcium-Ion/new-api@d146e45e2f95:relay/channel/api_request.go:290`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:231`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:324`。

### B. 计费与订阅预扣链路

1. `relay/helper/price.go` 先按模型价格/倍率/group ratio 或表达式计费计算预扣；证据见 `Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:67`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:70`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:88`、`Calcium-Ion/new-api@d146e45e2f95:relay/helper/price.go:241`。
2. `service/billing_session.go` 统一 reserve、pre-consume、refund 和 settle；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:152`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:186`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:82`、`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:41`。
3. `model/subscription.go` 支持订阅预扣、退款、到期失效和周期重置；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:970`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:1074`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:823`、`Calcium-Ion/new-api@d146e45e2f95:model/subscription.go:1100`。
4. `service/text_quota.go` 和 `service/quota.go` 在响应后根据真实 usage 做后结算与日志；证据见 `Calcium-Ion/new-api@d146e45e2f95:service/text_quota.go:322`、`Calcium-Ion/new-api@d146e45e2f95:service/quota.go:408`。

### C. 通道治理与健康链路

1. `controller/channel.go` 暴露通道列表、搜索、模型抓取、余额、测试、批量 tag、多密钥管理和本地模型操作；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/channel.go:71`、`Calcium-Ion/new-api@d146e45e2f95:controller/channel.go:199`、`Calcium-Ion/new-api@d146e45e2f95:controller/channel.go:1239`、`Calcium-Ion/new-api@d146e45e2f95:controller/channel.go:1698`。
2. `model/channel.go` 管理 key 列表、状态、多 key 禁用和 tag 批量更新；证据见 `Calcium-Ion/new-api@d146e45e2f95:model/channel.go:166`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:663`、`Calcium-Ion/new-api@d146e45e2f95:model/channel.go:752`。
3. `setting/operation_setting/status_code_ranges.go` 决定哪些错误触发 retry 或禁用；证据见 `Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:53`、`Calcium-Ion/new-api@d146e45e2f95:setting/operation_setting/status_code_ranges.go:80`。
4. `controller/relay.go` 在失败时调用通道错误处理，满足条件时异步禁用，并记录错误日志；证据见 `Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:356`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:360`、`Calcium-Ion/new-api@d146e45e2f95:controller/relay.go:366`。

### D. Admin UI 与 API contract 链路

1. `docs/openapi/api.json` 把后台 API 标记为系统、通道、令牌、模型等 tag；证据见 `Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:4`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:31`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:34`、`Calcium-Ion/new-api@d146e45e2f95:docs/openapi/api.json:55`。
2. `web/default/src/routeTree.gen.ts` 暴露 channels、models、dashboard、subscriptions、system settings 等页面；证据见 `Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:47`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:44`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:40`、`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:34`。
3. `router/api-router.go` 暴露对应后台 API route；证据见 `Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:251`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:294`、`Calcium-Ion/new-api@d146e45e2f95:router/api-router.go:384`。
4. `.agents/skills/classic-to-default-sync/SKILL.md` 说明两套前端需要功能同步治理；证据见 `Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:8`、`Calcium-Ion/new-api@d146e45e2f95:.agents/skills/classic-to-default-sync/SKILL.md:26`。

## HUAKAI 升级 punch list

| ref 项 | HUAKAI 现状 | HUAKAI 升级建议 | 升级维度 | 优先级 |
|---|---|---|---|---|
| new-api relay request hop lifecycle | 本轮未读 HUAKAI 代码，待核验 | 建 request hop trace：route、auth、quota、selected account、retry、settlement、error redaction 全链路可查 | 架构/生态 | P0 |
| new-api channel/key/multi-key management | 本轮未读 HUAKAI 代码，待核验 | 建 provider account health state machine，支持 key-level 禁用、冷却、恢复、手动覆盖和审计 | 算法/生态 | P0 |
| new-api pre-consume + refund + settlement | 本轮未读 HUAKAI 代码，待核验 | 采用 append-only ledger + idempotent request id + wallet/subscription funding source 分账 | 架构/安全 | P0 |
| new-api expression billing | 本轮未读 HUAKAI 代码，待核验 | 表达式计费进入 sandbox，配置有 smoke test、dry-run、历史重算和 rollback | 算法/安全 | P0 |
| new-api channel affinity | 本轮未读 HUAKAI 代码，待核验 | 升级为 account stickiness policy，输入 tenant/user/account/cache-hit/SLA，输出可解释 reason | 算法 | P1 |
| new-api status-code retry/auto-disable rules | 本轮未读 HUAKAI 代码，待核验 | Retry/disable policy 独立化，支持 per-provider profile、error taxonomy 和安全禁止重试列表 | 算法/安全 | P0 |
| new-api Admin API + web route surface | 本轮未读 HUAKAI 代码，待核验 | route 权限矩阵自动生成，绑定 OpenAPI、UI nav、审计 event 和 scenario tests | 架构/生态 | P0 |
| new-api dual frontend governance | 本轮未读 HUAKAI 代码，待核验 | 如 HUAKAI 有多 UI，需要 parity gate；如无，避免双栈长期并存 | 生态 | P1 |
| new-api performance metrics | 本轮未读 HUAKAI 代码，待核验 | 从 latency/usage bucket 升级到 SLO、error budget、成本/成功率/首 token 联合评分 | 生态/算法 | P1 |
| new-api deployment controller + external GPU client | 本轮未读 HUAKAI 代码，待核验 | 把 model deployment 作为插件化 provider lifecycle，不与核心 gateway 强绑定 | 架构 | P2 |
| new-api OAuth/custom provider | 本轮未读 HUAKAI 代码，待核验 | 自定义 OAuth 进入 enterprise plugin，强制 issuer/redirect/PKCE/JWK/audit guard | 安全 | P1 |
| new-api body/file source cache | 本轮未读 HUAKAI 代码，待核验 | 统一 request body store 与 remote file fetch guard，带租户归属、SSRF policy 和 cleanup metrics | 安全/生态 | P0 |
| new-api CI/CD release chain | 本轮未读 HUAKAI 代码，待核验 | release gate 加 SBOM、签名、migration dry-run、gateway smoke、edition manifest | 生态/安全 | P1 |

## Open questions

1. 本轮没有验证 new-api 当前默认分支是否仍是 HEAD，仅按本地 clone `d146e45e2f95` 观察。
2. 本轮没有执行测试或构建；所有结论来自 source regions 和目录扫描。
3. 本轮没有确认所有 provider adapter 的完整能力矩阵，只从 registry 和代表性 handler 观察到 adapter 形态。
4. 本轮没有深读支付 webhook 的幂等和签名校验细节，payment path 只作为 controller/model/setting 能力面记录。
5. 本轮没有深读每个 migration 是否仍可生产使用；`bin` 只记录为历史脚本。
6. 本轮没有完整读取 `web/default` 每个 feature 页面，只从 route manifest、package、主入口和代表页面观察 UI 面。
7. 本轮没有比较 HUAKAI 现状；punch list 中的“HUAKAI 现状”全部标记为待核验。
8. 本轮没有做 T2/T3 文件级精读；后续应按 request hop、billing ledger、channel health、Admin Ops 继续深挖。

## Source coverage proof

- 目录/SHA：`git rev-parse --short=12 HEAD`、`git log -1 --format=%cd --date=short`、`ls -la`、`tree -L 2 -d`。
- 启动和根层：`main.go`。
- 路由：`router/main.go`、`router/api-router.go`、`router/relay-router.go`、`router/web-router.go`、`router/dashboard.go`、`router/video-router.go`。
- 中间件：`middleware/auth.go`、`middleware/distributor.go`、`middleware/rate-limit.go`、`middleware/model-rate-limit.go`、`middleware/stats.go`、`middleware/performance.go`、`middleware/secure_verification.go`、`middleware/request-id.go`、`middleware/body_cleanup.go`、`middleware/cache.go`、`middleware/logger.go`。
- Controller：`controller/relay.go`、`controller/channel.go`、`controller/user.go`、`controller/token.go`、`controller/subscription.go`、`controller/topup.go`、`controller/log.go`、`controller/perf_metrics.go`、`controller/deployment.go`、`controller/custom_oauth.go`、`controller/passkey.go`、`controller/ratio_sync.go`、`controller/model_sync.go`。
- Model：`model/main.go`、`model/channel.go`、`model/ability.go`、`model/token.go`、`model/user.go`、`model/log.go`、`model/pricing.go`、`model/subscription.go`、`model/topup.go`、`model/perf_metric.go`、`model/custom_oauth_provider.go`、`model/model_meta.go`、`model/vendor_meta.go`。
- Service：`service/convert.go`、`service/quota.go`、`service/text_quota.go`、`service/channel_affinity.go`、`service/channel_select.go`、`service/billing_session.go`、`service/tiered_settle.go`、`service/task_polling.go`、`service/task_billing.go`、`service/token_counter.go`、`service/file_service.go`、`service/passkey/service.go`、`service/codex_oauth.go`、`service/codex_credential_refresh_task.go`、`service/rankings.go`。
- Relay：`relay/relay_adaptor.go`、`relay/relay_task.go`、`relay/compatible_handler.go`、`relay/responses_handler.go`、`relay/audio_handler.go`、`relay/image_handler.go`、`relay/embedding_handler.go`、`relay/mjproxy_handler.go`、`relay/gemini_handler.go`、`relay/common/relay_info.go`、`relay/common/billing.go`、`relay/common/request_conversion.go`、`relay/common/override.go`、`relay/channel/adapter.go`、`relay/channel/api_request.go`、`relay/channel/openai/relay-openai.go`、`relay/channel/gemini/relay-gemini.go`、`relay/channel/codex/oauth_key.go`、`relay/channel/aws/relay-aws.go`、`relay/channel/vertex/dto.go`。
- Package：`pkg/billingexpr/compile.go`、`pkg/billingexpr/run.go`、`pkg/billingexpr/settle.go`、`pkg/billingexpr/types.go`、`pkg/billingexpr/round.go`、`pkg/perf_metrics/metrics.go`、`pkg/perf_metrics/flush.go`、`pkg/perf_metrics/types.go`、`pkg/cachex/hybrid_cache.go`、`pkg/cachex/namespace.go`、`pkg/cachex/codec.go`、`pkg/ionet/client.go`、`pkg/ionet/deployment.go`、`pkg/ionet/hardware.go`、`pkg/ionet/container.go`、`pkg/ionet/types.go`、`pkg/ionet/jsonutil.go`。
- Setting：`setting/config/config.go`、`setting/ratio_setting/model_ratio.go`、`setting/ratio_setting/cache_ratio.go`、`setting/ratio_setting/group_ratio.go`、`setting/billing_setting/tiered_billing.go`、`setting/operation_setting/channel_affinity_setting.go`、`setting/operation_setting/status_code_ranges.go`、`setting/operation_setting/tools.go`、`setting/operation_setting/general_setting.go`、`setting/operation_setting/monitor_setting.go`、`setting/operation_setting/quota_setting.go`、`setting/operation_setting/token_setting.go`、`setting/operation_setting/payment_setting.go`、`setting/operation_setting/checkin_setting.go`、`setting/system_setting/oidc.go`、`setting/system_setting/passkey.go`、`setting/system_setting/theme.go`、`setting/system_setting/legal.go`、`setting/system_setting/fetch_setting.go`、`setting/system_setting/discord.go`、`setting/performance_setting/config.go`、`setting/perf_metrics_setting/config.go`、`setting/console_setting/config.go`、`setting/console_setting/validation.go`、`setting/rate_limit.go`、`setting/auto_group.go`、`setting/sensitive.go`。
- DTO/types/common/oauth/logger/i18n：`dto/openai_request.go`、`dto/openai_response.go`、`dto/claude.go`、`dto/gemini.go`、`dto/openai_image.go`、`dto/audio.go`、`dto/task.go`、`dto/channel_settings.go`、`types/error.go`、`types/file_source.go`、`types/request_meta.go`、`types/price_data.go`、`types/relay_format.go`、`common/body_storage.go`、`common/disk_cache.go`、`common/disk_cache_config.go`、`common/redis.go`、`common/gin.go`、`common/ssrf_protection.go`、`common/audio.go`、`common/crypto.go`、`common/constants.go`、`common/env.go`、`common/embed-file-system.go`、`common/endpoint_type.go`、`common/page_info.go`、`common/performance_config.go`、`common/rate-limit.go`、`common/str.go`、`common/system_monitor.go`、`oauth/generic.go`、`oauth/provider.go`、`oauth/types.go`、`oauth/registry.go`、`logger/logger.go`、`i18n/i18n.go`、`i18n/keys.go`。
- Frontend/desktop/docs/CI：`web/default/package.json`、`web/default/rsbuild.config.ts`、`web/default/src/main.tsx`、`web/default/src/routeTree.gen.ts`、`web/default/src/features/*` file list、`web/classic/package.json`、`web/classic/vite.config.js`、`web/classic/src/App.jsx`、`web/classic/src/pages/*` file list、`electron/main.js`、`electron/package.json`、`electron/preload.js`、`electron/README.md`、`.github/workflows/*.yml`、`.github/SECURITY.md`、`.agents/skills/*`、`docs/openapi/api.json`、`docs/openapi/relay.json`、`docs/channel/other_setting.md`、`docs/ionet-client.md`、`bin/time_test.sh`、`bin/migration_v0.2-v0.3.sql`、`bin/migration_v0.3-v0.4.sql`。

---

Agent: codex

Ref: new-api

SHA: d146e45e2f95

Pushed: 2026-05-09

Mining started: 2026-05-13T07:52:51Z

Mining done: 2026-05-13T08:21:30Z

Output LoC: 942

Source files read (per CLAUDE.md #11 closing): see "Source coverage proof" above.

Lane: specifier

Agent ID: Codex / GPT-5

UTC timestamp: 2026-05-13T08:21:30Z

Owner 中文摘要：本轮对 new-api 做了 T1 顶层目录骨架拆解；真实观察主要来自启动、router、middleware、controller、model、service、relay、setting、web、CI 和 docs 的 file:line 证据；合理推断集中在 HUAKAI 升级点，不把 ref 结构复制为 HUAKAI 设计；open questions 为 8 个，主要是未读 HUAKAI 代码、未跑测试、未做 T2/T3 文件级精读。
