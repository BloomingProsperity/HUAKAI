# 2026-07-14 P0-b 代理组全链修复（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI P0-b · 代理组全链修复——代理能进组 + 空组可观测 + 绑组预警” |
| Scope | 覆盖代理管理领域模型、sqlc 源查询与生成码、HTTP DTO、OpenAPI、请求期 resolver 空组日志、代理运营页、账号编辑绑组预警及判别性测试。明确不改数据库 schema、不改账号侧 group_id 校验、不改 fail-closed 语义、不动 `Sidebar.tsx`，不运行 sqlc generate，不执行 git add/commit/checkout。 |
| Success criteria | 代理 create/get/list/update/清组贯通；非法组标识返回 HTTP 400；resolver 空组仍返回可被 `errors.Is` 识别的 `ErrProxyUnhealthy`，同时错误与日志包含租户、账号、组上下文；代理页能编组与回显；账号组选项来自现有代理列表并显示 active 数，零成员时明确预警；指定 Go/前端检查全绿。 |
| Time estimate | 墙钟约 2—4 小时；单代理工程时间约 4—6 小时，主要成本在手工同步 sqlc 生成码、判别性测试与全量门验证。 |
| Blast radius | 代理管理 CRUD 契约、管理台代理/账号编辑 UI、请求期代理选择错误日志。若 SQL 参数/Scan 顺序错会导致代理 CRUD 运行失败；若前端 DTO 漂移会导致清组或预警错误；若 resolver 改坏会产生静默直连或错误分类回归。 |
| Failure modes | 见下文“失败模式与缓解”。 |
| Decision points | 本轮 Owner 已锁定所有产品语义，无待选产品分支。若发现现有 schema 缺列、需要新增依赖、需要改变账号侧校验或 fail-closed 语义，立即停止并另请 Owner 确认；当前初读未发现这些条件。 |
| Pre-execution checklist | 见下文有序清单。 |

## 约束与观察

- `REFERENCE PROJECTS IN SCOPE`: CLIProxyAPI、sub2api、new-api。Owner 本轮明确指定这是 HUAKAI 内部断链修复且“无需读镜像源码”，因此本计划与执行均不进入镜像目录、不作外部项目行为断言。
- 已观察 `backend/sql/migrations/0124_proxy_groups.up.sql` 中存在 `proxies.group_id text`，因此不需要也不允许新增迁移。
- 已观察代理管理服务依赖 `internal/db/admin` 的 sqlc 查询，不是 `service.go` 内手写 SQL；按 Owner 指令只手工同步查询源与生成码。
- 已观察账号侧仅校验绑组值非空，没有同一格式校验；本轮只在代理写口执行 `[A-Za-z0-9_-]{0,64}`，不扩大账号侧行为。
- 已观察账号编辑弹窗已请求租户代理列表，故预警无需新增后端端点，也无需第二次请求。
- `acceptance-test-writer` 约束本轮测试同时覆盖正常写入、非法输入、清组恢复、空组失败与可观测证据；`frontend-ops-ui-review` 约束组归属、active 成员数及危险绑定后果必须在操作点可见。

## 失败模式与缓解

| 失败模式 | 后果 | 缓解与判别门 |
| --- | --- | --- |
| 只改 sqlc 源或只改生成码 | 编译通过但下次生成回退，或运行时参数错位 | 两边同一工作单元同步；核对 SQL 占位符、参数结构、调用参数、Scan 顺序；不运行生成器。 |
| INSERT/RETURNING/SELECT 漏 `group_id` | create 成功但回显/后续读取丢组 | 单元桩断言写参和投影；真实 PG create→get/list 测试咬住同值。 |
| UPDATE 无法清组 | 运营无法从空组故障中自愈 | 真实 PG 测试依次改组并传 nil 清组，断言数据库/回显均为 NULL。 |
| 校验只在前端 | API 可绕过并写入不可匹配组名 | service 作为权威校验点；HTTP 通过真实 service 路径断言 400；前端仅做同规则即时反馈。 |
| resolver 为了“可用”回落直连 | 暴露真实出口 IP，破坏账号隔离 | 仅包装原 sentinel 并记录 warn；测试同时断言 `errors.Is`、账号号、组名和日志字段。 |
| 日志含敏感代理凭据 | 凭据泄露 | 日志只含 tenant_id/account_id/group_id，不含 host、用户名、secret 或 URL。 |
| 账号预警把 disabled/dead 算作可用 | UI 误判组健康，绑定后请求全失败 | 纯函数按 `status === 'active'` 计数；零与非零双向判别测试。 |
| 组名不存在与已存在空组无法区分 | 文案含糊但风险相同 | datalist 只列已出现组；任意输入都计算 active 数，未知组为 0并触发同一 fail-closed 警告。 |
| 前端清组字段被省略后语义漂移 | 编辑时旧组残留 | PATCH payload 对空值显式发送 `group_id: null`；测试精确断言 payload。 |
| 并行 AI 覆盖同文件 | 用户改动丢失 | 每批编辑前跑 `.coordination/check.sh` 并 claim 全部目标；发现 live 冲突立即停在冲突文件外。 |

## 预执行清单

1. 再次确认工作树无未知改动，并检查所有目标文件的 `.coordination` live lock。
2. claim 精确文件清单；不 claim、不编辑 `Sidebar.tsx`、迁移文件、鉴权/计费/配额代码。
3. 记录代理管理现有测试桩、真实 PG 模式、HTTP 错误映射与 OpenAPI schema 的当前契约。
4. 确认 resolver 调用方均以 `errors.Is(err, ErrProxyUnhealthy)` 或通用错误处理消费，包装不会改变分类。
5. 确认前端相关测试环境可用 `vitest`，以及账号弹窗已有代理列表请求可直接复用。
6. 实现后先跑定向格式化与测试，再跑仓库级 build/vet/OpenAPI 门，最后释放协调锁。

## 具体执行顺序

1. **代理领域模型与权威校验**
   - 在 `Proxy`、`CreateInput`、`UpdateInput` 增加 `GroupID *string`。
   - service 校验 nil/空串合法，非空须完整匹配 ASCII 字母、数字、下划线、短横线且最长 64；空串入库前规格化为 nil。
   - 四个 row→Proxy 投影全部回显组值。

2. **sqlc 查询源与生成码手工同步**
   - `CreateProxy` 的 INSERT/RETURNING、`UpdateProxy` 的 SET/RETURNING、`GetProxy` 与 `ListProxiesByTenant` 的 SELECT 补 `group_id`。
   - 同步 Params/Row 字段、实参顺序与 Scan 顺序；明确不运行 `sqlc generate`。

3. **HTTP 契约**
   - create/update DTO 接收 `group_id`，响应 DTO 始终输出 `group_id`（无组为 null）。
   - 保持现有审计现状：该路由当前无 create/update 审计机制，本轮不另造机制。
   - HTTP 测试咬住请求透传、list/get/create/update 回显、非法字符与超长值的 400。

4. **OpenAPI**
   - `Proxy`、`AdminProxyCreateRequest`、`AdminProxyUpdateRequest` 增加 nullable string、`maxLength: 64`、pattern 与中文语义描述；响应 `Proxy.required` 加 `group_id`，保证 null 也稳定出现。

5. **resolver 空组可观测**
   - 在空组/无 active 成员分支记录一条 `slog.WarnContext`，字段固定为 `tenant_id`、`account_id`、`group_id`。
   - 返回包含账号号、组名、“无 active 成员”与“不落直连”的 `%w` 包装错误；不改变任何代理选择或回退分支。
   - 通过小型内部 helper 让无数据库单测可精确验证错误链与结构化日志，生产分支只调用该 helper。

6. **代理运营页**
   - 类型、表单状态、create/update payload、表格行增加 `group_id`。
   - create/edit 增加“代理组”输入与同名/留空提示；前端校验镜像后端字符集和长度；列表增加“分组”列，null 显示“未分组”。
   - 不新增设计抽象，沿用现有 `Field`、input 样式和纯函数测试结构。

7. **账号绑组预警**
   - 在账号编辑纯逻辑中按组名汇总总成员与 active 成员，供 datalist、计数和警告共同消费。
   - group 模式输入挂已有组名 datalist；旁边显示当前组 active 数；active=0（含未知组）显示指定醒目文案，active>0 不显示危险文案。
   - 代理列表加载失败时保留现有非阻塞行为，并在 group 模式也显示无法核验可用性的提示，避免把未知状态误报为健康。

8. **判别性测试**
   - 后端 service 单测：create 写入并回显组；update 改组与 nil 清组；怪字符/65 字符在触达 querier 前拒绝。
   - 真实 PG：create→get/list 同组、update 改组、update nil 清组；未配置 `HUAKAI_DATABASE_URL` 时按既有模式 skip。
   - HTTP：DTO 透传/回显及非法格式 400；删除 service 校验后该 400 测试必须变红。
   - resolver：错误文本、`errors.Is`、Warn 级别与三个字段；去掉 `%w` 或上下文/日志任一项均转红。
   - 前端：代理表单 SSR 包含组输入和提示；create/update payload 精确携带组或 null；组汇总 active 计数；0 显示警告、非 0 不显示。

9. **验证与收尾**
   - `gofmt` 修改的 Go 文件；运行 `go test -count=1` 定向包、`go test -tags=integration_pg -count=1 ./internal/proxyadmin/...`（无 DSN 允许 skip）、`go test ./cmd/gateway/`。
   - 在 `backend/` 运行 `go build ./...`、`go vet ./...`；在 `frontend/` 运行相关 `npx vitest run` 与 `npx tsc --noEmit`（若项目配置要求则用等价 `npx tsc -b --noEmit` 并如实记录）。
   - 查看 `git diff --check` 与仅目标文件 diff；绝不 stage/commit/checkout。
   - 释放协调锁，按 Owner 八项规则报告文件清单、功能/clean-room/安全风险、每个变异测试为何会红，并停下等待审查。

## 独立计划交叉讨论状态

- 本文件是在未读取任何同 descriptor Claude 计划的前提下独立形成。
- 当前仓库未发现 `2026-07-14-p0b-proxy-group-chain-claude.md` 或无后缀综合计划，因此尚不能完成规则要求的“双独立草案→差异→综合计划”步骤。
- Owner 提供的修复清单已锁定范围与语义，但按 `AGENTS.md`，实现前仍需 Claude 独立草案及综合计划获批；在此之前 Codex 不执行上述实现。
