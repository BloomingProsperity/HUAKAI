# 2026-07-10 管理端按用户下钻明细用量（合成执行计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “管理端按用户下钻明细用量 GET /admin/v1/users/{id}/usage”；“代码注释全中文、报告全中文”；“禁止 commit” |
| Scope | 新增 admin 用户逐条用量只读路由、`UsageStore` 依赖、gateway 生产接线、判别测试和 OpenAPI；不改 SQL、schema、迁移、鉴权核心、计费/额度写路径或 runtime 依赖。 |
| Success criteria | 目标用户先经管理员租户作用域做存在性校验；用量查询同时精确设置管理员 `TenantID` 与 URL `UserID`，不设置 API key 限定；过滤、1..200 limit、keyset cursor、响应 DTO 与 `/v1/me/usage` 同语义；非法 status 400；接线非 nil且同实例；指定门全绿或如实报告环境性跳过。 |
| Time estimate | 45–75 分钟墙钟时间；60–100 分钟 agent 工作量。 |
| Blast radius | 一个新增 admin GET operation、admin 用户 Deps 组装、OpenAPI；错误可能导致死路由、分页漂移或租户数据越界。 |
| Failure modes | 精确参数捕获防漏 `TenantID/UserID`；Store 零调用断言防非法 status 继续查询；真实装配 helper 防字段未注入；`limit+1` 响应测试防分页 off-by-one；运行时/OpenAPI 双向断言防契约遗漏；handler 单独成文件防继续膨胀 867 行 `routes.go`。 |
| Decision points | 两份独立计划唯一分歧是目标用户不存在时 404 或空列表。按 Owner 指定的 `balance-history` 同形态，采用租户内存在性预检并统一 404；跨租户 ID 不可区分。任何 SQL/schema/鉴权核心/资金路径扩项均停止并请求 Owner。 |
| Pre-execution checklist | 工作树起点干净；已读规则、样板、查询参数类型、接线和一致性测试；`adminuserhttp` 仅 5 个非测试文件/约 1384 行，可新增内聚文件且不越包预算；代码注释与测试说明全中文。 |

## 双计划交叉结论

- 一致：新建 `user_usage.go` 与测试文件；`routes.go` 只放 Deps/挂载/装配；使用管理端专用 cursor kind；OpenAPI 直接同步；production wiring 测试同时检查非 nil 与同一实例。
- 差异裁决：按余额历史样板先做 `AdminGetUserForTenant`，不存在或跨租户统一 404；合法用户随后调用 `ListUsageRecords`，仍强制传双作用域。
- 补充项：断言 `APIKeyID == nil`，保证管理员看到该用户所有 key 的记录；OpenAPI/runtime 都明确禁止该路径的写方法。

## 执行顺序

1. `internal/adminuserhttp/routes.go` 增 `Deps.UsageStore` 并挂 `GET /{id}/usage`。
2. 新建内聚 handler：依赖检查 → `resolveTenantIdentity` → `pathID` → query 校验 → 租户内用户存在性检查 → 双作用域查询 → `limit+1` 分页 → 兼容 DTO。
3. 新增判别测试：双作用域+过滤、非法 status 零查询、分页/响应/游标；所有测试注释写明被守变异。
4. 把生产 admin user Deps 字面量提取为可测试 helper，并在 `cmd/gateway/routes.go` 明确注入 `UsageStore: d.billingQueries`；新增同实例接线测试。
5. 补 `docs/openapi/openapi.yaml` 的 path/参数/响应，并更新运行时/OpenAPI 只读一致性测试。
6. 仅 gofmt 本次 Go 文件；跑目标门、`git diff --check`、代码预算相关门；复核零 SQL/schema、零 commit。
