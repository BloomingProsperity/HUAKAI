# 2026-07-10 管理端按用户下钻明细用量（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “管理端按用户下钻明细用量 GET /admin/v1/users/{id}/usage”；“代码注释全中文、报告全中文”；“禁止 commit” |
| Scope | 在 `internal/adminuserhttp` 增加同租户、指定用户的只读用量路由及判别测试；在 `cmd/gateway/routes.go` 注入现有 `billingQueries` 并增加接线判别测试；按运行时/OpenAPI 一致性要求补 `docs/openapi/openapi.yaml`。不改 SQL、schema、迁移、鉴权核心、计费写路径、额度逻辑或依赖。 |
| Success criteria | `GET /admin/v1/users/{id}/usage` 使用管理员身份解析出的租户和 URL 用户 ID 同时限定 `ListUsageRecords`；过滤、时间、游标和 1..200 限制与 `/v1/me/usage` 一致；响应字段兼容；非法 `status` 返回 400；接线非 nil；指定 build/vet/test 门全部通过或如实列出环境性跳过。 |
| Time estimate | 约 45–75 分钟墙钟时间；约 60–100 分钟 agent 工作量（含只读梳理、实现、OpenAPI、格式化和全量 build）。 |
| Blast radius | 新增一个 admin GET 路由；修改 admin 用户路由依赖装配；OpenAPI 增加一个只读 operation。失败时可能表现为路由未挂载、依赖 nil 返回 503、过滤参数错传、跨租户数据暴露、分页游标不兼容或 OpenAPI 一致性门失败。 |
| Failure modes | 1. 只传 `UserID` 不传 `TenantID`：用捕获参数的判别测试同时精确断言两者。2. Deps 字段增加但未注入：提取可直接测试的装配 helper，并断言 `UsageStore != nil`。3. 错用 API key 自作用域：管理端参数明确不设置 `APIKeyID`。4. 游标或 limit off-by-one：查询取 `limit+1`，第 `limit` 条生成 next cursor。5. 响应 DTO 漂移：保持 `/v1/me/usage` 的 JSON 字段名。6. 在已超 600 行的 `routes.go` 堆入 handler：仅在该文件增加必要 Deps/挂载/装配调用，把内聚实现放到新文件；运行 codebudget 所在测试门。7. OpenAPI 漏项：预先补 path 并运行 `cmd/gateway` 测试。 |
| Decision points | 当前没有新增高风险决策：Owner 已明确要求零 SQL/schema 改动并复用现有查询。若发现必须改 SQL、schema、鉴权核心、计费账本、额度或新增 runtime 依赖，则立即停止并请求 Owner 确认。 |
| Pre-execution checklist | 1. 确认工作树无既有未提交改动。2. 读 `docs/RULES.md`、同形态 admin handler、me usage 语义、现有装配与测试。3. 确认 `ListUsageRecordsParams.UserID` 已存在且 SQL 同时支持 tenant/user。4. 确认包预算：`adminuserhttp` 当前 5 个非测试文件、约 1384 行；新增内聚文件不越包预算，避免扩大 867 行的 `routes.go` handler 职责。5. 核对 OpenAPI 现有 usage schema 能否复用。6. 所有新增代码注释与测试说明用中文。 |

## 具体执行顺序

1. 在 `internal/adminuserhttp/routes.go` 给 `Deps` 增加用量只读依赖，并在 `MountRoutes` 将 `GET /{id}/usage` 与余额明细并列挂载；GET 沿用外层 admin 鉴权与读操作 session 规则。
2. 新建职责单一的 admin 用户用量 handler 文件：解析管理员租户身份与 URL 用户 ID；校验查询参数；构造同时带 `TenantID`、`UserID` 的 `dbbilling.ListUsageRecordsParams`；执行 keyset 分页并映射兼容 DTO。
3. 新增 handler 判别测试：捕获 Store 参数，精确断言目标用户、管理员租户、model、error outcome；另测非法 status 为 400 且 Store 未调用。测试注释写清两类变异为何变红。
4. 在 `cmd/gateway/routes.go` 提取/使用 admin user Deps 装配 helper，把现有 `d.billingQueries` 注入 `UsageStore`；新增接线判别测试，删除注入行时必须失败。
5. 在 `docs/openapi/openapi.yaml` 增加 path、参数、200/错误响应，优先复用已有 `/v1/me/usage` schema，避免复制漂移。
6. 只对本次修改的 Go 文件运行 `gofmt`。
7. 先跑目标测试，再跑 Owner 指定的 `go vet`、`go test -count=1`、`go build ./...`；检查是否有 DB 环境性 skip，并记录原始结论。
8. 检查 `git diff --check`、改动清单、无 SQL/schema/迁移/依赖变化、无 commit；完成只读交叉复核后以中文交付。

## 判别测试要点

- 处理器测试必须使用两个不同的非零 ID：管理员租户与 URL 目标用户。删除 `UserID`、把 `UserID` 错设为身份 ID、删除 `TenantID` 或写成目标用户 ID，至少一条精确相等断言会失败。
- 非法 `status` 测试同时断言 HTTP 400、错误码和 Store 调用次数为零，防止“先查后报错”或宽松放行。
- 接线测试直接调用生产装配 helper，断言 `UsageStore` 与输入的 `billingQueries` 为同一非 nil 实例；删除生产注入行会失败，不能只做编译型测试。

## OpenAPI 判断

这是新增运行时公开管理端路由，且仓库有运行时/OpenAPI 一致性测试，因此计划直接补 `docs/openapi/openapi.yaml`，不等待测试失败后再补。只增加新 path，不修改既有 `/v1/me/usage` 契约。
