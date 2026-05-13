# 2026-05-13 Helicone 目录骨架拆解（Codex Lane）

| 项 | 值 |
|---|---|
| Ref project | Helicone |
| Local path | `~/refs/helicone/` |
| Lane | codex specifier |
| SHA | `3f4bd44b85f9` |
| Last commit date | `2026-05-01` |
| Mining started | `2026-05-13T09:00:52Z` |
| Clean-room mode | 只写行为和目录职责；不复制上游函数、结构体、注释、schema 或实现顺序 |
| Observed regions | 69 |
| Inferences | 18 |
| Open questions | 7 |

## 0. 总览

- Helicone 是一个 AI Gateway + LLM observability monorepo：根 README 明确把产品定位为网关、观测、成本/延迟追踪、数据集、微调与自动 fallback 的组合平台。证据：`Helicone/helicone@3f4bd44b85f9:README.md:25`、`Helicone/helicone@3f4bd44b85f9:README.md:27`、`Helicone/helicone@3f4bd44b85f9:README.md:29`。
- 根 workspace 把 `bifrost`、`web`、共享包、后端服务、worker、E2E 放进同一 Node 20 monorepo；`e2e` 也在 workspace 列表中。证据：`Helicone/helicone@3f4bd44b85f9:package.json:4`。
- 自托管说明把运行面拆成五类核心服务：Web、边缘 worker、后端 API、Supabase、ClickHouse，再加对象存储。证据：`Helicone/helicone@3f4bd44b85f9:README.md:86`。
- 本报告覆盖顶层一级目录，排除 `.git` 仓库元数据；`.git` 不属于产品或工程模块。
- 目录证据来自 `find -maxdepth 2`、`ls -la`、`wc -l`，少量关键入口用 `nl -ba | sed` 抽读。
- HUAKAI 升级点是基于观察到的上游行为做的产品工程推断，不表示 HUAKAI 当前代码已经具备或缺失相同能力。

## 1. 顶层文件（非一级目录，但解释 monorepo 入口）

1. **用途**
   - 顶层文件定义 monorepo 工作区、all-in-one 自托管镜像、进程编排与全局文档入口。
   - 根 README 把 quick start 指向网关 endpoint 和 dashboard 日志查看，说明根层是 operator onboarding 的第一入口。证据：`Helicone/helicone@3f4bd44b85f9:README.md:40`、`Helicone/helicone@3f4bd44b85f9:README.md:60`。
2. **关键文件**
   - `README.md:167`：产品定位、quick start、自托管和架构组件。
   - `package.json:31`：workspace 与根级 lint/build scripts。
   - `Dockerfile:167`：all-in-one 镜像分阶段构建，把数据库迁移、后端、web、对象存储打到同一镜像。
   - `supervisord.conf:110`：all-in-one 运行时进程控制，启动数据库、后端、web、迁移和对象存储。
   - `.env.example:13`：最小环境变量示例。
3. **入口 / 调用关系**
   - 根 `package.json` 将 web、marketing、packages、后端、worker、e2e 设为工作区，依赖安装和 lint 从根分发。证据：`Helicone/helicone@3f4bd44b85f9:package.json:4`。
   - all-in-one 镜像先准备数据库和迁移素材，再构建后端与 web。证据：`Helicone/helicone@3f4bd44b85f9:Dockerfile:46`、`Helicone/helicone@3f4bd44b85f9:Dockerfile:91`、`Helicone/helicone@3f4bd44b85f9:Dockerfile:106`。
4. **核心 logic / 算法**
   - 根层的 logic 是“一个仓库，多运行面”：开发时用 workspace 共享类型与包；自托管时用容器或 supervisor 串起存储、迁移、API、UI。
   - 迁移进程会等待 PostgreSQL / ClickHouse 后再执行，降低冷启动顺序风险。证据：`Helicone/helicone@3f4bd44b85f9:supervisord.conf:57`、`Helicone/helicone@3f4bd44b85f9:supervisord.conf:66`。
5. **暴露功能**
   - 用户看到的是单一网关 endpoint、dashboard、self-host Docker/Helm 路径、模型与成本资料库入口。证据：`Helicone/helicone@3f4bd44b85f9:README.md:42`、`Helicone/helicone@3f4bd44b85f9:README.md:80`、`Helicone/helicone@3f4bd44b85f9:README.md:153`。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 可把“root manifest + service map + readiness matrix”做成强约束文档，避免 monorepo 内服务边界靠 README 口头约定。
   - 生态升级：自托管入口应同时给出 all-in-one、dev compose、production helm 的适用边界和风险提示。
   - 运维升级：all-in-one 模式需要明确哪些 secrets 是 dummy、哪些不可生产复用。

## 2. `.claude/`

1. **用途**
   - 该目录保存 Claude Code 本地权限、流程提示和项目内部 agent 工作说明；不是产品运行路径。
   - 本次只把它视为工程协作元数据，不从中提取业务实现。
2. **关键文件**
   - `.claude/settings.local.json:121`：本地工具权限白名单。
   - `.claude/processes/add_new_cost:122`：成本包维护流程提示。
   - `.claude/processes/add_trusted_domain_readme.md:116`：受信域名类变更流程。
   - `.claude/summaries/slack-intercom.md:267`：集成沟通摘要。
3. **入口 / 调用关系**
   - 本地 agent 或 IDE 读取；产品服务不引用该目录。
   - 权限文件列出允许执行的 shell、浏览器、GitHub、测试和构建动作。证据：`Helicone/helicone@3f4bd44b85f9:.claude/settings.local.json:3`。
4. **核心 logic / 算法**
   - 主要 logic 是把 agent 操作边界显式化：哪些命令能跑、哪些外部域能抓取、哪些 MCP 能调用。
   - 成本流程提示强化“查价格、转换单位、更新模型覆盖”的人工规则；这类规则不应直接转写进 HUAKAI 实现。
5. **暴露功能**
   - 对 end user 无直接功能；对 maintainer 是本地自动化护栏。
   - 间接影响模型价格、文档、测试、PR review 等维护路径。
6. **HUAKAI 升级点**
   - 生态升级：把 agent 权限和流程模板纳入版本化治理，但不要让本地权限文件成为生产配置来源。
   - 安全升级：HUAKAI 应要求 agent 权限文件标明危险命令、网络域、secret 读取边界。

## 3. `.cursor/`

1. **用途**
   - Cursor 规则目录记录系统架构、网关文档、后端和数据库开发约束。
   - 它是 IDE/agent 辅助规则，不是 runtime 模块。
2. **关键文件**
   - `.cursor/rules/helicone-system-overview.mdc:100`：系统组件和数据流说明。
   - `.cursor/rules/ai-gateway-docs.mdc:76`：网关文档准确性规则。
   - `.cursor/rules/jawn.mdc:72`：后端开发提示。
   - `.cursor/rules/supabase.mdc:14`：数据库相关提示。
3. **入口 / 调用关系**
   - 由 Cursor/agent 在编辑时加载。
   - 系统概览把前端、worker、后端、marketing、数据库、对象存储、队列串成一条日志处理链。证据：`Helicone/helicone@3f4bd44b85f9:.cursor/rules/helicone-system-overview.mdc:8`、`Helicone/helicone@3f4bd44b85f9:.cursor/rules/helicone-system-overview.mdc:56`。
4. **核心 logic / 算法**
   - 规则层的核心是防止文档与配置漂移，尤其是网关配置形态和认证依赖。证据：`Helicone/helicone@3f4bd44b85f9:.cursor/rules/ai-gateway-docs.mdc:8`、`Helicone/helicone@3f4bd44b85f9:.cursor/rules/ai-gateway-docs.mdc:45`。
5. **暴露功能**
   - 用户不可见；维护者可借此减少错误示例、旧配置和无效模型格式进入 docs。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI 的 docs 规则应与 API schema 自动校验绑定，不能只靠编辑器提示。
   - 架构升级：把“配置破坏性变更”单列成 docs lint 输入，避免 UI、SDK、docs 不同步。

## 4. `.devcontainer/`

1. **用途**
   - 为 Codespaces / devcontainer 准备一键开发环境，自动拉起 Docker、Supabase、ClickHouse 迁移和 env 文件。
2. **关键文件**
   - `.devcontainer/devcontainer.json:35`：devcontainer 配置。
   - `.devcontainer/.build.sh:19`：post-start 环境初始化脚本。
   - `.devcontainer/Dockerfile:24`：开发容器基础镜像。
3. **入口 / 调用关系**
   - devcontainer 配置在容器启动后执行构建脚本。证据：`Helicone/helicone@3f4bd44b85f9:.devcontainer/devcontainer.json:17`。
   - 构建脚本启动 Docker、对象存储、ClickHouse、Supabase，并执行迁移。证据：`Helicone/helicone@3f4bd44b85f9:.devcontainer/.build.sh:7`、`Helicone/helicone@3f4bd44b85f9:.devcontainer/.build.sh:15`。
4. **核心 logic / 算法**
   - 这里的 logic 是“可复制开发基线”：容器启动后自动准备依赖服务、迁移和本地 env。
   - 风险点是该脚本会修改本地容器状态，并把开发数据路径固定到 workspaces。
5. **暴露功能**
   - 对开发者暴露可立即调试的 web、worker、后端、数据库环境。
   - 对用户无直接功能。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI 可提供 read-only demo seed 和 destructive reset 明确开关。
   - 安全升级：devcontainer post-start 应区分首次初始化和重复启动，避免无意重置本地数据。

## 5. `.github/`

1. **用途**
   - GitHub Actions、issue/PR 模板、review 指南集中目录。
   - 它承担 CI、迁移编号校验、worker 测试、E2E 测试、部署预检。
2. **关键文件**
   - `.github/workflows/worker-test.yml:68`：worker lint + test。
   - `.github/workflows/e2e-test-suite.yml:139`：启动依赖服务后跑网关 E2E。
   - `.github/workflows/clickhouse-migration-check.yml:49`：ClickHouse 迁移编号连续性检查。
   - `.github/PULL_REQUEST_TEMPLATE.md`：PR 模板。
   - `.github/dependabot.yml`：依赖更新配置。
3. **入口 / 调用关系**
   - worker 流水线只在 worker、packages 或自身 workflow 变更时触发。证据：`Helicone/helicone@3f4bd44b85f9:.github/workflows/worker-test.yml:8`。
   - E2E 流水线会启动 ClickHouse、对象存储、Supabase、两个 worker 和后端服务，再跑测试。证据：`Helicone/helicone@3f4bd44b85f9:.github/workflows/e2e-test-suite.yml:40`、`Helicone/helicone@3f4bd44b85f9:.github/workflows/e2e-test-suite.yml:47`、`Helicone/helicone@3f4bd44b85f9:.github/workflows/e2e-test-suite.yml:101`。
4. **核心 logic / 算法**
   - CI 的核心是路径过滤 + 服务编排 + health retry + 失败日志输出。
   - 迁移检查单独验证 ClickHouse 文件编号无重复、无断档。证据：`Helicone/helicone@3f4bd44b85f9:.github/workflows/clickhouse-migration-check.yml:29`、`Helicone/helicone@3f4bd44b85f9:.github/workflows/clickhouse-migration-check.yml:41`。
5. **暴露功能**
   - 对贡献者暴露质量门；对 operator 暴露 release 前迁移和网关行为可靠性。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 应把 gateway、billing、quota、auth 的路径过滤 CI 分层，不让低风险 UI 改动触发全量昂贵测试。
   - 运维升级：E2E health retry 和失败日志应标准化为可复用 composite action。

## 6. `.husky/`

1. **用途**
   - 本地 Git hook 目录，主要防止直接推送 main。
2. **关键文件**
   - `.husky/pre-push:11`：推送保护脚本。
   - `.husky/pre-commit:0`：空 pre-commit 占位。
3. **入口 / 调用关系**
   - 根 `package.json` 的 prepare script 安装 husky。证据：`Helicone/helicone@3f4bd44b85f9:package.json:15`。
   - pre-push 读取当前分支并阻止 main。证据：`Helicone/helicone@3f4bd44b85f9:.husky/pre-push:2`。
4. **核心 logic / 算法**
   - 本地保护 logic 很窄：仅分支名判断。
   - 没有观察到本地格式化、类型检查或 secret scan 被 hook 强制执行。
5. **暴露功能**
   - 维护者在本地 push 时获得主分支保护。
   - CI 仍需承担真正的质量门。
6. **HUAKAI 升级点**
   - 安全升级：HUAKAI 可加入 non-secret scan 和 migration danger scan，但保持 hook 快速，重检查放 CI。

## 7. `.vscode/`

1. **用途**
   - VS Code 调试配置，覆盖后端、web、示例、Python、worker miniflare attach。
2. **关键文件**
   - `.vscode/launch.json:72`：调试启动配置集合。
3. **入口 / 调用关系**
   - IDE 读取；配置把后端 cwd 指向 `valhalla/jawn`，web 指向 `web`，worker attach 指向 worker。证据：`Helicone/helicone@3f4bd44b85f9:.vscode/launch.json:5`、`Helicone/helicone@3f4bd44b85f9:.vscode/launch.json:17`、`Helicone/helicone@3f4bd44b85f9:.vscode/launch.json:62`。
4. **核心 logic / 算法**
   - 该目录没有业务算法；它把多服务调试入口显式化。
5. **暴露功能**
   - 对开发者暴露一键启动/attach 调试。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI 可提供按 vertical slice 的 launch profile，例如 gateway-only、ops-ui-only、billing-readonly。

## 8. `bifrost/`

1. **用途**
   - Marketing / public site，承载 landing、模型浏览、集成展示、博客/MDX 内容和前台 analytics。
   - 根系统规则把它归为 landing page and blog 应用。证据：`Helicone/helicone@3f4bd44b85f9:.cursor/rules/helicone-system-overview.mdc:24`。
2. **关键文件**
   - `bifrost/package.json:86`：Next 14 app、MDX、地图/图表、pricing 包依赖。
   - `bifrost/app/page.tsx:78`：首页按 section 懒加载营销模块。
   - `bifrost/app/layout.tsx:116`：metadata、analytics、第三方脚本。
   - `bifrost/hooks/useModelFiltering.ts:178`：模型列表搜索、筛选、排序状态。
   - `bifrost/lib/utils.ts`：布局和样式工具。
3. **入口 / 调用关系**
   - package scripts 暴露 dev/build/start/lint/postbuild sitemap。证据：`Helicone/helicone@3f4bd44b85f9:bifrost/package.json:5`。
   - 首页从多个 home/template 模块组成，重模块以动态加载推迟。证据：`Helicone/helicone@3f4bd44b85f9:bifrost/app/page.tsx:8`。
4. **核心 logic / 算法**
   - 核心 logic 是“营销页面 + 模型索引”：公开页面将网关、日志、生产化能力、统计等模块串成 funnel。
   - 模型过滤先做轻量搜索，再叠加 provider、价格、上下文、能力等维度并排序。证据：`Helicone/helicone@3f4bd44b85f9:bifrost/hooks/useModelFiltering.ts:105`。
5. **暴露功能**
   - 访客能看到 AI Gateway、模型目录、集成矩阵、定价/成本导向入口。
   - 模型搜索体验支持多维筛选，是 gateway 产品转化入口。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI 可把模型目录与实际路由健康、价格版本、账号池可用性联动。
   - 架构升级：marketing 与 ops dashboard 的 model registry 读取应共享只读 API，不重复静态逻辑。

## 9. `cc-agent/`

1. **用途**
   - 内部 agent loop 工具，用 Codespaces 跑 Claude Code，持续迭代直到产生完成证明。
2. **关键文件**
   - `cc-agent/README.md:213`：使用说明和 loop 模型。
   - `cc-agent/run.sh:90`：执行循环脚本。
   - `cc-agent/generate-prompt.sh`：拼装 prompt。
   - `cc-agent/src/base_prompt.md:50`：基础 prompt。
   - `cc-agent/src/task-template.md`：任务模板。
3. **入口 / 调用关系**
   - README 说明先编辑 task，再运行脚本，并等待 `.agent/DONE.md`。证据：`Helicone/helicone@3f4bd44b85f9:cc-agent/README.md:80`、`Helicone/helicone@3f4bd44b85f9:cc-agent/README.md:95`。
   - 脚本每轮生成 prompt，首轮新开，后续继续上下文。证据：`Helicone/helicone@3f4bd44b85f9:cc-agent/run.sh:21`、`Helicone/helicone@3f4bd44b85f9:cc-agent/run.sh:48`。
4. **核心 logic / 算法**
   - 核心是“生成 prompt -> 调 agent -> 检查完成文件 -> 继续/退出”的本地控制循环。
   - 它依赖非常高权限的 agent 模式，不能直接作为 HUAKAI 安全生产自动化模板。
5. **暴露功能**
   - 对维护者暴露长任务自动执行能力。
   - 对 end user 无直接功能。
6. **HUAKAI 升级点**
   - 安全升级：HUAKAI 的 agent loop 应加入只读/写入 scope、diff review、超时和成果校验，不允许无限高权限循环。

## 10. `clickhouse/`

1. **用途**
   - Analytics 存储迁移、seed、回填与本地管理脚本目录。
   - README 直接说明该目录保存 ClickHouse 数据库和迁移。证据：`Helicone/helicone@3f4bd44b85f9:clickhouse/README.md:1`。
2. **关键文件**
   - `clickhouse/README.md:46`：迁移说明。
   - `clickhouse/ch_hcone.py:336`：ClickHouse 管理 CLI。
   - `clickhouse/migrations/schema_*.sql`：连续编号 analytics schema 演进。
   - `clickhouse/seeds/*.sql`：角色和只读权限 seed。
   - `clickhouse/backfill_clickhouse.py:90`、`clickhouse/backfill_postgres.py:78`：历史数据回填脚本。
3. **入口 / 调用关系**
   - Docker/supervisor/CI 都会调用迁移 CLI 或检查迁移编号。证据：`Helicone/helicone@3f4bd44b85f9:supervisord.conf:66`、`Helicone/helicone@3f4bd44b85f9:.github/workflows/clickhouse-migration-check.yml:22`。
   - README 说明迁移文件按数字顺序应用并记录已应用状态。证据：`Helicone/helicone@3f4bd44b85f9:clickhouse/README.md:6`。
4. **核心 logic / 算法**
   - 迁移管理 logic：收集 SQL 文件、按 schema 编号排序、用 HTTP 接口执行、检测错误、可列出已应用记录。证据：`Helicone/helicone@3f4bd44b85f9:clickhouse/ch_hcone.py:13`、`Helicone/helicone@3f4bd44b85f9:clickhouse/ch_hcone.py:29`。
   - 这不是请求路径 runtime，但决定 analytics 表能否支持 cost、cache、session、rate limit、gateway 等维度。
5. **暴露功能**
   - Operator 获得日志分析、成本查询、HQL、报表、告警等功能的列式存储基础。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 应把 analytics schema 变更与 dashboard query acceptance test 绑定。
   - 运维升级：迁移 CLI 应具备 dry-run、lock、rollback note、per-edition 兼容矩阵。

## 11. `docker/`

1. **用途**
   - 自托管和本地开发 compose、容器 Dockerfile、Terraform、volume 配置集中目录。
2. **关键文件**
   - `docker/README.md:187`：self-host 和 local dev 说明。
   - `docker/docker-compose.yml:469`：PostgreSQL、ClickHouse、MinIO、后端、web、worker 等 profile。
   - `docker/helicone-compose.sh:137`：profile helper。
   - `docker/dockerfiles/dockerfile_worker`：worker 镜像。
   - `docker/volumes/db/*.sql`、`docker/volumes/logs/vector.yml`：依赖服务配置。
3. **入口 / 调用关系**
   - README 提供 all-in-one 和 compose 两类路径。证据：`Helicone/helicone@3f4bd44b85f9:docker/README.md:3`、`Helicone/helicone@3f4bd44b85f9:docker/README.md:21`。
   - compose 先启动基础设施，再通过 profile 启动 core / dev / workers。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:3`、`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:125`、`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:280`。
4. **核心 logic / 算法**
   - 运行 logic 是 healthcheck + depends_on + profile 分层。
   - helper 把 infra、helicone、dev、workers、kafka、all 映射为 compose profile。证据：`Helicone/helicone@3f4bd44b85f9:docker/helicone-compose.sh:21`、`Helicone/helicone@3f4bd44b85f9:docker/helicone-compose.sh:49`。
5. **暴露功能**
   - Operator 可启动基础设施、完整平台、开发热 reload、worker 组。
6. **HUAKAI 升级点**
   - 运维升级：HUAKAI compose 应提供 `ops-lite`、`gateway-only`、`full-observability` profile，并对每个 profile 明确数据保留和 secret 风险。

## 12. `docs/`

1. **用途**
   - Mintlify 文档站源码，覆盖 quick start、AI Gateway、observability、prompts、headers、REST/GraphQL、工具和集成。
2. **关键文件**
   - `docs/docs.json:723`：文档导航和主题。
   - `docs/gateway/overview.mdx:99`：AI Gateway 公开说明。
   - `docs/features/alerts.mdx:181`：告警能力说明。
   - `docs/introduction.mdx:96`：平台功能入口。
   - `docs/ai-gateway.openapi.json`、`docs/swagger.json`：API contract 资产。
3. **入口 / 调用关系**
   - 文档导航将 AI Gateway、Observability、Prompt Management 等作为主要分组。证据：`Helicone/helicone@3f4bd44b85f9:docs/docs.json:24`、`Helicone/helicone@3f4bd44b85f9:docs/docs.json:70`、`Helicone/helicone@3f4bd44b85f9:docs/docs.json:104`。
4. **核心 logic / 算法**
   - 文档的核心是把 gateway 请求链解释为统一 SDK 入口、转换/路由、provider 响应、日志采集。证据：`Helicone/helicone@3f4bd44b85f9:docs/gateway/overview.mdx:26`。
   - 告警文档把 error rate、cost、latency、tokens、count 等指标变成 operator 阈值和通知配置。证据：`Helicone/helicone@3f4bd44b85f9:docs/features/alerts.mdx:9`、`Helicone/helicone@3f4bd44b85f9:docs/features/alerts.mdx:99`。
5. **暴露功能**
   - 用户能学习网关集成、provider routing、prompt integration、alerts、reports、HQL、sessions、webhooks。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI docs 应从第一天把 API contract、UI screenshots、ops runbooks、failure recovery 合并，而不是只写 happy path。

## 13. `e2e/`

1. **用途**
   - 独立 E2E 测试工程，聚焦 AI Gateway；README 明确它不属于生产 workspace 的常规依赖链。证据：`Helicone/helicone@3f4bd44b85f9:e2e/README.md:1`、`Helicone/helicone@3f4bd44b85f9:e2e/README.md:5`。
2. **关键文件**
   - `e2e/README.md:45`：本地运行说明。
   - `e2e/package.json:26`：Jest scripts。
   - `e2e/tests/nightly/gateway.test.ts:155`：provider 级网关覆盖。
   - `e2e/lib/ai-gateway/client.ts`：测试 client。
   - `e2e/lib/wallet-helpers.ts`：测试钱包准备。
3. **入口 / 调用关系**
   - 本地先启动两个 worker 和后端，再运行 Jest。证据：`Helicone/helicone@3f4bd44b85f9:e2e/README.md:16`、`Helicone/helicone@3f4bd44b85f9:e2e/README.md:34`。
   - CI 也用同样思路准备依赖服务。证据：`Helicone/helicone@3f4bd44b85f9:.github/workflows/e2e-test-suite.yml:109`。
4. **核心 logic / 算法**
   - 测试 logic 是 provider matrix + wallet setup + chat/responses API 双路径验证。
   - gateway test 先给测试组织加余额，再遍历 provider 列表确认基础请求能返回可消费结构。证据：`Helicone/helicone@3f4bd44b85f9:e2e/tests/nightly/gateway.test.ts:13`、`Helicone/helicone@3f4bd44b85f9:e2e/tests/nightly/gateway.test.ts:27`。
5. **暴露功能**
   - 对 operator 暴露“多 provider 是否仍可用”的 release confidence。
6. **HUAKAI 升级点**
   - 测试升级：HUAKAI 应把 gateway E2E 分成 cheap mock、sandbox provider、paid provider 三层，并单独覆盖 failover、quota、billing ledger。

## 14. `examples/`

1. **用途**
   - SDK 和 feature examples，覆盖 AI Gateway、cache、prompt、score、sessions、provider-specific、Vercel AI SDK、MCP、HTTP requests。
2. **关键文件**
   - `examples/ai_gateway/index.ts:141`：多 provider / fallback 网关示例。
   - `examples/cache/index.ts:54`：缓存 header 示例。
   - `examples/promptsV3/index.ts:68`：prompt 模板示例。
   - `examples/worker-helicone-scores/README.md:21`：Cloudflare worker score template。
   - `examples/http-requests/README.md`：curl/HTTP 示例。
3. **入口 / 调用关系**
   - 每个示例通常自带 package 或 README，由开发者独立运行。
   - AI Gateway 示例演示用 OpenAI/Anthropic SDK、指定模型/provider、逗号式 fallback。证据：`Helicone/helicone@3f4bd44b85f9:examples/ai_gateway/index.ts:8`、`Helicone/helicone@3f4bd44b85f9:examples/ai_gateway/index.ts:104`。
4. **核心 logic / 算法**
   - examples 的 logic 是把功能开关通过请求 header、base URL、模型字符串、prompt id 显式传给网关。
   - cache 示例把缓存启用和 bucket size 放在请求 header 中。证据：`Helicone/helicone@3f4bd44b85f9:examples/cache/index.ts:11`。
5. **暴露功能**
   - 用户能复制并验证 gateway、cache、prompt、score、session、provider gateway 等场景。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI examples 应按“可跑、可测、可观测”标准维护，每个示例有 expected dashboard result 和 failure mode。

## 15. `helicone-cron/`

1. **用途**
   - 该目录存在 `src/db/database.types.ts`，从浅层目录看像历史/占位 cron 包。
   - 本次未发现 package 入口或 README，不能断言其 runtime 仍活跃。
2. **关键文件**
   - `helicone-cron/src/db/database.types.ts`：数据库类型定义。
3. **入口 / 调用关系**
   - 未观察到顶层 package scripts；可能被历史流程或生成器使用，需 T3 精读确认。
4. **核心 logic / 算法**
   - 目前只观察到类型资产，不足以证明它有独立调度 logic。
5. **暴露功能**
   - 无可确认用户功能。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 应避免保留无法判定归属的顶层服务目录；若保留，应加 README 标注 owner、入口、是否 deprecated。

## 16. `helicone-heartbeat/`

1. **用途**
   - Cloudflare scheduled worker，用于周期健康检查、队列拥塞检测和 Slack/operator 告警。
2. **关键文件**
   - `helicone-heartbeat/package.json:17`：wrangler scripts。
   - `helicone-heartbeat/src/index.ts:30`：scheduled handler 入口。
   - `helicone-heartbeat/src/AlertManager.ts:65`：Slack 通知封装。
   - `helicone-heartbeat/src/alertSqsCongestion.ts:97`：队列拥塞检测。
   - `helicone-heartbeat/wrangler.jsonc`：worker 配置。
3. **入口 / 调用关系**
   - package scripts 支持 US/EU 部署和 test scheduled。证据：`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/package.json:5`。
   - scheduled handler 每分钟检查后端 health endpoint，并调用队列拥塞检查。证据：`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/index.ts:12`。
4. **核心 logic / 算法**
   - 心跳 logic：按 cron 触发，先确认后端健康，再取队列大小；超过阈值时激活公开 alert banner 并发 Slack，恢复时关闭。证据：`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/alertSqsCongestion.ts:9`、`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/alertSqsCongestion.ts:22`。
5. **暴露功能**
   - Operator 获得队列堆积告警和用户可见状态横幅。
6. **HUAKAI 升级点**
   - 运维升级：HUAKAI 应把队列阈值、通知渠道、自动恢复动作做成可审计配置，并支持 per-tenant / per-region 抑制。

## 17. `helicone-mcp/`

1. **用途**
   - MCP server，允许 agent 查询 Helicone observability 数据，也可通过网关发起 LLM 请求。
2. **关键文件**
   - `helicone-mcp/README.md:71`：MCP server 使用和工具说明。
   - `helicone-mcp/package.json:54`：npm package、build、type generation scripts。
   - `helicone-mcp/src/index.ts:195`：stdio MCP server 入口。
   - `helicone-mcp/src/lib/helicone-client.ts`：实际 HTTP client。
   - `helicone-mcp/src/types/generated-zod.ts`：工具参数 schema。
3. **入口 / 调用关系**
   - README 提供 npx stdio 配置，并要求环境变量提供 API key。证据：`Helicone/helicone@3f4bd44b85f9:helicone-mcp/README.md:5`、`Helicone/helicone@3f4bd44b85f9:helicone-mcp/README.md:69`。
   - server 注册请求查询、会话查询、网关调用三类工具。证据：`Helicone/helicone@3f4bd44b85f9:helicone-mcp/README.md:22`、`Helicone/helicone@3f4bd44b85f9:helicone-mcp/src/index.ts:20`。
4. **核心 logic / 算法**
   - MCP logic：启动时验证 API key；每个 tool 做参数 schema 校验、调用 Helicone API、返回 JSON 文本。
   - 查询工具支持 filter、pagination、sort 和是否包含 bodies。证据：`Helicone/helicone@3f4bd44b85f9:helicone-mcp/src/index.ts:20`。
5. **暴露功能**
   - AI agent 可直接查请求、查会话、调用统一网关。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI MCP 应内建 audit trail、tenant scoping、PII redaction、signed body URL 的最小权限策略。

## 18. `packages/`

1. **用途**
   - 共享业务包：成本/模型 registry、过滤表达式、LLM 格式映射、pricing、prompt、secret、common utilities。
2. **关键文件**
   - `packages/README.md:73`：共享包总览与测试说明。
   - `packages/cost/README.md:329`：模型价格、BYOK/PTB、registry 说明。
   - `packages/filters/filterExpressions.ts:702`：过滤表达式核心。
   - `packages/llm-mapper/README.md:36`：请求/响应映射说明。
   - `packages/prompts/HeliconePromptManager.ts:420`、`packages/secrets/SecretManager.ts:123`：prompt 与 secret 公共逻辑。
3. **入口 / 调用关系**
   - web、worker、后端都通过 workspace 依赖这些包。证据：`Helicone/helicone@3f4bd44b85f9:web/package.json:33`、`Helicone/helicone@3f4bd44b85f9:worker/package.json:51`、`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/package.json:23`。
4. **核心 logic / 算法**
   - cost 包定义模型价格与 endpoint 配置，明确支持用户自带 key 与平台代付两种场景。证据：`Helicone/helicone@3f4bd44b85f9:packages/cost/README.md:1`、`Helicone/helicone@3f4bd44b85f9:packages/cost/README.md:48`。
   - mapping 包把 provider 原始格式和平台统一展示/存储格式分层，避免 dashboard 直接依赖 provider 原始结构。证据：`Helicone/helicone@3f4bd44b85f9:packages/llm-mapper/README.md:6`。
5. **暴露功能**
   - 用户感知为成本计算、多 provider 支持、统一请求查看、筛选、prompt 管理、secret 解析。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 应把 provider registry、billing price book、request mapper 分成版本化 contract，支持回放历史价格。
   - 算法升级：模型选择不应只看价格；应纳入成功率、延迟、余额、quota、region、合规标签。

## 19. `replicas/`

1. **用途**
   - 本地或 Codespaces replica 启动脚本目录。
2. **关键文件**
   - `replicas/start.sh:25`：启动浏览器 MCP、Docker、对象存储、ClickHouse、Supabase、迁移和 env 准备。
   - `replicas.json`（顶层文件）记录 replica 配置。
3. **入口 / 调用关系**
   - 脚本按顺序安装浏览器依赖、启动 infra、运行数据库迁移、复制 env。证据：`Helicone/helicone@3f4bd44b85f9:replicas/start.sh:1`。
4. **核心 logic / 算法**
   - replica logic 接近 devcontainer，但更偏 agent/browser testing 环境准备。
5. **暴露功能**
   - 开发/agent 环境能快速获得可跑依赖。
6. **HUAKAI 升级点**
   - 运维升级：HUAKAI 可提供 ephemeral preview replica，但必须加 TTL、资源清理和测试数据隔离。

## 20. `scripts/`

1. **用途**
   - 杂项生成和维护脚本，目前重点是 OpenAI 类型抽取和 key population。
2. **关键文件**
   - `scripts/openai-types/README.md:45`：从 OpenAI spec 抽取需要的类型。
   - `scripts/openai-types/extract-openai-types.js:205`：类型依赖抽取脚本。
   - `scripts/openai-types/package.json`：脚本依赖。
   - `scripts/populate-keys/main.py`：key population 工具。
3. **入口 / 调用关系**
   - OpenAI 类型脚本先生成完整 schema，再抽取 chat / responses 请求类型。证据：`Helicone/helicone@3f4bd44b85f9:scripts/openai-types/README.md:5`。
4. **核心 logic / 算法**
   - 类型抽取 logic 是“从大 OpenAPI 产物中选目标类型及依赖”，降低 worker 校验和生成资产体积。
5. **暴露功能**
   - 间接暴露为 OpenAI-compatible 请求校验、responses/chat 兼容和网关 schema 更新。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 的 protocol schema 抽取应记录 upstream spec version、生成时间和 diff summary，防止 silent protocol drift。

## 21. `sdk/`

1. **用途**
   - Python / TypeScript SDK，主要支持 asynchronous logging、helpers、manual log builder、prompt helper。
2. **关键文件**
   - `sdk/python/async/README.md:46`：Python async logging SDK。
   - `sdk/typescript/async/README.md:145`：Node async logging SDK。
   - `sdk/typescript/helpers/package.json:67`：helper package metadata。
   - `sdk/typescript/helpers/index.ts:3`：helper exports。
   - `sdk/typescript/async/index.ts:1`：async package entry。
3. **入口 / 调用关系**
   - SDK README 描述“绕过 proxy 直接记录 traces”的用法。证据：`Helicone/helicone@3f4bd44b85f9:sdk/python/async/README.md:1`、`Helicone/helicone@3f4bd44b85f9:sdk/typescript/async/README.md:1`。
   - helper package 面向 OpenAI 等客户端扩展。证据：`Helicone/helicone@3f4bd44b85f9:sdk/typescript/helpers/package.json:16`。
4. **核心 logic / 算法**
   - SDK logic 是 client-side instrumentation：在应用侧附加观测上下文、custom properties、cache/retry/user 元数据，然后异步提交。
5. **暴露功能**
   - 用户可不用代理也采集 LLM traces；可用 helper 构建日志和 prompt。
6. **HUAKAI 升级点**
   - 生态升级：HUAKAI SDK 应统一 trace schema、重试语义、flush 保证和离线缓冲，不能只封装单 provider。

## 22. `shared/`

1. **用途**
   - worker 与后端共享 proxy header 类型、认证头解析和小工具。
2. **关键文件**
   - `shared/proxy/heliconeHeaders.ts:367`：请求元数据 header 聚合。
   - `shared/proxy/types/heliconeAuth.ts:17`：认证 header 类型。
   - `shared/proxy/types/internalHeaders.ts:29`：内部 header 类型。
   - `shared/utils/getValidUUID.ts:10`：UUID helper。
3. **入口 / 调用关系**
   - worker request wrapper 与后端 proxy 相关路径共享这些 header contract（inferred from shared path + worker imports）。
4. **核心 logic / 算法**
   - 核心是把认证、限流、retry、缓存、prompt、user/session、omit logs、gateway deployment 等请求控制面集中成一份 header contract。证据：`Helicone/helicone@3f4bd44b85f9:shared/proxy/heliconeHeaders.ts:15`。
5. **暴露功能**
   - 用户通过 headers 操作缓存、重试、prompt、session、omit logs、model override、gateway deployment 等功能。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI 应把 header contract 生成为 SDK/docs/tests 三份输出，并对 deprecated header 做兼容窗口和 audit。

## 23. `supabase/`

1. **用途**
   - PostgreSQL / Supabase 配置、Flyway 配置、SQL migrations 和 seed。
2. **关键文件**
   - `supabase/config.toml:74`：本地 Supabase API/db/auth/storage 配置。
   - `supabase/flyway.conf:23`：Flyway migration source 和 naming convention。
   - `supabase/migrations/*.sql`：应用关系型 schema 变更。
   - `supabase/migrations_without_supabase/`：非 Supabase 专用迁移。
   - `supabase/seeds/*.sql`：初始化数据。
3. **入口 / 调用关系**
   - Docker compose 通过 migrations service 挂载 `supabase` 和 `clickhouse`，等数据库健康后运行。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:106`。
   - Flyway 指向两个 migration locations。证据：`Helicone/helicone@3f4bd44b85f9:supabase/flyway.conf:12`。
4. **核心 logic / 算法**
   - 关系库承载 user/org/key/auth/admin 等 transactional 数据；这点在系统概览中被定义为用户、组织和 API key 存储。证据：`Helicone/helicone@3f4bd44b85f9:.cursor/rules/helicone-system-overview.mdc:28`。
5. **暴露功能**
   - 用户登录、组织、API keys、权限、配置、billing/control-plane 元数据依赖该目录迁移。
6. **HUAKAI 升级点**
   - 高风险提醒：数据库 schema 是 HUAKAI high-risk 区域；任何借鉴只能做行为级 parity，不复制 schema。
   - 架构升级：HUAKAI 应把 transactional DB 与 analytics DB 的数据归属写入 DR，避免同一指标双写无 owner。

## 24. `tests/`

1. **用途**
   - Python integration tests 目录，偏 legacy / supplemental 测试。
2. **关键文件**
   - `tests/README.md:26`：Python 测试运行说明。
   - `tests/e2e_suite.py:455`：Python E2E suite。
   - `tests/python_integration_tests.py`：Python integration tests。
   - `tests/requirements.txt`：Python test dependencies。
   - `tests/test_data/pride.txt`、`tests/test_image.png`：测试素材。
3. **入口 / 调用关系**
   - README 要求先启动 workers，再安装 Python 依赖并运行 pytest。证据：`Helicone/helicone@3f4bd44b85f9:tests/README.md:1`。
4. **核心 logic / 算法**
   - 该目录像是非 workspace 测试补充，覆盖 Python 客户端和多媒体/文本 test data。
5. **暴露功能**
   - 对用户无直接功能；对 release 增加跨语言信心。
6. **HUAKAI 升级点**
   - 测试升级：HUAKAI 可把 Python integration tests 与 SDK release gate 绑定，避免 SDK 示例长期漂移。

## 25. `valhalla/`

1. **用途**
   - 后端 API / control plane 主目录；其中 `jawn` 是 Express + TSOA 后端，另含 prompt security service 和 Terraform。
2. **关键文件**
   - `valhalla/README.md:31`：迁移和内存排查 notes。
   - `valhalla/jawn/package.json:106`：后端 scripts 和依赖。
   - `valhalla/jawn/src/index.ts:324`：后端 HTTP/WebSocket 入口。
   - `valhalla/jawn/src/mainLoops.ts:34`：后台 loop 调度。
   - `valhalla/prompt_security/main.py:236`：prompt security 服务。
3. **入口 / 调用关系**
   - 后端启动时加载 tracing/env、注册 proxy router、public/private routes、auth middleware、rate limiter、Swagger、WebSocket upgrade 和 graceful shutdown。证据：`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/src/index.ts:1`、`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/src/index.ts:169`、`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/src/index.ts:279`。
   - 当生产或显式开启 cron 时运行后台 loop。证据：`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/src/index.ts:38`。
4. **核心 logic / 算法**
   - 后端 logic 是 dashboard/control-plane 的业务聚合层：认证、请求查询、统计、缓存、告警、prompt、实验、钱包、score、webhook、provider status 等经理/服务分层（目录观察 + package deps）。
   - 错误处理会把部分数据库资源错误转成 operator 可理解的安全提示。证据：`Helicone/helicone@3f4bd44b85f9:valhalla/jawn/src/index.ts:213`。
5. **暴露功能**
   - Dashboard API、public stats、Swagger、realtime WebSocket、control-plane WebSocket、background experiments。
6. **HUAKAI 升级点**
   - 架构升级：HUAKAI control plane 应拆分 user-facing API、operator API、internal ingest API，避免一个 Express app 承担全部 blast radius。
   - 安全升级：safe error mapping 应集中化并测试，不能每个 controller 自行决定暴露细节。

## 26. `web/`

1. **用途**
   - 用户 dashboard / admin UI，Next.js app，覆盖 requests、sessions、alerts、cache、credits、playground、vault、webhooks、settings 等页面。
2. **关键文件**
   - `web/package.json:175`：dashboard 依赖和 scripts。
   - `web/pages/dashboard.tsx:33`：dashboard page route。
   - `web/pages/requests.tsx:109`：requests page route、分页/sort/query。
   - `web/lib/auth.ts:126`：Better Auth + email verification。
   - `web/filterAST/filterAst.ts:27`：UI 过滤 AST。
3. **入口 / 调用关系**
   - package scripts 提供本地、preview、better-auth、build、lint、test。证据：`Helicone/helicone@3f4bd44b85f9:web/package.json:8`。
   - dashboard 和 requests 页面都包在 auth layout 下，requests 从 query 中提取分页、sort 和 requestId。证据：`Helicone/helicone@3f4bd44b85f9:web/pages/dashboard.tsx:17`、`Helicone/helicone@3f4bd44b85f9:web/pages/requests.tsx:82`。
4. **核心 logic / 算法**
   - web logic 是 auth-gated operations UI：请求列表、详情、筛选、dashboard、billing/credits、prompt、alerts、vault 等。
   - auth 文件显示本地/生产 SMTP 差异、邮箱验证和自定义 session enrichment。证据：`Helicone/helicone@3f4bd44b85f9:web/lib/auth.ts:34`。
5. **暴露功能**
   - 用户能查看日志、成本、sessions、alerts、playground、vault、webhooks、credits、reports 等。
6. **HUAKAI 升级点**
   - UI 升级：HUAKAI Ops UI 应把 request log、account pool、quota、billing、provider health 放到同一 operator cockpit，而不是散落页面。
   - 安全升级：dashboard auth/session enrichment 要有审计事件和 failure visibility。

## 27. `worker/`

1. **用途**
   - Cloudflare Worker gateway/proxy runtime，处理 provider proxy、AI Gateway API、Helicone API、feedback、scheduled tasks、durable objects、KV、queue。
2. **关键文件**
   - `worker/package.json:65`：wrangler/vitest/openapi scripts。
   - `worker/src/index.ts:572`：worker fetch/queue/scheduled 入口。
   - `worker/wrangler.toml:140`：durable objects、KV、queue、container、routes、cron。
   - `worker/src/routers/routerFactory.ts:144`：worker type 到 router 的选择。
   - `worker/src/lib/ai-gateway/ARCHITECTURE.md:171`：AI Gateway flow notes。
3. **入口 / 调用关系**
   - fetch 先包装请求，再根据 host/path/env 选择 worker type 和 router。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:391`。
   - wrangler 配置绑定 rate limiter、wallet、request body buffer、KV、queue、containers 和 cron。证据：`Helicone/helicone@3f4bd44b85f9:worker/wrangler.toml:8`、`Helicone/helicone@3f4bd44b85f9:worker/wrangler.toml:28`、`Helicone/helicone@3f4bd44b85f9:worker/wrangler.toml:108`。
4. **核心 logic / 算法**
   - Host/path classification 将不同子域映射到 provider gateway、OpenAI/Anthropic proxy、customer gateway、internal API。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:78`、`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:106`。
   - AI Gateway flow 是 attempt-based：解析模型请求、扩展 prompt、构造候选、检查 disallow list、按优先级执行并 fallback。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:97`、`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:139`。
   - Scheduled tasks 负责报告、告警、provider/API key sync 等周期操作。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:427`。
5. **暴露功能**
   - 用户可通过统一网关调用多 provider、获得 fallback、observability、cache、rate limit、wallet/credits、feedback、API health。
6. **HUAKAI 升级点**
   - 算法升级：HUAKAI routing 应把候选执行从“优先级顺序”升级为 policy engine：价格、健康、quota、tenant、region、latency、错误预算共同评分。
   - 架构升级：把 host/path classification、provider target resolution、attempt execution、billing escrow 分成可测试 contract。
   - 运维升级：cron/queue failure 需要单独 dead-letter dashboard 和 replay 操作。

## 28. 跨目录 workflow trace

### 28.1 AI Gateway 请求路径

1. Client 通过公开 endpoint 或 provider 子域进 worker；根 README 和 docs 都说明 OpenAI-compatible 单一入口。证据：`Helicone/helicone@3f4bd44b85f9:README.md:42`、`Helicone/helicone@3f4bd44b85f9:docs/gateway/overview.mdx:35`。
2. `worker/` fetch 入口创建请求 wrapper，并根据 host/path/env 选择 runtime type。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:391`、`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:402`。
3. `worker/` router factory 将 runtime type 分派到相应 router，同时加 healthcheck 和 feedback base routes。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/routers/routerFactory.ts:26`、`Helicone/helicone@3f4bd44b85f9:worker/src/routers/routerFactory.ts:75`。
4. `packages/cost/` 提供模型、provider、price、endpoint metadata；worker 依赖它构造候选。证据：`Helicone/helicone@3f4bd44b85f9:packages/cost/README.md:14`、`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:58`。
5. `packages/llm-mapper/` 负责 provider 格式和统一展示/存储格式之间的转换边界。证据：`Helicone/helicone@3f4bd44b85f9:packages/llm-mapper/README.md:14`。
6. `worker/` 执行 attempt，结合 wallet/escrow、cache、provider auth、forwarder 和 metrics；AI Gateway architecture notes 明确请求生命周期。证据：`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:126`。
7. 请求/响应元数据进入 `valhalla/`、`clickhouse/`、`supabase/`、对象存储；系统规则把 metadata、analytics、大 body 分别放到不同存储。证据：`Helicone/helicone@3f4bd44b85f9:.cursor/rules/helicone-system-overview.mdc:70`。
8. `web/` dashboard 从后端/API 查询日志、requests、sessions、成本和告警；requests 页面从 query 恢复分页/排序/requestId。证据：`Helicone/helicone@3f4bd44b85f9:web/pages/requests.tsx:82`。

### 28.2 自托管启动路径

1. Operator 选择 all-in-one 或 docker compose。证据：`Helicone/helicone@3f4bd44b85f9:docker/README.md:3`、`Helicone/helicone@3f4bd44b85f9:docker/README.md:23`。
2. compose 默认启动 PostgreSQL、ClickHouse、MinIO、MailHog、Redis 和迁移服务。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:8`、`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:25`、`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:51`。
3. 迁移服务挂载 `supabase/` 和 `clickhouse/`。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:121`。
4. profile 启动后端和 web，后端依赖数据库、ClickHouse、对象存储、迁移成功。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:129`、`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:168`。
5. worker profile 启动边缘 runtime 的本地容器。证据：`Helicone/helicone@3f4bd44b85f9:docker/docker-compose.yml:280`。

### 28.3 告警 / 心跳路径

1. `helicone-heartbeat/` scheduled handler 每分钟检查后端 health。证据：`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/index.ts:17`。
2. 它读取队列大小，超过阈值后调用后端公开 alert banner API 并发送 Slack。证据：`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/alertSqsCongestion.ts:22`、`Helicone/helicone@3f4bd44b85f9:helicone-heartbeat/src/alertSqsCongestion.ts:56`。
3. `docs/features/alerts.mdx` 描述用户侧告警可基于 error、cost、latency、token、count 等维度配置。证据：`Helicone/helicone@3f4bd44b85f9:docs/features/alerts.mdx:11`。
4. HUAKAI fit：把系统健康告警和租户业务告警分开建模，前者属于 operator SRE，后者属于用户 observability。

## 29. HUAKAI 整体升级 punch list

| ref 项 | HUAKAI 现状 | HUAKAI 升级建议 | 升级维度 | 优先级 |
|---|---|---|---|---|
| AI Gateway attempt-based routing | 未在本轮读取 HUAKAI 代码，现状不作事实断言 | 建立 policy engine：价格、健康、quota、tenant、region、latency、错误预算共同评分，输出可审计 route decision | 算法升级 | P0 |
| BYOK + platform-billed 双模式 | 未读取 HUAKAI 代码 | 将自带 key、平台代付、账号池余额、tenant credit limit 分成独立 contract，不混在 provider adapter | 架构升级 | P0 |
| Worker host/path classification | 未读取 HUAKAI 代码 | 用 declarative route registry 替代硬编码子域分派；每条规则带 owner、test、deprecation | 架构升级 | P0 |
| Wallet / escrow pre-check | 未读取 HUAKAI 代码 | 在 billing ledger 前加 reservation / release / failure reconciliation acceptance tests | 安全/计费升级 | P0 |
| Request/response body object storage | 未读取 HUAKAI 代码 | 大 body 走对象存储，metadata 走 OLTP，analytics 走 OLAP；所有 URL 访问加 redaction 和 expiry | 架构升级 | P0 |
| ClickHouse migration discipline | 未读取 HUAKAI 代码 | 对 OLAP migration 做连续编号、dry-run、dashboard query regression、rollback note | 运维升级 | P1 |
| Supabase/Postgres transactional migration | 未读取 HUAKAI 代码 | DB schema 属高风险，必须 Owner sign-off；每个 migration 带 data compatibility note | 安全升级 | P0 |
| Docs navigation as product map | 未读取 HUAKAI 代码 | docs 信息架构和 feature parity matrix 双向链接，所有 L1/L2 feature 必须有 runbook 或 scenario | 生态升级 | P1 |
| E2E provider matrix | 未读取 HUAKAI 代码 | 按 mock/sandbox/paid 分层，provider matrix 自动生成，不靠手写 list 漂移 | 测试升级 | P0 |
| MCP observability access | 未读取 HUAKAI 代码 | MCP 工具加 tenant scope、audit log、PII redaction、body retrieval permission | 安全/生态升级 | P1 |
| Header contract | 未读取 HUAKAI 代码 | header/schema 单源生成 SDK、docs、tests，并设 deprecated compatibility window | 架构升级 | P0 |
| Heartbeat queue congestion | 未读取 HUAKAI 代码 | 队列 backlog、DLQ、consumer lag、replay 操作统一进 Ops cockpit | 运维升级 | P0 |
| Marketing model browser | 未读取 HUAKAI 代码 | 模型目录接入真实 provider health、价格版本、route availability 和地区合规标签 | 生态升级 | P2 |
| SDK async logging | 未读取 HUAKAI 代码 | SDK flush、retry、offline buffer、trace correlation 做跨语言一致 contract | 生态升级 | P1 |
| Agent/devcontainer automation | 未读取 HUAKAI 代码 | 所有 agent loop 需要 scope、timeout、review gate、DONE proof schema | 安全升级 | P1 |
| Self-host compose profiles | 未读取 HUAKAI 代码 | 提供 gateway-only、ops-lite、full-observability profile，每个 profile 标注资源和安全边界 | 运维升级 | P1 |
| Safe error mapping | 未读取 HUAKAI 代码 | 对 DB/OLAP/provider 错误做集中映射，测试生产环境不泄露内部细节 | 安全升级 | P0 |
| Prompt management through gateway | 未读取 HUAKAI 代码 | prompt version、deployment、rollback、experiment result 应进入同一 audit chain | 生态升级 | P1 |
| Alerts metrics model | 未读取 HUAKAI 代码 | 用户业务告警与平台 SRE 告警分表分权；告警触发、抑制、恢复都可审计 | 运维升级 | P1 |
| Filter expression package | 未读取 HUAKAI 代码 | 过滤 AST 要同时服务 UI、API、SQL/OLAP translator，并防 SQL 注入/高成本查询 | 架构/安全升级 | P0 |

## 30. Open Questions

1. `helicone-cron/` 是否仍为有效运行模块，还是历史残留？本轮只观察到类型文件。
2. `worker/` 中 AI Gateway 的 production 路由是否完全由 wrangler routes 覆盖，还是还依赖外部 DNS / Cloudflare route 配置？
3. `packages/cost/` 的价格数据更新是否有自动校验 against vendor pricing，还是人工维护为主？
4. `valhalla/prompt_security/` 是否在 production 强制启用，还是按 header / org feature flag 启用？
5. `web/` 的 dashboard RBAC 具体粒度需要 T2/T3 阅读后端 managers/controllers 才能确认。
6. `supabase/` migrations 中 billing/wallet/usage 相关 schema 不应在 T1 复制或复述，后续若做 parity 必须 reviewer-lane 单独审。
7. `worker/RequestBodyBufferContainer/` 只做了浅层识别，未拆容器内部 request body buffering 机制。

## 31. Clean-room 风险记录

- 本报告没有复制上游代码块、函数体、schema、SQL、UI source 或测试用例。
- 引用使用 `file:line` 作为证据锚；正文用 HUAKAI vocabulary 描述行为。
- 读到的 README/architecture 文件包含上游示例代码，本报告未复述这些代码。
- Helicone README 声明项目许可证为 Apache v2.0。证据：`Helicone/helicone@3f4bd44b85f9:README.md:147`。
- 对 HUAKAI 的建议是独立设计方向，不表示可复制上游目录、schema 或代码结构。

## 32. 中文 Owner 摘要

本次 codex lane 对 `~/refs/helicone/` 做了 T1 目录骨架拆解，主要真实观察来自根 README/workspace、worker 网关入口、valhalla 后端入口、packages 共享包文档、ClickHouse/Supabase/Docker 配置、web dashboard routes、MCP/SDK/E2E/heartbeat 等 69 个区域；合理推断集中在目录边界、调用关系和 HUAKAI 可升级方向，共 18 条；open questions 有 7 个，主要是 `helicone-cron` 是否仍有效、prompt security 是否强制启用、RBAC 和 request body buffer 机制需要 T2/T3。没有功能缩水：所有观察到的一级目录都保留说明，`.git` 仅作为仓库元数据排除。clean-room 风险可控：未复制代码、schema 或测试，仅行为级中文总结并用 file:line 引证。安全风险主要在后续若借鉴 DB schema、billing/wallet、auth/RBAC、agent loop 时必须走 Owner 确认和独立设计。

---
Agent: codex
Ref: helicone
SHA: 3f4bd44b85f9
Pushed: 2026-05-01
Mining started: 2026-05-13T09:00:52Z
Mining done: 2026-05-13T09:20:22Z
Output LoC: 667
Source files read (per CLAUDE.md #11 closing): README.md; package.json; Dockerfile; supervisord.conf; bifrost/package.json; bifrost/app/page.tsx; bifrost/app/layout.tsx; bifrost/hooks/useModelFiltering.ts; web/package.json; web/pages/dashboard.tsx; web/pages/requests.tsx; web/lib/auth.ts; worker/package.json; worker/src/index.ts; worker/wrangler.toml; worker/src/lib/ai-gateway/ARCHITECTURE.md; worker/src/routers/routerFactory.ts; worker/src/lib/ai-gateway/SimpleAIGateway.ts; worker/src/lib/ai-gateway/AttemptExecutor.ts; valhalla/README.md; valhalla/jawn/README.md; valhalla/jawn/package.json; valhalla/jawn/src/index.ts; valhalla/jawn/src/mainLoops.ts; packages/README.md; packages/cost/README.md; packages/llm-mapper/README.md; docs/docs.json; docs/gateway/overview.mdx; docs/features/alerts.mdx; docs/introduction.mdx; clickhouse/README.md; clickhouse/ch_hcone.py; supabase/config.toml; supabase/flyway.conf; docker/README.md; docker/docker-compose.yml; docker/helicone-compose.sh; examples/ai_gateway/index.ts; examples/cache/index.ts; examples/promptsV3/index.ts; examples/worker-helicone-scores/README.md; sdk/python/async/README.md; sdk/typescript/async/README.md; sdk/typescript/helpers/package.json; sdk/typescript/helpers/index.ts; helicone-heartbeat/package.json; helicone-heartbeat/src/index.ts; helicone-heartbeat/src/AlertManager.ts; helicone-heartbeat/src/alertSqsCongestion.ts; helicone-mcp/README.md; helicone-mcp/package.json; helicone-mcp/src/index.ts; shared/proxy/heliconeHeaders.ts; e2e/README.md; e2e/package.json; e2e/tests/nightly/gateway.test.ts; tests/README.md; cc-agent/README.md; cc-agent/run.sh; scripts/openai-types/README.md; replicas/start.sh; .github/workflows/worker-test.yml; .github/workflows/e2e-test-suite.yml; .github/workflows/clickhouse-migration-check.yml; .devcontainer/devcontainer.json; .devcontainer/.build.sh; .cursor/rules/helicone-system-overview.mdc; .cursor/rules/ai-gateway-docs.mdc; .claude/settings.local.json; .claude/processes/add_new_cost; .husky/pre-push; .vscode/launch.json
Lane: specifier
Agent: GPT-5 Codex / codex lane retry
UTC timestamp: 2026-05-13T09:20:22Z
