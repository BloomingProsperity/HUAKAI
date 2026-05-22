# 2026-05-22 HUAKAI work-tree 结构审计（Codex 独立版）

## 审计边界

- 本报告只审计目录布局、模块组织、命名与工具结构，不做代码缺陷复查。
- 按 Owner 约束未运行 `git`，未读取 `docs/process/research/2026-05-22-worktree-audit-claude.md`。
- 文件数量以工作树扫描为准：`rg --files --hidden -g '!.git/**'` 用于接近“源文件/未忽略文件”的统计；另用 `find` 观察物理工作树噪音。因为未运行 `git`，本文不声称这些数字等于 tracked 文件数。

## 总体证据快照

源文件视角（`rg --files --hidden -g '!.git/**'`）共 3335 个文件，主要分布：

| 区域 | 文件数 | 观察 |
| --- | ---: | --- |
| `exploratory/` | 1694 | 绝大多数是 `rust-core-gateway/`，其中 `vendor/` 1561 个文件，`merged/` 89 个文件。 |
| `backend/` | 816 | Go 后端主体，含 `cmd/`、`internal/`、`sql/`、`pkg/`、Makefile、Docker 与 sqlc 配置。 |
| `docs/` | 628 | 规格、流程、研究、OpenAPI、schema、runbook 均在这里，但 research/plans 有多套入口。 |
| `frontend/` | 48 | Next.js 前端 wedge，规模明显小于后端。 |
| `tools/` | 44 | `fingerprint-collector` 和 `upstream-policy-monitor`。 |
| `reference_deep_dive/` | 41 | 参考研究资料仍在顶层，而非 `docs/` 下。 |
| `docs_zh/` | 13 | 中文规则说明镜像。 |

物理工作树视角（排除 `.git`）有 112694 个文件；大量是被 `.gitignore` 标明不应提交的缓存/构建产物：`huakai-rust-target/` 约 16G、`cargo-targets/` 约 5.8G、`tmp/` 约 4.5G、`backend/.cache/` 约 1.5G、`.cache/` 约 618M、`.gocache/` 约 571M、`frontend/node_modules/` 约 514M、`frontend/.next/` 约 177M。这个数字不代表源码规模，但会影响 agent 扫描、备份和人工定位。

## 优点

1. 顶层主分区总体合理。`backend/`、`frontend/`、`docs/`、`exploratory/`、`tools/` 的职责大体可读：生产 Go 后端、前端 wedge、规范流程文档、实验 Rust 网关与辅助工具分开存放。源文件统计也显示主体集中在这几个区域，而不是散落在根目录。

2. `backend/` 使用了常见 Go 服务骨架。`backend/go.mod`、`backend/Makefile`、`backend/Dockerfile`、`backend/docker-compose.dev.yml`、`backend/sqlc.yaml`、`backend/cmd/`、`backend/internal/`、`backend/sql/` 同处一个后端模块下，开发、构建、迁移、测试入口集中。

3. `backend/cmd/` 边界基本健康。当前可见入口包括 `cmd/gateway`、`cmd/openapi-check`、`cmd/huakai-verify`、`cmd/smoke-setup`，主服务、契约校验、验证工具、环境初始化工具没有混在 `internal/` 包里。

4. `backend/internal/` 已经按业务域拆包，而不是一个单体包。直接子包包括：`admin`、`adminhttp`、`audit`、`auditledger`、`auth`、`billing`、`binding`、`cache`、`cache_routing`、`cachemetrics`、`channelhealth`、`clientid`、`clientshape`、`community`、`config`、`credentialacq`、`credentialstore`、`credentialworker`、`db`、`dlq`、`email`、`eventbus`、`gateway`、`gatewayhttp`、`obs`、`observability`、`openapicheck`、`pool`、`privacy`、`proto`、`provider`、`rate`、`redact`、`registry`、`router`、`sign`、`tokencheck`、`transport`、`userauth`、`usersession`、`voucher`。这说明功能域已经显式命名，后续拆分有抓手。

5. `backend/internal/db` 和 SQL 组织很强。`backend/sql/queries/` 有 25 个查询文件，`backend/sql/migrations/` 有 49 个 `.up.sql`；`backend/sqlc.yaml` 把生成代码分到 `internal/db/admin`、`internal/db/billing`、`internal/db/auth`、`internal/db/audit`、`internal/db/registry`，比把所有 sqlc 输出塞进一个包更可维护。

6. `pool`、`provider`、`proto` 已经出现二级结构。`internal/pool` 下有 `binding/`、`dispatcher/`、`router/`、`scoring/`；`internal/provider` 下有 `openai/`、`gemini/`、`bedrock/`、`anthropic/` 等 vendor 子目录；`internal/proto` 下也有 `openai/`、`gemini/`、`anthropic/`、`bedrock/`。这些都是正确的拆分方向。

7. 测试布局覆盖面较好。`backend/` 下有 237 个 `*_test.go`，其中 `internal/` 231 个、`cmd/` 6 个；`integration_pg` build tag 出现在 billing、pool、admin、provider、auditledger、db、obs、registry、dlq、auth 等路径。`backend/Makefile` 明确区分 `test` 和 `test-integration`。

8. OpenAPI 契约意识存在。`docs/openapi/openapi.yaml` 约 168KB；`backend/cmd/openapi-check/main.go` 和 `backend/cmd/gateway/openapi_consistency_test.go` 都围绕 `docs/openapi/openapi.yaml` 做路径一致性检查；`backend/cmd/gateway/routes.go` 也声明 route wiring 对齐 OpenAPI。

9. `exploratory/` 与生产 Go 后端当前隔离良好。扫描 `backend/` 中的 `exploratory`、`rust-core-gateway`、`python-offline-analytics` 字符串没有命中，说明生产 Go 后端没有显式依赖实验目录。

10. `frontend/` 虽小，但基本 Next.js 项目结构清楚。`app/` 有页面入口，`components/` 拆出布局、dashboard、audit、ui 组件，`lib/api/` 拆出 API client 文件；48 个源文件对于一个手测/vertical closure wedge 是可接受的。

## 缺点

### 1. `exploratory/rust-core-gateway` 已经不像“探索目录”（HIGH）

证据：`exploratory/` 有 1694 个源视角文件，其中 `rust-core-gateway/` 1693 个；其下 `vendor/` 1561 个、`merged/` 89 个。`exploratory/rust-core-gateway/merged/Cargo.toml` 是正式 Cargo workspace，`crates/core_gateway/src/` 下有 `proxy_engine/`、`stream_pipeline/`、`mimicry/`、`attempt_reporter/` 等生产形态模块，`merged/README.md` 写明 `M-rust-1` merged lane、build/test/clippy/fmt 验证结果与后续推进计划。

判断：隔离是好的，但“探索桶”已经承载一个独立 Rust core gateway。继续放在 `exploratory/` 会模糊 Owner、发布、CI、license/vendor 审计和与 Go gateway 的关系。

一行修复方向：把 `exploratory/rust-core-gateway/merged` 提升为一等模块，例如 `rust-gateway/` 或 `gateway-rust/`；把 `claude-lane/`、`codex-lane/`、`claude-m3/` 归档到 `docs/process/archive/` 或 `exploratory/archive/`。

### 2. Admin API 路由前缀不统一（HIGH）

证据：`backend/cmd/gateway/routes.go` 同时挂载 `/admin/v1/...` 与 `/v1/admin/...`。例如 provider accounts 同时有 `/admin/v1/provider-accounts`、`/v1/admin/provider-accounts`、`/v1/admin/pool-accounts`；email、voucher、channel-health 则偏 `/v1/admin/...`；usage、billing、audit-events、dlq、cache/l2 偏 `/admin/v1/...`。在 `backend/cmd` 与 `backend/internal/gatewayhttp` 的 Go 文件中，`/admin/v1` 字符串出现 112 次，`/v1/admin` 出现 34 次。

判断：这不是单纯命名 nit。API prefix 是外部契约的一部分；双前缀如果不是明确 alias/deprecation 策略，会让 OpenAPI、前端 client、权限中间件和文档长期分叉。

一行修复方向：确定唯一 canonical admin prefix；另一套只作为兼容 alias 存在，并在 OpenAPI、路由测试和前端 client 中标注 deprecation/compatibility。

### 3. 前端技术栈与权威规则/文档冲突（HIGH）

证据：`docs/RULES.md` TS-002 写的是“前端 TS + React + Vite + TanStack + Tailwind”，TS-004 写的是“OpenAPI 是 contract source of truth，前端类型从此 codegen”。实际 `frontend/package.json` 使用 `next: 14.2.5`，没有 Vite/TanStack；`frontend/lib/api/types.ts` 文件注释写“从 docs/openapi/openapi.yaml 推导”，但工作树未发现前端 OpenAPI codegen 配置或生成标记；`frontend/README.md` 仍写“不是 Admin UI，不含 pool / user / billing 管理页”，而实际 `frontend/app/` 已有 `accounts/`、`audit/`、`bindings/`、`mimicry/`、`renew/`、`selection/` 等页面。

判断：这里的风险不是 Next.js 本身，而是架构规则、README、实际实现三者不一致。后续 agent 会按不同“真相”工作。

一行修复方向：Owner 选择其一：更新 DR/RULES 正式接受 Next.js，或迁回 Vite/TanStack；同时补前端 OpenAPI codegen pipeline，刷新 `frontend/README.md`。

### 4. `gatewayhttp` 已经是 HTTP god-package（MED）

证据：`backend/internal/gatewayhttp` 有 68 个 Go 文件，约 20339 行；文件名覆盖 admin billing settings、admin cache L2、credential acquisition、credentials、DLQ、email settings、observability、pool accounts、pools、audit verify/pubkey、auth/session、channel health、chat completions、cost receipt、invitation、voucher 等多条业务线。

判断：它当前作为 HTTP adapter 包可以工作，但已把 admin ops、user auth、billing/receipt、gateway inference handler 混在一个包内。规模继续扩大时，权限依赖、测试 fixture、handler deps 会互相牵扯。

一行修复方向：保留 `gatewayhttp` 作为薄 wiring 层，按域拆成 `internal/http/admin...`、`internal/http/user...`、`internal/http/gateway...` 或在 `gatewayhttp/` 下建子包，避免所有 handler 共用一个包命名空间。

### 5. `gateway` 与 `proto` 根包仍偏重（MED）

证据：`backend/internal/gateway` 有 49 个 Go 文件、约 13837 行，含 forwarder、stream scanner、health FSM、mimicry compose、token bucket、protocol selector、storm policy、upstream dispatcher、cache control 等。`backend/internal/proto` 有 86 个 Go 文件、约 17869 行，含 capability matrix、client adapter registry、OpenAI/Anthropic/Gemini request/response/stream parsing、HCSF、trust-chain mismatch 等。

判断：这两个包已经有 subpackage 化迹象，但根包仍承担过多核心概念。未来改协议、账单、审计、streaming 时，根包容易成为跨域冲突点。

一行修复方向：把 root package 收缩为稳定类型/接口/策略；把 OpenAI/Anthropic/Gemini/Bedrock、stream projection、capability registry、trust-chain 分别推到子包或明确的 internal family。

### 6. 文档树有双入口和遗留层（MED）

证据：`docs/` 有 628 个文件，其中 `docs/process/plans/` 334 个、`docs/process/research/` 20 个、`docs/research/` 47 个、`docs/decompositions/` 88 个、`docs/reference_delta/` 18 个；同时顶层还有 `reference_deep_dive/` 41 个文件。`docs/plans/` 也存在 1 个计划文件，而主计划目录是 `docs/process/plans/`。`docs/decompositions/` 中有 `_superseded-round1`、`_superseded-round2`。

判断：文档量大本身不是问题，问题是同类资料有多个家：research 在 `docs/research` 和 `docs/process/research`，reference deep dive 在顶层和 `docs/reference_delta`，plans 在 `docs/plans` 和 `docs/process/plans`。这会制造引用漂移和“哪个是最新”的判断成本。

一行修复方向：建立 docs taxonomy：`docs/process/*` 只放过程产物，`docs/research/*` 只放研究结论，历史 round 进 `docs/archive/*`，顶层 `reference_deep_dive/` 迁入 `docs/research/reference-deep-dive/` 或归档。

### 7. 物理工作树被缓存/构建产物严重污染（MED）

证据：排除 `.git` 后物理工作树有 112694 个文件；巨大目录包括 `huakai-rust-target/` 约 16G、`cargo-targets/` 约 5.8G、`tmp/` 约 4.5G、`backend/.cache/` 约 1.5G、`.cache/` 约 618M、`.gocache/` 约 571M、`frontend/node_modules/` 约 514M、`frontend/.next/` 约 177M。`.gitignore` 已覆盖这些路径，但它们仍在共享工作树里。

判断：这不一定是仓库提交风险，但它是工作树结构风险：agent 搜索、备份、压缩、磁盘配额、误读文件数都会受影响。

一行修复方向：保留 ignore 规则，同时提供 `make clean-worktree-artifacts` 或 `scripts/clean-local-artifacts.sh`，并把 Rust/Go/Next build cache 默认落到 `/tmp` 或统一 `.cache/` 下。

### 8. 命名有同义域漂移（MED）

证据：认证相关同时有 `auth`、`userauth`、`usersession`、`tokencheck`；审计相关有 `audit` 与 `auditledger`；观测相关有 `obs` 与 `observability`；缓存相关有 `cache`、`cache_routing`、`cachemetrics`；admin HTTP 分散在 `adminhttp` 与 `gatewayhttp` 的多个 `admin_*` 文件。目录命名也混用 `cache_routing` 这种 underscore Go package。

判断：这些包未必代码有错，但命名层面已经让新贡献者难判断“哪个包负责哪一层”。这和 `docs/RULES.md` TS-005 的“术语严格对齐，不许同义词”方向不一致。

一行修复方向：做一张 package ownership map：每个域只保留一个 canonical noun，legacy 包通过小步迁移合并或重命名；Go package 目录优先使用无 underscore 的短名。

### 9. 缺少根级别构建/CI 编排（MED）

证据：顶层没有 `Makefile` 或 `.github/` 工作流目录；`backend/Makefile` 很完整，但只覆盖 Go 后端；`frontend/package.json` 有 `dev/build/start/type-check`；`exploratory/rust-core-gateway/merged/Cargo.toml` 是 Rust workspace；`tools/fingerprint-collector/go.mod` 又是独立 Go module。根目录只有 `scripts/run-tests.sh`、`scripts/run-integration-tests.sh`、`scripts/db_schema_review.sh` 这类脚本。

判断：多语言、多模块项目进入 release gate 后，缺根级别任务图会导致“后端过了、前端没跑、Rust 没跑、工具没跑”的局部成功。

一行修复方向：添加根级 `Makefile`/`justfile` 或 CI matrix，显式串起 backend test、integration_pg、frontend type-check/build、Rust fmt/clippy/test、tool tests。

### 10. 数据库迁移编号统一，但 rollback 结构不完整（MED）

证据：`backend/sql/migrations/` 有 49 个 `.up.sql`，但只有 42 个 `.down.sql`；`0001` 到 `0007` 没有对应 down 文件，从 `0008` 开始才基本成对。`backend/Makefile` 的 `migrate-down` 目标注释仍写“requires .down.sql files; Phase 4 task”。

判断：迁移编号和命名是优点，但 rollback 策略不是全程一致。早期 baseline 可以选择不可逆，但必须明确，否则 operator 会以为所有版本都可按工具回退。

一行修复方向：为 `0001` 到 `0007` 补 down，或正式声明这些是 irreversible baseline，并让 `migrate-down`/runbook 对 baseline 迁移特殊处理。

### 11. `backend/pkg/adapter` 是悬空 public package（LOW）

证据：`backend/pkg/adapter/adapter.go` 只有包注释和 TODO，描述未来 `pkg/adapter/openaichat`、`pkg/adapter/anthropicupstream` 等子包；实际当前适配器形态主要在 `internal/proto/*` 和 `internal/provider/*`。

判断：`pkg/` 在 Go 里通常暗示外部可导入 API。现在这里既没有实现，也和真实适配器位置不同，容易误导。

一行修复方向：在实现前移除 `pkg/adapter`，或改为 `internal/adapter` 并和 `proto/provider` 拆分计划同步。

### 12. `tools/fingerprint-collector` 混有本地输出和二进制（LOW）

证据：`tools/fingerprint-collector/bin/fingerprint-collector` 存在；`tools/fingerprint-collector/output/` 下有 `clienthello-template.json`、`http2-settings.json`、`ja3-hashes.txt`、`ja4-hashes.txt`、`metadata.json`、stdout/stderr log。根 `.gitignore` 明确写 `tools/fingerprint-collector/output/` 和 collector binary 不应提交，并说明公开仓库不应携带 captured template / pcap / JA3 / JA4 / metadata。

判断：这更像工作树卫生问题，不一定是 tracked 风险。但该工具本身处理 fingerprint 证据，本地产物留在仓库树下会增加误提交和审计噪音。

一行修复方向：保留模板/fixtures，清理本地 capture output；将默认输出目录改到 `/tmp/huakai-fingerprint-collector` 或 operator 明确配置的私有目录。

## 维度结论

### 1. 顶层布局

`backend/`、`docs/`、`frontend/`、`tools/` 是合理主干；`exploratory/` 的存在也合理，但 `rust-core-gateway` 已经越过“探索小项目”的阈值。顶层最明显的 misplaced 是 `reference_deep_dive/`，它应进入 `docs/research` 或归档目录。根目录 README 多语言文件可以保留；`ROUND_7_REPORT.md` 这类轮次报告更适合归入 `docs/process/reviews` 或 archive。

### 2. backend/internal 组织

边界总体可维护，尤其 `db`、`pool`、`provider` 已经有正确的二级拆分。但 `gatewayhttp`、`gateway`、`proto` 是三个主要重包，应成为下一轮结构治理重点。`cmd/` 和 `sql/` 组织 sound；`pkg/` 目前不 sound，因为只有一个未来 TODO public package。

### 3. exploratory

隔离状态好：生产 `backend/` 没有显式依赖 `exploratory/`。但体量和成熟度已经要求提升 `rust-core-gateway/merged` 的身份。建议不是删除 `exploratory/`，而是把活跃 Rust core gateway 提升为一等模块，把 lane 历史归档。

### 4. docs

docs 内容覆盖很强，但组织开始 sprawling。最大问题是 research/plans/reference deep dive 多入口和历史 round 未统一归档。需要 taxonomy 和 index，不需要推倒重来。

### 5. 命名与约定

Go package 命名总体可读，但同义词漂移明显；API route prefix 是实质性不一致；migration 编号统一但 down 文件策略不一致；前端技术栈与 `docs/RULES.md` 不一致。

### 6. Dead / orphaned / misplaced

高优先级 misplaced：`exploratory/rust-core-gateway/merged`、`reference_deep_dive/`。中低优先级 orphan/stale：`frontend/README.md`、`backend/pkg/adapter`、`docs/plans/` 单文件旁路、`ROUND_7_REPORT.md`、工具本地 output、工作树缓存。

### 7. Build / tooling / config

后端 tooling 很好，前端 tooling 基本够用，Rust merged workspace 独立可读；弱点是没有根级 task graph/CI matrix。`integration_pg` 测试分层清楚，是值得保留的约定。

### 8. Frontend 与 backend 结构平衡

`frontend/` 48 个源视角文件，`backend/` 816 个，后端规模约为前端 17 倍。若 frontend 仍只是 vertical closure wedge，这是合理；若目标是 Admin Ops Platform，则前端结构显著 underweight，且无前端测试文件、README 已落后实际页面。建议先对齐产品定位和技术栈规则，再决定是否扩张。

## Bottom-line verdict

结论：work-tree architecture fundamentally sound，应该“保留主结构 + 分阶段治理”，不需要全仓结构重做。

保留的主轴：`backend/` Go 服务、`docs/` 规格流程、`frontend/` UI、`tools/` 辅助工具、`exploratory/` 实验区。必须尽快治理的结构弱点是：Rust core gateway 的身份、Admin API prefix、前端规则/实现冲突、三个后端重包、docs 多入口、根级 CI/task graph。

最高优先级建议：先把 `exploratory/rust-core-gateway/merged` 提升或明确冻结，因为它同时影响模块身份、CI、vendor/license、release gate 和 Go/Rust gateway 的产品边界。随后统一 admin route prefix 和前端技术栈规则。这三件事修完后，其余问题可按低风险重构逐步处理。
