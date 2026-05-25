# LiteLLM T1 顶层目录骨架拆解（Codex lane）

| 项 | 值 |
|---|---|
| Ref | litellm |
| Ref path | `~/refs/litellm/` |
| SHA | `b5d3a5fc856e` |
| Pushed | `2026-05-08` |
| Mining started | `2026-05-13T07:52:56Z` |
| Mining mode | T1 顶层目录骨架；只读 LiteLLM ref；行为级 clean-room 总结 |
| Output target | `docs/research/2026-05-13-litellm-dir-skeleton-codex.md` |

## Clean-room 边界

- 本文只读取 `~/refs/litellm/` 这一份 reference project；未读取其他 reference project。
- 本文没有读取 HUAKAI 自己业务实现代码；HUAKAI 升级点是从 LiteLLM 行为证据推导出的产品差距和增强建议。
- 引用采用 `BerriAI/litellm@b5d3a5fc856e:<path>:<line>`；引用是证据锚点，不复刻上游实现。
- 对数据库、函数、类、配置键、测试样例等，本文尽量使用 HUAKAI 语义重述，避免复制上游 distinctive identifier。
- 对二进制产物、目录列表、行数统计，只作为结构证据，不把未读源码推断成行为。

## 顶层目录快照

- 观察到的一层目录：`.circleci/`、`.devcontainer/`、`.github/`、`.semgrep/`、`ci_cd/`、`cookbook/`、`db_scripts/`、`deploy/`、`dist/`、`docker/`、`docs/`、`enterprise/`、`litellm/`、`litellm-js/`、`litellm-proxy-extras/`、`scripts/`、`tests/`、`ui/`、`.git/`。
- 根目录同时放置 Python 包配置、Dockerfile、Makefile、Prisma schema、模型价格/能力数据、provider 端点能力数据和 proxy 示例配置。
- README 把项目定位为统一 LLM API、OpenAI 兼容 Gateway、virtual key、spend、guardrails、load balancing、dashboard 的组合产品面（BerriAI/litellm@b5d3a5fc856e:README.md:50，BerriAI/litellm@b5d3a5fc856e:README.md:83）。
- Python package extras 把 SDK、proxy runtime、extra proxy、enterprise workspace、CLI entry 串在一起（BerriAI/litellm@b5d3a5fc856e:pyproject.toml:39，BerriAI/litellm@b5d3a5fc856e:pyproject.toml:102，BerriAI/litellm@b5d3a5fc856e:pyproject.toml:125，BerriAI/litellm@b5d3a5fc856e:pyproject.toml:219）。
- Dockerfile 说明生产镜像会安装 proxy extras、构建管理 UI、生成 Prisma 客户端并暴露 gateway 端口（BerriAI/litellm@b5d3a5fc856e:Dockerfile:39，BerriAI/litellm@b5d3a5fc856e:Dockerfile:50，BerriAI/litellm@b5d3a5fc856e:Dockerfile:61，BerriAI/litellm@b5d3a5fc856e:Dockerfile:98）。
- Makefile 把 lint、proxy 测试、集成测试和 provider 专项测试拆成不同目标，说明仓库不是单纯 SDK，而是 gateway 级工程（BerriAI/litellm@b5d3a5fc856e:Makefile:29，BerriAI/litellm@b5d3a5fc856e:Makefile:142）。

## 00 根目录 `/`

1. 用途

- 根目录承担产品入口和发布装配：Python 包、proxy runtime、Docker 构建、Prisma 数据访问、模型价格/能力配置、provider 能力矩阵和示例 proxy 配置都在这里汇合。
- README 将它公开描述为 OpenAI 兼容代理与 SDK 两种形态，gateway 侧强调 central service、auth/authz、多租户 spend、guardrails、logging/caching、virtual keys 与 admin UI（BerriAI/litellm@b5d3a5fc856e:README.md:380）。
- 根配置还暴露“SDK 与 Gateway 的边界”：SDK 是应用内库，Gateway 是中央服务，便于 HUAKAI 判断哪些能力应该沉到 Account Hub / Admin Ops。

2. 关键文件

- `README.md`：524 行；产品定位、接口覆盖、gateway vs SDK 说明、生产 gateway 能力清单（BerriAI/litellm@b5d3a5fc856e:README.md:50，BerriAI/litellm@b5d3a5fc856e:README.md:380）。
- `pyproject.toml`：280 行；核心依赖、proxy extras、runtime extras、CLI entry、workspace 成员（BerriAI/litellm@b5d3a5fc856e:pyproject.toml:1，BerriAI/litellm@b5d3a5fc856e:pyproject.toml:125）。
- `Dockerfile`：101 行；多阶段镜像、UI build、Prisma generate、runtime copy（BerriAI/litellm@b5d3a5fc856e:Dockerfile:1，BerriAI/litellm@b5d3a5fc856e:Dockerfile:86）。
- `Makefile`：190 行；本地安装、CI、proxy 测试、import safety 检查（BerriAI/litellm@b5d3a5fc856e:Makefile:54，BerriAI/litellm@b5d3a5fc856e:Makefile:142）。
- `schema.prisma`：1376 行；PostgreSQL datasource 和 Python Prisma client 生成入口（BerriAI/litellm@b5d3a5fc856e:schema.prisma:1）。
- `model_prices_and_context_window.json`、`provider_endpoints_support.json`、`policy_templates.json`：根级数据文件；本轮仅做行数/存在性观察，未逐条读取。

3. 入口

- Python CLI 入口在 package 配置中声明，指向同一 proxy server 入口，说明命令行启动和镜像启动最终汇入同一服务面（BerriAI/litellm@b5d3a5fc856e:pyproject.toml:125）。
- Docker 入口把 runtime 依赖和 Prisma 生成结果放入运行镜像，并通过固定端口对外提供 gateway（BerriAI/litellm@b5d3a5fc856e:Dockerfile:61，BerriAI/litellm@b5d3a5fc856e:Dockerfile:98）。
- Makefile 的 proxy test 目标是开发者进入测试面的主要入口（BerriAI/litellm@b5d3a5fc856e:Makefile:142）。

4. Logic

- 根目录逻辑不是业务算法，而是“产品装配”：统一 SDK facade、gateway server、dashboard、Prisma schema、deployment packaging、provider metadata 被组合为一个发行单元。
- README 的 gateway 表格把 cost tracking、per-project logging/guardrails/caching、virtual key、admin UI 作为 gateway-only 能力，说明上游对“多租户控制面”的定位清晰（BerriAI/litellm@b5d3a5fc856e:README.md:387）。
- Dockerfile 里 runtime extras、UI build、Prisma generate 的顺序显示生产镜像把后端、前端和数据库客户端绑定在同一 deployable 中（BerriAI/litellm@b5d3a5fc856e:Dockerfile:39，BerriAI/litellm@b5d3a5fc856e:Dockerfile:50，BerriAI/litellm@b5d3a5fc856e:Dockerfile:61）。

5. 暴露功能

- SDK/API facade、OpenAI-compatible proxy、provider adapter metadata、Admin dashboard、deployment image、local/CI scripts。
- Gateway 能力公开包括 virtual keys、spend tracking、guardrails、load balancing、admin dashboard、logging/caching、health/load-test 语义（BerriAI/litellm@b5d3a5fc856e:README.md:83，BerriAI/litellm@b5d3a5fc856e:README.md:387）。
- 发布层面支持 pip package、Docker image、workspace package 和 Helm/Kubernetes 目录联动。

6. HUAKAI 升级点

- HUAKAI 应把“SDK facade”和“Gateway 控制面”明确分层，避免所有能力被单体启动路径耦合。
- 根级 provider metadata 可以转成 HUAKAI 的 signed registry / migration-backed catalog，降低手工 JSON 漂移风险。
- 镜像构建中的 UI、Prisma、runtime extras 装配值得借鉴，但 HUAKAI 应把 Admin Ops、Account Hub、Gateway runtime 做 edition-aware package 边界。
- 对根级模型价格和能力数据，HUAKAI 应补“来源、刷新时间、签名、灰度发布、回滚”字段；本轮未读取 HUAKAI 实现，作为 Mandatory Roadmap 候选。

## 01 `.circleci/`

1. 用途

- `.circleci/` 是主 CI 编排目录，负责 Python/Node/Docker/DB 依赖准备、服务等待、测试拆分和企业 workspace 安装。
- 配置使用外部 orb、基础镜像、环境变量、PostgreSQL/Redis service 和 uv 安装逻辑，说明上游在 CI 中模拟 proxy 运行依赖（BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:1，BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:35）。

2. 关键文件

- `.circleci/config.yml`：主配置；包含 DNS 设置、service wait helper、uv 安装校验、local database/cache 启动、enterprise workspace 安装提示（BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:12，BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:46，BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:76）。
- 该目录没有被观察到多文件分拆；T1 视为单配置中心。

3. 入口

- CI 入口由 CircleCI service 读取 `config.yml` 触发。
- 等待服务、安装依赖、启动本地 PostgreSQL/Redis 的 shell 段是测试 job 的前置入口（BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:35，BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:65）。

4. Logic

- CI logic 体现“gateway 测试必须有真实外部依赖替身”：PostgreSQL、Redis、Python runtime、Node/uv 都被预装或启动。
- uv 安装带 checksum 校验，说明供应链基础工具不是裸下载后立即信任（BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:46）。
- enterprise workspace 安装出现在 CI 依赖路径中，说明专有 extension 与主包至少在测试环境有关联（BerriAI/litellm@b5d3a5fc856e:.circleci/config.yml:76）。

5. 暴露功能

- 对外不暴露产品 API，但暴露工程能力：可重复测试环境、DB/cache 服务、依赖安装、CI job 基线。
- 对维护者暴露“proxy 不是纯 unit-test 项目”的事实：测试需要真实服务协作。

6. HUAKAI 升级点

- HUAKAI 应建立 Gateway/Account Hub/Admin Ops 的 CI matrix：PostgreSQL、Redis、object storage mock、provider mock、billing ledger dry-run 分开跑。
- CI 应在 money-path、auth-path、quota-path 上强制迁移和回滚检查；LiteLLM 已有 DB/cache CI 影子，HUAKAI 需要更细的 slice gate。
- uv/tool checksum 思路可保留，但 HUAKAI 还应固定 actions、container digest 和 SBOM 产物。

## 02 `.devcontainer/`

1. 用途

- `.devcontainer/` 提供开发容器定义，降低本地启动 proxy/dashboard/test 依赖差异。
- 该配置使用 Python 3.11 开发镜像，加 Node、Docker-in-Docker、VS Code 扩展、端口转发和 post-create setup（BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:1，BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:8，BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:40）。

2. 关键文件

- `.devcontainer/devcontainer.json`：57 行；定义容器 image、features、extensions、forward port、debug env 和 setup command（BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:2，BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:25）。

3. 入口

- 开发者通过 VS Code / Dev Containers 打开 repo 自动进入。
- `postCreateCommand` 是安装/准备依赖的入口（BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:47）。
- 端口 4000 被显式转发，和 proxy runtime 暴露端口一致（BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:40）。

4. Logic

- 开发容器把 Python、Node、Docker、debug 配置放在一个可重建环境中。
- 它服务于全栈 gateway 开发：后端、UI、Docker 构建都可在同一 dev shell 中完成。
- 配置没有体现生产安全边界；它是开发便利层，不应被 HUAKAI 当作部署基线。

5. 暴露功能

- 本地开发入口、调试端口、编辑器插件、容器内 Docker build 支持。
- 通过环境变量开启调试行为，方便本地 proxy trace（BerriAI/litellm@b5d3a5fc856e:.devcontainer/devcontainer.json:42）。

6. HUAKAI 升级点

- HUAKAI 可建立 devcontainer，但应区分 local fake provider、local DB seed、local billing sandbox。
- 对 Gateway + Account Hub，devcontainer 应内置最小 PostgreSQL migration check 和 audit log viewer。
- 调试 env 应保证不会进入生产镜像；建议 CI 检查 dev-only env 与 deployment templates 的隔离。

## 03 `.github/`

1. 用途

- `.github/` 存放 GitHub Actions、依赖更新策略和项目协作配置。
- 观察到 PR workflow 针对 proxy auth/key management 变更触发专项测试，也有手动 mock test workflow（BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-unit-proxy-auth.yml:1，BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-litellm.yml:1）。

2. 关键文件

- `.github/workflows/test-unit-proxy-auth.yml`：27 行；PR 触发、权限收敛、并发取消、调用共享测试模板并指定测试路径（BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-unit-proxy-auth.yml:3，BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-unit-proxy-auth.yml:16）。
- `.github/workflows/test-litellm.yml`：45 行；手动触发 mock tests，安装 uv 后执行 pytest（BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-litellm.yml:16，BerriAI/litellm@b5d3a5fc856e:.github/workflows/test-litellm.yml:35）。
- `.github/dependabot.yaml`：13 行；GitHub Actions 依赖每日更新、冷却和分组（BerriAI/litellm@b5d3a5fc856e:.github/dependabot.yaml:1）。

3. 入口

- PR 入口：指定路径变更触发 proxy auth/key management 测试。
- Manual dispatch 入口：维护者手动跑 full mock tests。
- Dependabot 入口：每日扫描 action 依赖。

4. Logic

- GitHub workflow 把 auth/key 管理归为高风险区域，独立测试。
- 手动 mock workflow 显示全套 mock tests 可能成本较高或不适合所有 PR 自动跑。
- Dependabot 只观察到 actions 更新，不代表 Python/Node deps 全由 Dependabot 管理。

5. 暴露功能

- 项目治理能力：PR test routing、manual regression、dependency update hygiene。
- 对外 API 不在此目录暴露，但工程风险控制入口在这里。

6. HUAKAI 升级点

- HUAKAI 应对 auth、quota、billing ledger、provider routing、schema migration 建立 path-based PR gates。
- Money-path workflow 应高于普通 mock tests，包含 migration dry-run、ledger invariant、quota bypass negative tests。
- Dependabot 需要覆盖 GitHub Actions、Python、Node、container base image，并生成 license/security delta 报告。

## 04 `.semgrep/`

1. 用途

- `.semgrep/` 存放自定义静态规则，覆盖资源滥用和供应链/敏感目录风险。
- README 说明规则按语言和类别组织，并提供运行方式（BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/README.md:1，BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/README.md:13）。

2. 关键文件

- `.semgrep/rules/README.md`：22 行；目录结构、运行命令、CI integration 提示（BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/README.md:5）。
- `.semgrep/rules/python/unbounded-memory.yml`：14 行；对无界内存队列模式设高严重级别，关联资源耗尽风险（BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/python/unbounded-memory.yml:1）。
- `.semgrep/rules/security/no-claude-directory.yml`：18 行；阻止特定 agent 目录进入代码库，原因指向供应链/秘密泄露风险（BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/security/no-claude-directory.yml:1，BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/security/no-claude-directory.yml:13）。

3. 入口

- 入口是 Semgrep CLI 或 CI integration，README 给出针对整个规则目录和指定规则的运行方式（BerriAI/litellm@b5d3a5fc856e:.semgrep/rules/README.md:13）。

4. Logic

- 规则不是业务逻辑，而是变更前安全筛选。
- 资源耗尽规则说明上游关注 async queue / producer-consumer 设计的无界增长。
- 敏感目录规则说明上游将 AI-agent 本地配置视为不能进入产品仓库的供应链风险。

5. 暴露功能

- 暴露给维护者的是静态风险闸门：resource abuse、supply-chain hygiene、CI-friendly custom scans。

6. HUAKAI 升级点

- HUAKAI 应加入 auth bypass、billing double-write、quota zero-cost bypass、tenant scope leakage、secret in log 的 Semgrep/AST 规则。
- 对 `.agents/`、本地 prompt、connector token、test credentials 需要建立 HUAKAI 自己的 allow/deny policy。
- 资源耗尽检查应覆盖 streaming buffer、usage aggregation queue、audit event queue、provider retry fan-out。

## 05 `ci_cd/`

1. 用途

- `ci_cd/` 是迁移和测试 credential hygiene 的工程脚本目录。
- 主要观察点是 DB migration safety：脚本识别 destructive SQL、检查 migration freshness，并要求人类显式允许高风险迁移继续（BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:13，BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:149）。

2. 关键文件

- `ci_cd/run_migration.py`：220 行；destructive pattern、migration freshness、schema diff、migration create、AI assistant 禁止自动重跑破坏性操作提示（BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:24，BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:175）。
- `ci_cd/TEST_KEY_PATTERNS.md`：40 行；说明 test/mock key pattern，目标是降低 secret scanning 误报和真实凭据混入风险（BerriAI/litellm@b5d3a5fc856e:ci_cd/TEST_KEY_PATTERNS.md:1）。

3. 入口

- Migration script 从 CLI 参数进入，支持 schema path、migrations dir、output name、allow destructive flag 等（BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:198）。
- Test key pattern 文档给 CI/开发者提供约定入口。

4. Logic

- 脚本先检查 migration 是否落后，再生成新 migration，随后扫描生成 SQL 中的 destructive patterns。
- 如果命中高风险 pattern，默认拒绝继续，并明确要求人类手动 rerun 带 allow flag；这对 AI agent 自动执行尤其重要（BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:149，BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:175）。
- 该目录将 DB schema 风险视为工程一等公民，而不是事后 review。

5. 暴露功能

- 暴露 migration guard、schema freshness check、destructive SQL blocker、test key convention。
- 不暴露 runtime API，但强约束发布和 schema 演进。

6. HUAKAI 升级点

- HUAKAI 的数据库 schema、billing ledger、quota enforcement 都属于高风险区，应直接采用“AI 不可自动放行破坏性迁移”的 hard gate。
- Migration gate 应额外生成 tenant-data blast radius、rollback plan、ledger invariant diff。
- Test key convention 应进入 secret scanner allowlist，并禁止任何真实 provider key 进入 fixtures。

## 06 `cookbook/`

1. 用途

- `cookbook/` 是示例、demo、load test、mock server 和使用配方目录。
- 它不是核心实现，但暴露上游鼓励的使用方式：proxy 请求、fallback、logging、spend、semantic cache、streaming、router pressure、guardrail mock（BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_proxy_server/readme.md:14）。

2. 关键文件

- `cookbook/litellm_proxy_server/readme.md`：177 行；早期 proxy 能力说明，覆盖 OpenAI-style input/output、fallback、logging、spend、cache、streaming（BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_proxy_server/readme.md:32，BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_proxy_server/readme.md:35）。
- `cookbook/litellm_router/load_test_router.py`：143 行；用多个 deployment 组成 router 并发请求样本，记录成功失败和耗时（BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_router/load_test_router.py:17，BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_router/load_test_router.py:108）。
- `cookbook/mock_guardrail_server/mock_bedrock_guardrail_server.py`：mock guardrail server；用 FastAPI 模拟 guardrail 服务请求/响应和鉴权失败路径（BerriAI/litellm@b5d3a5fc856e:cookbook/mock_guardrail_server/mock_bedrock_guardrail_server.py:1，BerriAI/litellm@b5d3a5fc856e:cookbook/mock_guardrail_server/mock_bedrock_guardrail_server.py:151）。

3. 入口

- 文档入口是不同 cookbook 子目录 README。
- Router load test 入口是 Python script；读取 env credential 并发调用 router。
- Mock guardrail 入口是 FastAPI app，本地监听端口供测试或手动验证。

4. Logic

- Cookbook 把“如何使用”转成可运行样本：不是抽象文档，而是能够制造并发、失败、guardrail 响应的环境。
- Router 样本用多个 deployment 映射到同一 logical model，再通过并发请求观察分流与失败统计（BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_router/load_test_router.py:17，BerriAI/litellm@b5d3a5fc856e:cookbook/litellm_router/load_test_router.py:115）。
- Guardrail mock 样本覆盖 token 缺失、格式错误、无效 token 和内容策略响应，说明 guardrail 集成需要可离线验证（BerriAI/litellm@b5d3a5fc856e:cookbook/mock_guardrail_server/mock_bedrock_guardrail_server.py:163）。

5. 暴露功能

- Provider proxy 使用方式、router pressure test、guardrail test double、部署说明、旧版 roadmap hints。
- 对 HUAKAI 重要的是“operator recipes”：怎么验证 fallback、cache、guardrail、spend tracking 是否真的工作。

6. HUAKAI 升级点

- HUAKAI 应把 cookbook 变成 `docs/runbooks/` + `tests/scenarios/` 双轨：可读配方和可执行验收测试不混淆。
- 对 fallback、provider outage、quota exhaustion、budget stop、guardrail block、stream abort 应提供 mock server + scenario tests。
- Cookbook 中的旧 demo key/env 样式要避免进入 HUAKAI；示例凭据必须走统一 fake-secret pattern。

## 07 `db_scripts/`

1. 用途

- `db_scripts/` 是数据库辅助脚本目录，主要服务 proxy admin/spend dashboard 所需聚合视图和旧 key 数据迁移。
- 视图脚本明确用于在运行时之外预创建管理和 spend 相关 helper views（BerriAI/litellm@b5d3a5fc856e:db_scripts/create_views.py:18）。

2. 关键文件

- `db_scripts/create_views.py`：209 行；连接 PostgreSQL，创建多类 spend/token/team 聚合视图；本报告不复刻具体 view/table 名（BerriAI/litellm@b5d3a5fc856e:db_scripts/create_views.py:18，BerriAI/litellm@b5d3a5fc856e:db_scripts/create_views.py:53）。
- `db_scripts/migrate_keys.py`：187 行；从 CSV 迁移 legacy key 数据到数据库；包含本地 DB URL 示例，应视为 demo-only（BerriAI/litellm@b5d3a5fc856e:db_scripts/migrate_keys.py:1，BerriAI/litellm@b5d3a5fc856e:db_scripts/migrate_keys.py:10）。

3. 入口

- `create_views.py` 通过直接运行脚本进入，读取数据库连接环境并执行 DDL helper。
- `migrate_keys.py` 以 CSV 文件作为输入，构造数据库记录。

4. Logic

- Spend dashboard 依赖数据库侧聚合，避免每次 UI 请求都重新扫描原始 usage 数据。
- Legacy key migration 表明上游考虑从外部/旧系统导入 token、team、budget、model scope 等控制面数据。
- 脚本里出现本地连接示例，HUAKAI 需要将此类内容严格限定为非生产 fixture。

5. 暴露功能

- 管理端 spend 汇总加速、token/team 维度辅助查询、legacy key import。
- 这些功能服务 Account Hub / Admin Ops，不直接处理 provider 请求。

6. HUAKAI 升级点

- HUAKAI 应把聚合视图纳入正式 migration，而不是单独脚本漂移；视图版本要能回滚。
- Legacy key import 应是审计化 job：dry-run、校验、冲突报告、Owner approval、导入后 disable rollback path。
- Spend 聚合应区分账单事实表和 dashboard projection，避免管理 UI 视图影响 ledger 真相。

## 08 `deploy/`

1. 用途

- `deploy/` 提供 Helm、Kubernetes、Azure Resource Manager 等部署资产。
- Helm chart 声明 app version、PostgreSQL/Redis dependency，values 覆盖 replica、image、service、security context、env injection、probe、ingress、master key secret 和 proxy config（BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/Chart.yaml:1，BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:1）。

2. 关键文件

- `deploy/charts/litellm-helm/Chart.yaml`：41 行；Helm metadata 和 optional database/cache dependencies（BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/Chart.yaml:29）。
- `deploy/charts/litellm-helm/values.yaml`：180 行；runtime values、secrets/configmap、probes、resources、sample models（BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:9，BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:60，BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:82）。
- `deploy/kubernetes/kub.yaml`：56 行；简化 deployment/service/config volume/probe 示例（BerriAI/litellm@b5d3a5fc856e:deploy/kubernetes/kub.yaml:1，BerriAI/litellm@b5d3a5fc856e:deploy/kubernetes/kub.yaml:36）。
- `deploy/azure_resource_manager/main.bicep`：42 行；ACI template，暴露 container image、port、DNS、resource 参数（BerriAI/litellm@b5d3a5fc856e:deploy/azure_resource_manager/main.bicep:1）。

3. 入口

- Helm chart 是生产化 Kubernetes 入口。
- `kub.yaml` 是快速 Kubernetes demo 入口。
- Bicep 文件是 Azure container instance 快速部署入口。

4. Logic

- 部署资产将 proxy config 与 secret/env 分开注入；Helm values 支持已有 secret 和 config map（BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:60）。
- Probe 和 service port 与 runtime gateway 端口一致，体现部署健康检查基础面（BerriAI/litellm@b5d3a5fc856e:deploy/charts/litellm-helm/values.yaml:82）。
- Demo manifest 出现样例 env/credentials 字段，HUAKAI 不能把这类示例当生产默认。

5. 暴露功能

- Helm install、Kubernetes deployment、ACI deployment、secret/config injection、liveness/readiness/startup probe、sample proxy model config。

6. HUAKAI 升级点

- HUAKAI deployment 应把 Gateway、Account Hub、Admin Ops、worker、migration job 拆成独立 workload。
- Helm values 必须把 real secrets 全部走 external secret/provider integration；示例值只允许 fake-secret。
- Probe 应覆盖 provider route table loaded、DB reachable、cache reachable、billing writer healthy、quota gate healthy，而不只是进程 alive。

## 09 `dist/`

1. 用途

- `dist/` 是构建产物目录，本轮观察到 `litellm-1.79.1.tar.gz`，文件大小只有 64 bytes；未解压、未读取二进制内容。
- 该目录不是行为源码证据，T1 仅记录它作为 release artifact 占位或构建输出位置。

2. 关键文件

- `dist/litellm-1.79.1.tar.gz`：二进制/归档产物；本轮未读取源码内容，不能从中推出行为。

3. 入口

- 入口通常来自 build backend 或 release pipeline；本轮未读取 pipeline 中对 `dist/` 的写入逻辑。

4. Logic

- 目录 logic 是 artifact staging，而不是 runtime path。
- 文件大小异常小，可能是占位/损坏/测试产物；本轮不推断。

5. 暴露功能

- 发布打包痕迹；没有直接暴露 runtime API。

6. HUAKAI 升级点

- HUAKAI release artifact 应有 checksum、SBOM、provenance、license inventory、signature。
- 若保留 `dist/`，应避免把临时/损坏 artifact 纳入源码树。
- Clean-room 风险：不需要读取归档内部内容；HUAKAI 不应从 reference package 复制任何实现。

## 10 `docker/`

1. 用途

- `docker/` 存放生产/非 root 镜像辅助文件和 entrypoint。
- 这里体现 Admin UI 定制构建、runtime migration、ddtrace wrapper、non-root container hardening（BerriAI/litellm@b5d3a5fc856e:docker/build_admin_ui.sh:1，BerriAI/litellm@b5d3a5fc856e:docker/Dockerfile.non_root:1）。

2. 关键文件

- `docker/build_admin_ui.sh`：73 行；根据 enterprise UI branding 文件是否存在决定是否构建自定义 UI，并执行 npm build（BerriAI/litellm@b5d3a5fc856e:docker/build_admin_ui.sh:1，BerriAI/litellm@b5d3a5fc856e:docker/build_admin_ui.sh:24）。
- `docker/prod_entrypoint.sh`：8 行；可选 trace wrapper 后执行传入命令（BerriAI/litellm@b5d3a5fc856e:docker/prod_entrypoint.sh:1）。
- `docker/entrypoint.sh`：16 行；启动前执行 Prisma migration 再启动命令（BerriAI/litellm@b5d3a5fc856e:docker/entrypoint.sh:1，BerriAI/litellm@b5d3a5fc856e:docker/entrypoint.sh:10）。
- `docker/Dockerfile.non_root`：145 行；非 root runtime、Prisma cache、UI copy、权限收敛、固定用户运行（BerriAI/litellm@b5d3a5fc856e:docker/Dockerfile.non_root:86，BerriAI/litellm@b5d3a5fc856e:docker/Dockerfile.non_root:133）。

3. 入口

- `prod_entrypoint.sh` 是生产镜像启动入口之一。
- `entrypoint.sh` 是带 migration-before-start 的入口。
- `build_admin_ui.sh` 是镜像 build 阶段定制 UI 入口。

4. Logic

- Docker 层把 UI branding、runtime tracing、DB migration、non-root hardening 合并到 image lifecycle。
- Migration-before-start 简化部署，但对多副本环境可能产生竞态，需要外部锁或 migration job；本轮只观察到 entrypoint 行为，不评价其生产完整性（BerriAI/litellm@b5d3a5fc856e:docker/entrypoint.sh:10）。
- Non-root Dockerfile 显式调整 ownership/permission 并切换用户，说明上游考虑容器权限面（BerriAI/litellm@b5d3a5fc856e:docker/Dockerfile.non_root:129）。

5. 暴露功能

- Production entrypoint、trace wrapper、startup migration、custom admin UI build、non-root image。

6. HUAKAI 升级点

- HUAKAI 应把 migration 从 app startup 中拆成 release job，配锁、dry-run 和 Owner approval。
- Non-root、read-only filesystem、dropped capabilities、seccomp profile 应是 production default。
- Admin UI branding 应走 edition/plugin 构建，不应在 Docker build 时隐式读取 enterprise 文件改变行为。

## 11 `docs/`

1. 用途

- `docs/` 在该 ref 中不是主文档站根，而包含少量 provider 文档和图片资产。
- 观察到 provider 文档页面解释某 provider 的 proxy/SDK 使用方式、custom API base 和 credential env（BerriAI/litellm@b5d3a5fc856e:docs/my-website/docs/providers/crusoe.md:1）。

2. 关键文件

- `docs/my-website/docs/providers/crusoe.md`：160 行；provider overview、supported operation、SDK/proxy examples、custom endpoint 设置（BerriAI/litellm@b5d3a5fc856e:docs/my-website/docs/providers/crusoe.md:1，BerriAI/litellm@b5d3a5fc856e:docs/my-website/docs/providers/crusoe.md:39）。
- `docs/images/local-testing/hosted-vllm-custom-tool-local-test.png`：图片资产；本轮未读取图片内容。

3. 入口

- 文档站生成器或 docs site 引用该 markdown。
- Provider docs 是用户配置某 provider 的入口之一。

4. Logic

- Docs 将 provider 接入抽象为：安装/环境、SDK 调用、proxy config、custom API endpoint。
- 这与 `litellm/llms/` provider adapter 和根级 provider ability metadata 形成文档-实现-配置三角。

5. 暴露功能

- Provider-specific setup、proxy route examples、SDK examples、custom API base setup。

6. HUAKAI 升级点

- HUAKAI provider docs 应从 provider registry 生成，避免文档和实际 adapter 能力漂移。
- Provider onboarding 文档应包含安全字段：credential scope、secret storage、rate limits、fallback eligibility、cost source。
- 对每个 provider 应有 acceptance fixture：docs 示例能在 mock/sandbox 环境验证。

## 12 `enterprise/`

1. 用途

- `enterprise/` 是专有扩展 package 目录，包含 enterprise proxy routes、premium-gated docs/auth settings、audit logging endpoint、enterprise hooks/callbacks、UI branding。
- Package metadata 声明独立 enterprise package 和 proprietary license，本项目 clean-room 下只能提取行为，不复制实现（BerriAI/litellm@b5d3a5fc856e:enterprise/pyproject.toml:1，BerriAI/litellm@b5d3a5fc856e:enterprise/pyproject.toml:10）。

2. 关键文件

- `enterprise/pyproject.toml`：33 行；独立 enterprise package，workspace version sync（BerriAI/litellm@b5d3a5fc856e:enterprise/pyproject.toml:1，BerriAI/litellm@b5d3a5fc856e:enterprise/pyproject.toml:19）。
- `enterprise/litellm_enterprise/proxy/proxy_server.py`：34 行；premium gated custom docs/auth settings（BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/proxy_server.py:1）。
- `enterprise/litellm_enterprise/proxy/enterprise_routes.py`：29 行；enterprise router 组合 audit/email/management/static blocking routes（BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/enterprise_routes.py:1）。
- `enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py`：210 行；audit list/get，带 pagination/filter/sort 和 DB dependency（BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:1，BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:84）。
- `enterprise/enterprise_hooks/banned_keywords.py`、`enterprise/litellm_enterprise/enterprise_callbacks/secret_detection.py`、`enterprise/litellm_enterprise/enterprise_callbacks/callback_controls.py`：enterprise guardrail/callback control 样本（BerriAI/litellm@b5d3a5fc856e:enterprise/enterprise_hooks/banned_keywords.py:1，BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/enterprise_callbacks/secret_detection.py:1）。

3. 入口

- Enterprise package entry 由 workspace install / runtime imports 接入主 proxy。
- Enterprise routes 被主服务 include 后暴露 audit、email event、management 等能力。
- Enterprise UI branding 通过 Docker build helper 进入 Admin UI build（BerriAI/litellm@b5d3a5fc856e:enterprise/enterprise_ui/README.md:1，BerriAI/litellm@b5d3a5fc856e:docker/build_admin_ui.sh:24）。

4. Logic

- Enterprise 采用扩展包 + runtime gating 方式，不是完全独立服务。
- Audit endpoint 按 actor/action/object/date 等条件过滤并支持排序分页；本文不复刻参数名细节，只记录行为（BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:84，BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:116）。
- Callback controls 允许请求层面关闭部分 callbacks，但 gated by admin/premium checks；这类能力对安全和合规有风险，需要 HUAKAI 重新设计（BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/enterprise_callbacks/callback_controls.py:1）。

5. 暴露功能

- Audit log query、enterprise route bundle、premium docs/auth settings、banned term guardrail、secret detection guardrail、dynamic callback control、custom UI branding。

6. HUAKAI 升级点

- HUAKAI 应用 MIT clean-room 方式重建 enterprise-like 功能：audit log、SIEM export、policy guardrails、branding、SSO/SCIM，而不是复制 proprietary extension。
- Callback disable 这类能力必须以 policy exception / audit trail / break-glass workflow 呈现，不应成为普通 request toggle。
- Audit log 应成为 core Account Hub 能力，不只 enterprise add-on；查询性能与 retention policy 要在设计中明确。

## 13 `litellm/`

1. 用途

- `litellm/` 是核心 Python package：SDK facade、provider adapters、proxy server、auth、router、caching、guardrails、policy engine、logging integrations、types/utils。
- 根 package 暴露 SDK-style completion/embedding/responses 等 API，同时 proxy 子包暴露 FastAPI gateway（BerriAI/litellm@b5d3a5fc856e:litellm/__init__.py:1270，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:1040）。

2. 关键文件

- `litellm/__init__.py`：2219 行；全局配置、feature flags、proxy mode、budget/cache/guardrail/prompt/global settings、public API exports（BerriAI/litellm@b5d3a5fc856e:litellm/__init__.py:212，BerriAI/litellm@b5d3a5fc856e:litellm/__init__.py:270）。
- `litellm/main.py`：7853 行；chat/embedding 等 SDK facade，参数兼容多 provider 和 OpenAI-style 调用（BerriAI/litellm@b5d3a5fc856e:litellm/main.py:386，BerriAI/litellm@b5d3a5fc856e:litellm/main.py:4565）。
- `litellm/responses/main.py`：Responses API facade，输入、tools、reasoning、stream、provider override 等（BerriAI/litellm@b5d3a5fc856e:litellm/responses/main.py:416）。
- `litellm/router.py`：10853 行；deployment list、cache、retry/fallback、pre-call checks、tag routing、routing strategy、budget limiter、health、affinity（BerriAI/litellm@b5d3a5fc856e:litellm/router.py:234，BerriAI/litellm@b5d3a5fc856e:litellm/router.py:837）。
- `litellm/proxy/proxy_server.py`：15143 行；FastAPI app、startup lifecycle、auth/cache/router/management routers、request middleware（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:210，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:724）。
- `litellm/proxy/auth/user_api_key_auth.py`、`litellm/proxy/auth/auth_checks.py`：credential normalization、JWT-to-virtual-key mapping、model/budget/end-user checks（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:437，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/auth_checks.py:1）。
- `litellm/caching/caching.py`、`litellm/proxy/caching_routes.py`、`litellm/proxy/common_utils/cache_coordinator.py`：cache backend abstraction、cache admin routes、cache-aside coordination（BerriAI/litellm@b5d3a5fc856e:litellm/caching/caching.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/common_utils/cache_coordinator.py:1）。
- `litellm/proxy/guardrails/guardrail_registry.py`、`litellm/proxy/policy_engine/architecture.md`：guardrail registry、policy scoping/inheritance（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/guardrails/guardrail_registry.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/policy_engine/architecture.md:1）。

3. 入口

- SDK 入口：package root re-export 和 `main.py` facade。
- Gateway 入口：proxy server FastAPI app 和 CLI entry。
- Provider adapter 入口：`llms/` 下各 provider transformation/handler；README 说明各 provider folder 承担 request/response translation（BerriAI/litellm@b5d3a5fc856e:litellm/llms/README.md:1）。
- Router 入口：deployment selection、fallback、pre-call checks 和 strategy selection。

4. Logic

- SDK facade 把 OpenAI-style 参数映射到不同 provider 调用，同时保留 provider credential/base/version 等 override（BerriAI/litellm@b5d3a5fc856e:litellm/main.py:386，BerriAI/litellm@b5d3a5fc856e:litellm/main.py:1067）。
- Provider adapter 基类定义 request/response processing、client creation、environment validation 的抽象形状；具体 provider handler 做 sync/async HTTP、stream wrapper、error mapping（BerriAI/litellm@b5d3a5fc856e:litellm/llms/base.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/llms/anthropic/chat/handler.py:1）。
- Proxy startup 读取 config、加载 DB/cache、初始化 budget/global spend、JWT auth、scheduled jobs、health checks、adaptive router flusher、trace/profiling/shared HTTP client（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:724，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:858）。
- Auth 逻辑支持多种 header/query credential 来源和 pass-through auth，随后映射 virtual key、检查 model/budget/user constraints（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:437，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:550，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/auth_checks.py:1）。
- Router 逻辑可按 least-busy、usage、latency、cost、tag、budget、adaptive 等策略选择 deployment，并维护 cooldown、health、fallback、provider budget limiter（BerriAI/litellm@b5d3a5fc856e:litellm/router.py:497，BerriAI/litellm@b5d3a5fc856e:litellm/router.py:837）。
- Adaptive router README 描述按请求类型学习质量/成本 tradeoff、post-call signal、DB flush 和限制；本文只记录行为形状，不复制具体常量/字段（BerriAI/litellm@b5d3a5fc856e:litellm/router_strategy/adaptive_router/README.md:1，BerriAI/litellm@b5d3a5fc856e:litellm/router_strategy/adaptive_router/README.md:64）。
- Logging manager 把成功/失败、cost、tokens、redaction、observability callbacks 统一处理；Prometheus integration 记录请求、latency、token、spend、budget 等维度（BerriAI/litellm@b5d3a5fc856e:litellm/litellm_core_utils/litellm_logging.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/integrations/prometheus.py:1）。
- Guardrails registry 结合固定集成与目录扫描，policy engine 说明用 scope rules 将 policy 绑定到 team/key/model 并解析继承（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/guardrails/guardrail_registry.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/policy_engine/architecture.md:12）。

5. 暴露功能

- SDK：chat、embedding、responses、image/audio/video/rerank/batch 等统一 facade。
- Gateway：OpenAI-compatible endpoints、provider pass-through、virtual key auth、JWT mapping、budget/model/end-user checks、management routers、guardrails、policy engine、cache admin、health、analytics、routing/fallback。
- Router：multi-deployment routing、fallback/retry、cooldown, provider budgets, health aware selection, adaptive selection。
- Cache：local/Redis/semantic/object store/dual cache, cache admin delete/ping/info/flush（BerriAI/litellm@b5d3a5fc856e:litellm/caching/Readme.md:1，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/caching_routes.py:1）。
- Observability：callbacks, Prometheus, third-party logs, spend/token metrics, callback manager。

6. HUAKAI 升级点

- HUAKAI 应拆出清晰 runtime domains：Gateway request path、Account Hub identity/key/budget path、Admin Ops config/audit path、Billing/usage writer path。
- Routing 必须成为 policy-driven engine：provider budget、tenant quota、feature flag、compliance region、cost ceiling、latency SLO、quality feedback 分层组合，而不是单一 strategy 参数。
- Auth pass-through 默认行为需要 HUAKAI 重新审查；真实生产应最小授权、tenant-scoped、audited exceptions。
- Adaptive router 是重要升级方向，但 HUAKAI 需要可解释 state、offline eval、rollback、per-tenant isolation、防反馈投毒。
- Cache coordinator 思路可借鉴；HUAKAI 应给 global spend/quota config 加 single-flight 与 stale-while-revalidate，避免热点 key stampede。
- Guardrails/policy 应成为 first-class contract：scope resolution、versioning、dry-run、explainability、failure mode 都要可审计。

## 14 `litellm-js/`

1. 用途

- `litellm-js/` 是 JavaScript/TypeScript sidecar/demo 目录，包含 worker-style proxy demo 和 spend log batching service。
- 它不是主 runtime，但展示前端/边缘环境或 sidecar 对 proxy/spend 的访问方式（BerriAI/litellm@b5d3a5fc856e:litellm-js/proxy/src/index.ts:1，BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/src/index.ts:1）。

2. 关键文件

- `litellm-js/proxy/README.md`：8 行；npm dev/deploy commands（BerriAI/litellm@b5d3a5fc856e:litellm-js/proxy/README.md:1）。
- `litellm-js/proxy/src/index.ts`：59 行；Hono worker proxy demo，使用 OpenAI-compatible client 转发到 proxy，并暴露 chat completion route（BerriAI/litellm@b5d3a5fc856e:litellm-js/proxy/src/index.ts:1，BerriAI/litellm@b5d3a5fc856e:litellm-js/proxy/src/index.ts:18）。
- `litellm-js/spend-logs/README.md`：8 行；npm dev/open localhost commands（BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/README.md:1）。
- `litellm-js/spend-logs/src/index.ts`：84 行；Hono Node service，Prisma client、in-memory spend log queue、periodic flush、spend update endpoint（BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/src/index.ts:1，BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/src/index.ts:20）。

3. 入口

- Proxy demo 入口是 npm dev / deploy。
- Spend log service 入口是 Hono server，HTTP endpoint 接收 spend updates 并定时写入 DB。

4. Logic

- Worker demo 表达“轻量 OpenAI-compatible relay”模式，但含 demo bearer value；HUAKAI 只能把它视为 prototype 证据。
- Spend log sidecar 使用内存 buffer + interval flush，说明上游探索过把 usage write 从 request path 拆出为异步批处理（BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/src/index.ts:20，BerriAI/litellm@b5d3a5fc856e:litellm-js/spend-logs/src/index.ts:42）。

5. 暴露功能

- Edge/worker proxy prototype、OpenAI-compatible relay、spend update batching prototype。

6. HUAKAI 升级点

- HUAKAI 可把 JS edge proxy 做成 optional plugin，但真实 auth、quota、billing 不应在 edge demo 中简化。
- Spend batching 必须生产化：durable queue、idempotency key、at-least-once semantics、dead-letter、tenant ledger reconciliation。
- Demo hardcoded key pattern 必须禁止进入 HUAKAI runtime；只能保留 fake fixture 并被 scanner 识别。

## 15 `litellm-proxy-extras/`

1. 用途

- `litellm-proxy-extras/` 是 proxy 附加包，主要承载 Prisma migration 文件和 migration helper，以减轻主 package 体积。
- README 明确该包放置 proxy 额外文件，目前包括 migration SQL，并提供安装/迁移命令（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/README.md:1）。

2. 关键文件

- `litellm-proxy-extras/README.md`：20 行；说明用途、安装和 migrate 命令（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/README.md:1，BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/README.md:14）。
- `litellm-proxy-extras/pyproject.toml`：33 行；MIT package、version、workspace sync（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/pyproject.toml:1，BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/pyproject.toml:19）。
- `litellm-proxy-extras/litellm_proxy_extras/utils.py`：220 行；offline Prisma env、migration timestamp sorting、migration dir override/copy、baseline migration create/list/resolve（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/litellm_proxy_extras/utils.py:21，BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/litellm_proxy_extras/utils.py:80）。
- `litellm-proxy-extras/litellm_proxy_extras/migrations/`：大量 migration 目录；本轮未逐个读取 SQL。

3. 入口

- Package install 后通过 migrate command 或 helper functions 使用 migration bundle。
- Dockerfile 安装 proxy extras，并在 build/runtime 生成 Prisma client（BerriAI/litellm@b5d3a5fc856e:Dockerfile:39，BerriAI/litellm@b5d3a5fc856e:Dockerfile:61）。

4. Logic

- 附加包把重 migration assets 从主 package 分离，利于发行体积和 runtime dependency 管理。
- Helper 会复制/覆盖 migration dir 并处理 baseline/list/resolve；这表明 migration bundle 被当作可编程资产，而非静态目录（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/litellm_proxy_extras/utils.py:80，BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/litellm_proxy_extras/utils.py:109）。
- 观察到 migration 覆盖 spend aggregates、managed resources、policy/versioning、project/org/account resources、agent/memory/workflow 类资源；未读取 SQL，不能断言字段级行为。

5. 暴露功能

- Migration asset package、offline Prisma env setup、migration dir management、baseline migration handling。

6. HUAKAI 升级点

- HUAKAI 应把 migrations 作为 core release artifact，配 schema digest、migration manifest、rollback manifest、tenant-safe verification。
- 附加 migration 包可以借鉴，但数据库 schema 不能由 runtime 隐式复制覆盖；应由 release controller 管理。
- Migration scope 需要和 feature parity map 绑定：每个新增 resource 有 spec、tests、ops UI 和 rollback。

## 16 `scripts/`

1. 用途

- `scripts/` 是操作验证、health check、benchmark、adaptive router demo/verification 等脚本目录。
- 它把 gateway 行为转成 operator 可运行检查：模型健康、proxy vs provider performance、adaptive router convergence/sticky/latency、demo dashboard（BerriAI/litellm@b5d3a5fc856e:scripts/health_check/health_check_client.py:3，BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:1）。

2. 关键文件

- `scripts/health_check/health_check_client.py`：health check client；从 YAML 或 proxy API 获取模型，对 chat/embedding 并发发起大输入测试，支持 custom auth header（BerriAI/litellm@b5d3a5fc856e:scripts/health_check/health_check_client.py:3，BerriAI/litellm@b5d3a5fc856e:scripts/health_check/health_check_client.py:76）。
- `scripts/verify_adaptive_router.py`：端到端验证 adaptive router；训练、等待 flush、收敛、sticky session、latency、最终判定（BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:1，BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:137）。
- `scripts/adaptive_router_demo/README.md`：live demo；dashboard、chat、synthetic traffic、state polling、cost meter、troubleshooting（BerriAI/litellm@b5d3a5fc856e:scripts/adaptive_router_demo/README.md:20，BerriAI/litellm@b5d3a5fc856e:scripts/adaptive_router_demo/README.md:88）。
- `scripts/benchmark_proxy_vs_provider.py`：proxy vs direct provider benchmark，输出 success/error、latency、throughput、variance 等（BerriAI/litellm@b5d3a5fc856e:scripts/benchmark_proxy_vs_provider.py:1，BerriAI/litellm@b5d3a5fc856e:scripts/benchmark_proxy_vs_provider.py:51）。

3. 入口

- Health check：CLI 参数 + YAML/proxy API。
- Adaptive verify：proxy URL/key/router env 变量 + Python script。
- Demo：static HTML + traffic generator + proxy endpoint。
- Benchmark：env variables + CLI flags。

4. Logic

- Health check 用高长度输入测试模型可用性，并支持 embedding/chat 区分和 custom auth header；这是生产 sentinel 风格（BerriAI/litellm@b5d3a5fc856e:scripts/health_check/health_check_client.py:24，BerriAI/litellm@b5d3a5fc856e:scripts/health_check/health_check_client.py:211）。
- Adaptive verify 分阶段检查：先训练信号，再等待队列 flush，再冷启动收敛，再 sticky session，再测 p50 roundtrip（BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:137，BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:171）。
- Benchmark 关注 proxy overhead 相对 direct provider 的成功率、延迟分布、吞吐（BerriAI/litellm@b5d3a5fc856e:scripts/benchmark_proxy_vs_provider.py:51，BerriAI/litellm@b5d3a5fc856e:scripts/benchmark_proxy_vs_provider.py:95）。

5. 暴露功能

- Operator health checks、performance benchmark、adaptive router validation、demo dashboard、synthetic traffic driver。

6. HUAKAI 升级点

- HUAKAI 应把 scripts 升级为 supported ops commands：`doctor`、`provider-check`、`route-eval`、`quota-drill`、`billing-reconcile`、`audit-export-check`。
- Adaptive router verify 的 convergence/sticky/latency 思路应转成 acceptance tests，覆盖 feedback poisoning、tenant isolation 和 rollback。
- Health checks 应区分 live/readiness/synthetic transaction，并纳入 Admin Ops UI。

## 17 `tests/`

1. 用途

- `tests/` 是单元、集成、mock、security、router、guardrails、proxy、load、coverage consistency 的大测试目录。
- README 声称总测试量超过 1000，并开始按 `tests/test_litellm` 映射核心 package 行为；该目录只能跑 mock tests（BerriAI/litellm@b5d3a5fc856e:tests/README.MD:1，BerriAI/litellm@b5d3a5fc856e:tests/README.MD:7）。

2. 关键文件

- `tests/README.MD`：9 行；测试规模和 mock-test mapping 说明。
- `tests/router_unit_tests/README.md`：5 行；router 测试命名约束，说明 router coverage 被单独治理（BerriAI/litellm@b5d3a5fc856e:tests/router_unit_tests/README.md:1）。
- `tests/code_coverage_tests/prevent_key_leaks_in_exceptions.py`：156 行；AST/regex 扫描避免把 raw args 类内容泄漏到异常文本（BerriAI/litellm@b5d3a5fc856e:tests/code_coverage_tests/prevent_key_leaks_in_exceptions.py:25，BerriAI/litellm@b5d3a5fc856e:tests/code_coverage_tests/prevent_key_leaks_in_exceptions.py:137）。
- `tests/code_coverage_tests/check_provider_folders_documented.py`：provider folder 与 provider endpoint documentation 的一致性检查（BerriAI/litellm@b5d3a5fc856e:tests/code_coverage_tests/check_provider_folders_documented.py:1，BerriAI/litellm@b5d3a5fc856e:tests/code_coverage_tests/check_provider_folders_documented.py:138）。
- `tests/proxy_security_tests/test_master_key_not_in_db.py`：security regression，确保 startup/health path 不把 master credential 写入 DB（BerriAI/litellm@b5d3a5fc856e:tests/proxy_security_tests/test_master_key_not_in_db.py:30，BerriAI/litellm@b5d3a5fc856e:tests/proxy_security_tests/test_master_key_not_in_db.py:54）。
- `tests/guardrails_tests/test_guardrails_config.py`：guardrail config tests，覆盖 logging-only masking、event hook selection、guardrail info response 等行为（BerriAI/litellm@b5d3a5fc856e:tests/guardrails_tests/test_guardrails_config.py:49，BerriAI/litellm@b5d3a5fc856e:tests/guardrails_tests/test_guardrails_config.py:77）。

3. 入口

- Makefile/CI 进入不同测试子集。
- `tests/test_litellm/` 对应 core package mock tests。
- `tests/proxy_unit_tests/` 覆盖 proxy config/auth/spend/cache/routes/JWT/migration/schema/guardrails 等大量场景；本轮只目录列表观察，未逐个读取。

4. Logic

- 测试目录不仅验证 happy path，还包括 provider documentation drift、secret leakage in exceptions、startup credential persistence、guardrails behavior。
- Provider folder documentation check 防止新增 provider adapter 后忘记能力矩阵，这是 feature parity/ops docs 绑定的一个可借鉴模式。
- Security test 明确把 master credential 不落 DB 作为 invariant，而不是依赖代码 review。

5. 暴露功能

- Regression coverage、security invariants、router coverage、guardrail scenarios、proxy config fixtures、load tests、provider capability drift checks。

6. HUAKAI 升级点

- HUAKAI 应按 Owner 的 acceptance-test-writer 路径，把真实生产场景转成 AT IDs：auth bypass、quota exhaustion、billing ledger idempotency、provider failover、guardrail block、cache stampede、audit retention。
- Coverage consistency 应扩展到 provider registry、pricing registry、Admin Ops UI route coverage、API docs schema coverage。
- Security tests 必须包含 “master/root credential never stored/logged/returned”、tenant boundary、negative cache、custom header spoofing、pass-through auth exception。

## 18 `ui/`

1. 用途

- `ui/` 是 Admin dashboard 前端目录，基于 Next/React，覆盖 virtual keys、models、admin settings、guardrails monitor、cost tracking 等操作界面。
- Package 配置显示 Next、React、query/table/UI/test/e2e 依赖，并有 lint/test/e2e/format scripts（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/package.json:5，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/package.json:22）。

2. 关键文件

- `ui/litellm-dashboard/package.json`：98 行；Next app scripts、React deps、table/query/UI libs、Playwright/Vitest/Prettier/TypeScript（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/package.json:5，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/package.json:51）。
- `ui/litellm-dashboard/src/app/(dashboard)/layout.tsx`：105 行；dashboard shell、Navbar、Sidebar、auth hook、base path normalization、legacy/query route bridge（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/app/(dashboard)/layout.tsx:37，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/app/(dashboard)/layout.tsx:66）。
- `ui/litellm-dashboard/src/app/(dashboard)/networking.ts`：17 行；team fetch 按 user role 决定 scope（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/app/(dashboard)/networking.ts:3）。
- `ui/litellm-dashboard/src/components/AdminPanel.tsx`：387 行；admin settings、SSO/IP allowlist/UI access/logging/vault/SCIM 等操作面（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/AdminPanel.tsx:37，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/AdminPanel.tsx:186）。
- `ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx`：902 行；virtual key table、pagination/sorting/filtering/status/details（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:55，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:133）。
- `ui/litellm-dashboard/src/components/add_model/add_model_tab.tsx`：98 行；添加模型和自动路由器两个 tab（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/add_model/add_model_tab.tsx:60）。
- `ui/litellm-dashboard/src/components/GuardrailsMonitor/GuardrailsMonitorView.tsx`：74 行；date range、overview/detail switch（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/GuardrailsMonitor/GuardrailsMonitorView.tsx:20）。
- `ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx`：cost discount/margin/pricing calculator UI，admin-only mutations（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx:22，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx:41）。

3. 入口

- Next dashboard app 是 UI 入口。
- Dashboard layout 通过 auth hook、navbar、sidebar 和 path/query route bridge 加载不同页面。
- AdminPanel、VirtualKeysTable、AddModelTab、GuardrailsMonitor、CostTrackingSettings 是关键操作入口。

4. Logic

- UI 使用 role-aware fetching：非 admin fetch teams 时附带 user scope，admin viewer/admin 不附带 user id（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/app/(dashboard)/networking.ts:9）。
- Virtual keys table 支持 server pagination/sort、client filter、status badge、details drawer，并把 blocked/scim blocked 状态显示给 operator（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:84，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:187）。
- Add model UI 把普通 model add 和 auto router add 并列为两个 tab，说明 routing config 是 first-class admin action（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/add_model/add_model_tab.tsx:62）。
- Guardrails monitor 提供 7 天默认窗口、overview/detail drilldown（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/GuardrailsMonitor/GuardrailsMonitorView.tsx:16）。
- Cost tracking settings 管理 provider discount/margin，并包含 pricing calculator；admin-only mutation gate 在 UI 层可见（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx:41，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx:60）。

5. 暴露功能

- Admin dashboard shell、virtual key ops、team/org scoped fetching、model/auto-router add、SSO/security/IP/UI access settings、SCIM/vault/logging settings、guardrails monitor、cost discount/margin/pricing calculator。

6. HUAKAI 升级点

- HUAKAI Admin Ops 应把 high-risk operations 做成 explicit approval flows：IP allowlist、SSO, key block/unblock, model route change, cost margin, callback disable。
- UI 权限不能只依赖前端 role gate；后端 contract 与 audit trail 必须同步。
- Guardrails monitor 应支持 explain, sample redaction, false positive feedback, version diff。
- Cost tracking UI 应接入 billing ledger truth，而不是仅编辑 provider discount/margin；每次价格变更需要 effective date 和回放影响预估。

## 19 `.git/`

1. 用途

- `.git/` 是本地 VCS 元数据目录，不是产品源码或 feature surface。
- 本轮只通过 git 命令读取当前 HEAD SHA 与最近提交日期，用于报告元数据。

2. 关键文件

- 未读取 `.git/` 内部文件。
- 记录元数据：SHA `b5d3a5fc856e`，Pushed/last commit date `2026-05-08`。

3. 入口

- Git CLI 是唯一入口。

4. Logic

- 不从 `.git/` 推导产品行为。
- 不读取历史实现，不做 commit archaeology。

5. 暴露功能

- 仅暴露版本定位能力。

6. HUAKAI 升级点

- HUAKAI research artifact 应固定 source SHA、date、agent、source files read，便于 stale citation 策略执行。
- 未来复核 LiteLLM 时应重新 fetch HEAD 并验证 SHA reachability。

## 跨目录 Workflow Trace

### A. Runtime request path：client -> proxy -> auth -> router -> provider -> logging/spend

1. Client 以 OpenAI-compatible request 进入 proxy；README 将 `/v1` style endpoints 和 gateway 能力作为公共产品面（BerriAI/litellm@b5d3a5fc856e:README.md:83）。
2. FastAPI app 在 proxy server 中组装，并 include 多类 routers；startup 负责 config、DB/cache、budget、JWT、health/adaptive/background jobs（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:724，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:14855）。
3. Auth 层从多种 header/query source 归一化 credential，并可把 JWT claim 映射为 virtual key，再进入 model/budget/end-user checks（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:437，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:550）。
4. Router 根据 deployment list、strategy、fallback、cooldown、health、provider budget 等选择目标 provider deployment（BerriAI/litellm@b5d3a5fc856e:litellm/router.py:234，BerriAI/litellm@b5d3a5fc856e:litellm/router.py:837）。
5. Provider adapter 把统一参数转成 provider request 并处理 sync/async/stream/error mapping（BerriAI/litellm@b5d3a5fc856e:litellm/llms/base.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/llms/anthropic/chat/handler.py:1）。
6. Logging/callback layer 计算/转发 usage、cost、success/failure、metrics，Prometheus 类集成暴露 request/latency/token/spend/budget 维度（BerriAI/litellm@b5d3a5fc856e:litellm/litellm_core_utils/litellm_logging.py:1，BerriAI/litellm@b5d3a5fc856e:litellm/integrations/prometheus.py:1）。
7. HUAKAI 升级结论：把这条 path 拆成明确 contract：Ingress/Auth、Policy/Quota、Route Selection、Provider Adapter、Usage Writer、Observation Sink；每段都有独立验收和故障降级。

### B. Admin config path：UI -> management endpoints -> DB/cache -> runtime reload

1. UI dashboard 提供 key table、model add/auto-router、admin settings、guardrails monitor、cost settings（BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:55，BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/add_model/add_model_tab.tsx:60）。
2. Proxy server include management/config/guardrail/cache 等 routers；startup 也同步 UI settings 与 feature flags（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:210，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:724）。
3. Cache coordinator 对 global spend/config/feature flag 等热点数据做 cache-aside coordination，减少并发加载风暴（BerriAI/litellm@b5d3a5fc856e:litellm/proxy/common_utils/cache_coordinator.py:1）。
4. DB schema 使用 PostgreSQL datasource 和 Prisma Python client；schema 具体表字段本轮不复刻（BerriAI/litellm@b5d3a5fc856e:schema.prisma:1）。
5. HUAKAI 升级结论：Admin Ops 写操作需要 explicit change request、diff preview、approval、audit、runtime propagation status，而不是直接 form submit 后生效。

### C. Migration/release path：pyproject/Dockerfile -> proxy extras -> migrations -> container startup

1. Root package 把 proxy extras 纳入依赖组合，Dockerfile 安装 extras 并生成 Prisma client（BerriAI/litellm@b5d3a5fc856e:pyproject.toml:67，BerriAI/litellm@b5d3a5fc856e:Dockerfile:39，BerriAI/litellm@b5d3a5fc856e:Dockerfile:61）。
2. Proxy extras package 承载 migration SQL 和 helper，提供 migration copy/list/baseline 行为（BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/README.md:1，BerriAI/litellm@b5d3a5fc856e:litellm-proxy-extras/litellm_proxy_extras/utils.py:80）。
3. Docker entrypoint 可在应用启动前执行 migration（BerriAI/litellm@b5d3a5fc856e:docker/entrypoint.sh:10）。
4. 独立 CI migration script 对 destructive change 做 refusal，要求人类显式 allow（BerriAI/litellm@b5d3a5fc856e:ci_cd/run_migration.py:149）。
5. HUAKAI 升级结论：release controller 应统一执行 migration，而不是 app replica 启动时竞争执行；destructive migration 必须 Owner sign-off。

### D. Adaptive router path：config -> router strategy -> feedback -> DB/state -> UI/demo/verification

1. Router 支持 adaptive strategy，并在 startup 启动相关 flusher/health background path（BerriAI/litellm@b5d3a5fc856e:litellm/router.py:837，BerriAI/litellm@b5d3a5fc856e:litellm/proxy/proxy_server.py:858）。
2. Adaptive README 描述按 request category 学习质量/成本 score，并把 post-call feedback flush 到数据库；也列出限制（BerriAI/litellm@b5d3a5fc856e:litellm/router_strategy/adaptive_router/README.md:1，BerriAI/litellm@b5d3a5fc856e:litellm/router_strategy/adaptive_router/README.md:64）。
3. Demo README 暴露 state polling、bandit bars、cost meter、synthetic traffic、troubleshooting（BerriAI/litellm@b5d3a5fc856e:scripts/adaptive_router_demo/README.md:20，BerriAI/litellm@b5d3a5fc856e:scripts/adaptive_router_demo/README.md:88）。
4. Verify script 检查 training、convergence、sticky session 和 latency verdict（BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:137，BerriAI/litellm@b5d3a5fc856e:scripts/verify_adaptive_router.py:171）。
5. HUAKAI 升级结论：adaptive routing 必须产品化为可解释、可禁用、可回滚、可离线评估的 feature flag；每个 tenant 的 feedback state 隔离。

## HUAKAI 升级 Punch List

| ID | LiteLLM 观察到的能力/结构 | HUAKAI 升级建议 | 状态建议 | 风险 |
|---|---|---|---|---|
| P-001 | README 将 Gateway 与 SDK 能力明确区分（README.md:380） | HUAKAI 也应分层 Gateway runtime、Account Hub、Admin Ops、SDK facade | Mandatory Roadmap | 架构边界不清会导致控制面耦合 |
| P-002 | Proxy startup 负责 config/DB/cache/JWT/budget/background jobs（proxy_server.py:724） | 拆出 lifecycle supervisor 和 health dependency graph | Implemented Better | 单体 startup 难以诊断 |
| P-003 | Auth 支持多 header/query source 与 pass-through（user_api_key_auth.py:437） | 默认最小授权；pass-through 只做 audited exception | Safe Equivalent | Header spoofing / tenant leakage |
| P-004 | JWT 可映射到 virtual key（user_api_key_auth.py:550） | 设计 Account Hub identity-to-key binding，带 negative cache 和 revocation | Implemented Better | 身份吊销延迟 |
| P-005 | Router 支持多策略/fallback/cooldown/provider budget（router.py:234） | 做 policy-composed route engine：cost、quota、region、SLO、compliance | Implemented Better | 策略冲突不可解释 |
| P-006 | Adaptive router 有 feedback/state/demo/verify（adaptive README, scripts） | Feature-flagged adaptive routing，tenant-isolated state，offline eval | Feature Flag | Feedback poisoning / unfair cost |
| P-007 | Cache coordinator 防热点加载风暴（cache_coordinator.py:1） | 给 spend/quota/config 增加 single-flight 与 stale-while-revalidate | Implemented Better | Cache stampede 影响配额 |
| P-008 | Cache admin route 支持 ping/delete/info/flush（caching_routes.py:1） | Admin Ops cache 操作需要 role gate、dry-run、audit、scope limit | Safe Equivalent | 误 flush 影响生产 |
| P-009 | Guardrail registry + policy engine scope/inheritance（guardrail_registry.py, architecture.md） | Guardrail policy versioning、dry-run、explain、rollback | Implemented Better | 误拦截和绕过 |
| P-010 | Enterprise audit query endpoint（audit_logging_endpoints.py:84） | Audit log 进 core；查询、导出、retention、tamper evidence | Implemented Better | 合规不可追溯 |
| P-011 | Cost tracking UI 可设置 provider discount/margin（cost_tracking_settings.tsx:22） | 价格变更加入 effective date、impact preview、ledger reconciliation | Implemented Better | 账单回放错误 |
| P-012 | DB helper views 支撑 spend dashboard（create_views.py:18） | 把 projections 纳入 migration manifest，不影响 ledger truth | Safe Equivalent | 视图漂移/性能问题 |
| P-013 | Migration script 拒绝 destructive SQL 并要求人类 allow（run_migration.py:149） | HUAKAI 强制 Owner gate + rollback plan + data blast radius | Implemented Better | 数据破坏 |
| P-014 | Docker entrypoint 可启动前 migrate（entrypoint.sh:10） | 改为独立 migration job / release controller | Safe Equivalent | 多副本竞态 |
| P-015 | Helm values 注入 secrets/config/probes（values.yaml:60） | External secret + edition-aware workloads + richer readiness | Implemented Better | secret 泄露/假健康 |
| P-016 | UI virtual key table 显示 status、scope、details（VirtualKeysTable.tsx:55） | Account Hub key lifecycle：rotate, revoke, reason, owner, audit | Implemented Better | key 操作不可追踪 |
| P-017 | AdminPanel 管理 SSO/IP/UI access/logging/vault/SCIM（AdminPanel.tsx:37） | 所有安全设置改 change request + approval + rollback | Implemented Better | 前端误操作直达生产 |
| P-018 | Provider folder documentation drift test（check_provider_folders_documented.py:1） | Provider registry/docs/tests 三方一致性 gate | Implemented Better | provider 能力漂移 |
| P-019 | Secret leak regression 测异常内容（prevent_key_leaks_in_exceptions.py:25） | 扩展到 logs, traces, audit payload, UI errors, webhook payload | Implemented Better | 凭据泄露 |
| P-020 | Master credential not persisted regression（test_master_key_not_in_db.py:30） | Root credential never stored/logged/returned invariant | Implemented Better | 根密钥泄露 |
| P-021 | Health check client 做 synthetic model checks（health_check_client.py:3） | Admin Ops synthetic transactions + status page + alert routing | Implemented Better | Provider 假可用 |
| P-022 | Benchmark proxy vs provider 性能差异（benchmark_proxy_vs_provider.py:1） | 发布 gate 加 latency overhead budget 和 p95/p99 regression | Mandatory Roadmap | 网关引入不可见延迟 |
| P-023 | JS spend sidecar 使用内存队列批量写（spend-logs index.ts:20） | 改 durable usage queue + idempotent ledger writer + DLQ | Implemented Better | 用量丢失/重复计费 |
| P-024 | Enterprise callback control 可动态禁用 callback（callback_controls.py:1） | 改 break-glass policy，强审计，默认禁用普通请求绕开 | Safe Equivalent | 观测/合规被绕过 |
| P-025 | Devcontainer 提供全栈开发环境（devcontainer.json:1） | HUAKAI 本地环境内置 fake provider、seed data、scenario runner | Plugin | 开发环境不一致 |
| P-026 | Semgrep 防无界内存/敏感目录（.semgrep rules） | 增加 HUAKAI money/auth/quota/logging 专用静态规则 | Mandatory Roadmap | 回归靠人工发现 |
| P-027 | Proxy extras 拆出 migration assets（README.md:1） | 迁移资产独立但由 release controller 管理，不由 runtime 覆盖 | Safe Equivalent | schema drift |
| P-028 | Guardrails monitor UI 支持时间窗口与详情（GuardrailsMonitorView.tsx:20） | 增加 false-positive feedback、policy version diff、sample redaction | Implemented Better | Operator 无法调优 |

## Open Questions / 后续深挖点

- 本轮 T1 没有逐个读取 `litellm/llms/` provider 子目录；provider adapter parity 需要单独 T2/T3 深挖。
- 本轮没有读取 migration SQL 内容；数据库 schema/ledger/account/quota 需要专门 reviewer-lane 与 clean-room specifier-lane 分离。
- 本轮没有运行测试，也没有验证部署 chart 是否可安装。
- 本轮没有读取 HUAKAI 自己实现；punch list 的状态建议是从产品目标与 LiteLLM 证据推导，需 Owner/PM lane 对照 HUAKAI 当前实现确认。
- 本轮没有展开 UI 全路由；Admin Ops UI parity 需要另开 frontend-ops-ui-review。

## Truth-first 结论

- 真实观察：根配置、CI、devcontainer、GitHub workflow、Semgrep、migration scripts、cookbook、DB scripts、deploy、docker、docs provider page、enterprise extension、core package/proxy/router/auth/cache/guardrails/logging、JS demos、proxy extras、scripts、tests、UI 关键组件。
- 合理推断：HUAKAI 升级点、cross-directory workflow、release/migration 风险、Admin Ops 产品化建议；这些均基于已读 LiteLLM 区域，不声称 HUAKAI 当前已经或尚未实现。
- Open questions：5 个，主要集中在 provider adapter、migration SQL、HUAKAI 对照、UI 全路由和部署可运行性。

---
Agent: codex
Ref: litellm
SHA: b5d3a5fc856e
Pushed: 2026-05-08
Mining started: 2026-05-13T07:52:56Z
Mining done: 2026-05-13T08:20:55Z
Output LoC: 845
Source files read (per CLAUDE.md #11 closing): README.md; pyproject.toml; Dockerfile; Makefile; schema.prisma; package.json; .circleci/config.yml; .devcontainer/devcontainer.json; .github/workflows/test-litellm.yml; .github/workflows/test-unit-proxy-auth.yml; .github/dependabot.yaml; .semgrep/rules/README.md; .semgrep/rules/python/unbounded-memory.yml; .semgrep/rules/security/no-claude-directory.yml; ci_cd/run_migration.py; ci_cd/TEST_KEY_PATTERNS.md; db_scripts/create_views.py; db_scripts/migrate_keys.py; deploy/charts/litellm-helm/Chart.yaml; deploy/charts/litellm-helm/values.yaml; deploy/kubernetes/kub.yaml; deploy/azure_resource_manager/main.bicep; docker/build_admin_ui.sh; docker/prod_entrypoint.sh; docker/entrypoint.sh; docker/Dockerfile.non_root; docs/my-website/docs/providers/crusoe.md; enterprise/pyproject.toml; enterprise/litellm_enterprise/proxy/proxy_server.py; enterprise/litellm_enterprise/proxy/enterprise_routes.py; enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py; enterprise/enterprise_hooks/banned_keywords.py; enterprise/litellm_enterprise/enterprise_callbacks/secret_detection.py; enterprise/litellm_enterprise/enterprise_callbacks/callback_controls.py; enterprise/enterprise_ui/README.md; litellm/__init__.py; litellm/main.py; litellm/responses/main.py; litellm/router.py; litellm/llms/README.md; litellm/llms/base.py; litellm/llms/anthropic/chat/handler.py; litellm/llms/openai/openai.py; litellm/proxy/proxy_server.py; litellm/proxy/auth/user_api_key_auth.py; litellm/proxy/auth/auth_checks.py; litellm/router_strategy/adaptive_router/README.md; litellm/caching/Readme.md; litellm/caching/caching.py; litellm/proxy/caching_routes.py; litellm/proxy/common_utils/cache_coordinator.py; litellm/integrations/Readme.md; litellm/litellm_core_utils/litellm_logging.py; litellm/litellm_core_utils/logging_callback_manager.py; litellm/integrations/prometheus.py; litellm/proxy/guardrails/guardrail_registry.py; litellm/proxy/guardrails/init_guardrails.py; litellm/proxy/policy_engine/architecture.md; litellm/proxy/policy_engine/policy_endpoints.py; litellm-js/proxy/README.md; litellm-js/proxy/src/index.ts; litellm-js/spend-logs/README.md; litellm-js/spend-logs/src/index.ts; litellm-proxy-extras/README.md; litellm-proxy-extras/pyproject.toml; litellm-proxy-extras/litellm_proxy_extras/utils.py; ui/litellm-dashboard/package.json; ui/litellm-dashboard/src/app/(dashboard)/layout.tsx; ui/litellm-dashboard/src/app/(dashboard)/networking.ts; ui/litellm-dashboard/src/components/AdminPanel.tsx; ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx; ui/litellm-dashboard/src/components/add_model/add_model_tab.tsx; ui/litellm-dashboard/src/components/GuardrailsMonitor/GuardrailsMonitorView.tsx; ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx; tests/README.MD; tests/code_coverage_tests/prevent_key_leaks_in_exceptions.py; tests/code_coverage_tests/check_provider_folders_documented.py; tests/proxy_security_tests/test_master_key_not_in_db.py; tests/router_unit_tests/README.md; tests/guardrails_tests/test_guardrails_config.py; cookbook/litellm_proxy_server/readme.md; cookbook/litellm_router/load_test_router.py; cookbook/mock_guardrail_server/mock_bedrock_guardrail_server.py; scripts/health_check/health_check_client.py; scripts/verify_adaptive_router.py; scripts/adaptive_router_demo/README.md; scripts/benchmark_proxy_vs_provider.py
