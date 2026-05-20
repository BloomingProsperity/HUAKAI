# 2026-05-20 /renew 页面真实接线修复计划 - Codex 独立草案

| Owner directive | "HUAKAI 前端 /renew 页面修复 — 独立起草执行计划(平行交叉计划之一)"；"本轮只调查 + 写计划, 不改任何代码" |
| Scope | 本计划只覆盖 `/renew` 页面从 mock 改为真实前端读端点接线的执行方案；本轮已只读调查指定前后端文件，并只写本计划文件。执行期预计只改 `frontend/app/renew/page.tsx`、`frontend/lib/api/renew.ts`、`frontend/lib/api/types.ts`，除非 Owner 选择新增后端端点。 |
| Out of scope | 本轮不改 `.tsx` / `.ts` / `.go` 代码，不执行 git 操作，不读取参考项目源码，不新增数据库 schema，不实现后端 refresh worker 或真实 OAuth token exchange。 |
| Success criteria | `/renew` 不再展示硬编码 mock 数据；页面通过现有 admin API 展示真实账号与凭据 refresh 状态；表头全英文；不能真实执行的 renew action 不再伪装成功；类型、错误处理、空态与 501/网络错误处理遵循现有前端范式；可用检查通过。 |
| Time estimate | 计划与对比：0.5h；前端实现：1.5h-2.5h；本地检查：0.5h；若 Owner 选择后端新端点，另需后端设计、实现、测试，至少 0.5-1.5d，且属于高风险确认项。 |
| Blast radius | 推荐方案 A 的主要影响面是 `/renew` 页面和前端 API 类型；会增加前端对 provider account list 与 per-account credentials list 的调用次数。方案 B 会触碰 Go backend credential/refresh 路由与可能的刷新语义，风险显著更高。 |

## 调查结论

1. 当前 `/renew` 页面仍是 mock：
   - `frontend/app/renew/page.tsx:4` 引入 `listRenewStatus` / `triggerRenew`。
   - `frontend/app/renew/page.tsx:7-8` 注释写明后端尚无 `/admin/v1/auth-credentials/{id}/renew-status`。
   - `frontend/app/renew/page.tsx:73-90` 展示 `MOCK` 标记和 mock 说明。
   - `frontend/app/renew/page.tsx:99-105` 当前表头为 `account_id / account_name / last_renew_at / next_renew_at / status / error_msg / 操作`，存在中英混排。
   - `frontend/lib/api/renew.ts:1-53` 全部是 `MOCK_DATA` 与模拟延迟。

2. 当前前端类型也明确将 renew 标记为 mock：
   - `frontend/lib/api/types.ts:307-318` 定义 `RenewStatus` 与 `AuthCredentialRenewStatus`，注释为 "Mock: Renew status"。
   - `ProviderAccount` 已有 `tenant_id`、`credential_state`、`expires_at` 等字段，见 `frontend/lib/api/types.ts:27-37`。

3. 现有真实前端 API 范式：
   - `frontend/lib/api/client.ts:38-85` 提供 `apiGet` / `apiPost` / `apiPatch` / `apiPostNoContent`，统一带 admin bearer token、JSON 解析和错误抛出。
   - `frontend/lib/api/providerAccounts.ts:14-29` 已用 `apiGet` 实现 `GET /admin/v1/provider-accounts`。
   - `/accounts`、`/bindings`、`/selection` 等页面使用 `useCallback + useEffect + loading/error state`，并对 `ApiError.isNotImplemented()` 做 501 提示；`/renew` 应沿用这个模式。

4. 后端已有 account credential / acquisition 端点，但没有直接 renew 状态端点：
   - provider account admin routes 挂在 `/admin/v1/provider-accounts`、`/v1/admin/provider-accounts`、`/v1/admin/pool-accounts`，见 `backend/cmd/gateway/routes.go:157-165`。
   - account credential routes 包括 `GET /{id}/credentials`、`POST /{id}/credentials`、`POST /{id}/credentials/{credentialID}/rotate`、`PATCH state`、`DELETE`，见 `backend/internal/gatewayhttp/admin_credentials_handler.go:49-54`。
   - credential list 返回 `{"credentials": rows}`，见 `backend/internal/gatewayhttp/admin_credentials_handler.go:57-73`。
   - rotate 端点要求提交新 credentials payload，并返回 `CredentialMetadata`，不是自动 renew，见 `backend/internal/gatewayhttp/admin_credentials_handler.go:101-120`。
   - credential acquisition routes 包括 start/status/callback/cancel/finalize，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:76-82`；status 返回 `{"flow": session}`，finalize 也需要 operator 提交 credentials，见 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:112-118`、`200-224`。
   - 在本次指定搜索范围内，未发现 `/renew`、`/renew-status` 或 `/auth-credentials/{id}/renew` 路由。

5. 可用于拼出 renew 视图的真实字段：
   - `ProviderAccount` 后端响应包含 `credential_state`、`last_refresh_at`、`last_refresh_outcome`，见 `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121-145`。
   - `CredentialMetadata` 包含 `state`、`access_expires_at`、`refresh_before_at`、`last_refresh_at`、`last_refresh_outcome`、`failure_class`、`failure_count`，见 `backend/internal/credentialstore/postgres_store.go:69-82`。
   - credential store 内部已有 refresh 成功/失败持久化字段，成功会更新 `last_refresh_at`、`last_refresh_outcome`、`refresh_before_at`，失败会写 `failure_class`、`failure_count`、`next_attempt_at`，见 `backend/internal/credentialstore/postgres_store.go:473-544`。但 `next_attempt_at` 当前不在 `CredentialMetadata` JSON 返回字段内。

## 范围判定与方案选择

### A. 用现有 account-credential 端点拼出 renew 视图，纯前端

做法：
- `listRenewStatus()` 改为真实调用：
  - 先 `GET /admin/v1/provider-accounts?limit=...` 获取账号与 `tenant_id`。
  - 对每个账号调用 `GET /admin/v1/provider-accounts/{id}/credentials?tenant_id={tenant_id}`。
  - 前端聚合为 `AuthCredentialRenewStatus[]`。
- 字段映射：
  - `account_id` <- `ProviderAccount.id`
  - `account_name` <- `ProviderAccount.name`
  - `last_renew_at` <- 优先 `CredentialMetadata.last_refresh_at`，再 fallback 到 `ProviderAccount.last_refresh_at`
  - `next_renew_at` <- `CredentialMetadata.refresh_before_at`；没有则 `null`，不把 `access_expires_at` 冒充 next renew
  - `renew_status` <- `credential_state` / credential `state` / `last_refresh_outcome` / `failure_class` 派生
  - `error_msg` <- `failure_class` 或 `last_refresh_outcome` 的保守说明；没有真实 redacted message 时显示 `null`
- Action：
  - 不把 `rotate` 或 acquisition `finalize` 冒充 `triggerRenew`。
  - 推荐将 action 显示为 disabled 文案，例如 `Renew endpoint unavailable`，或去掉按钮但保留 `Actions` 列用于后续后端端点接入。

优点：
- 满足 Owner "前端解冻" 方向，不触碰冻结中的 Go backend。
- 不保留假数据，页面展示真实账号/凭据状态。
- 实现面小，风险主要在前端聚合与请求量。

缺点：
- 只能做真实状态视图，不能执行真实一键 renew。
- `error_msg` 只能保守映射 failure class/outcome，不能展示后端未返回的详细错误消息。
- 多账号会产生 N+1 credentials 请求；当前 admin 页面 limit 小时可接受，后续可优化为后端聚合端点。

### B. 新增后端 renew/renew-status 端点

做法：
- 新增类似 `GET /admin/v1/provider-accounts/{id}/renew-status` 或聚合 `GET /admin/v1/auth-credentials/renew-status`。
- 如需触发 renew，新增 `POST /admin/v1/provider-accounts/{id}/credentials/{credentialID}/renew`，明确权限、审计、锁、失败重试、刷新 adapter 与安全边界。

优点：
- 能提供最权威、低请求数、语义清晰的状态和一键 renew 行为。

缺点：
- Go backend 处于冻结期，且 credential refresh/secret handling/审计属于高风险路径。
- 现有 `rotate` 需要新 payload，acquisition finalize 也需要 credentials；它们不是 renew。直接新增触发 renew 会进入 auth/credential core 风险区。
- 需要 Owner 明确解冻后端、确认端点契约和触发语义。

### C. 保留 mock，仅英文化表头和更清楚标注

优点：
- 最低风险，几乎只改 UI 文案。

缺点：
- 不满足 "把 /renew 页面从 mock 改成真实实现"。
- 继续展示假状态，会制造运营误导。

### Codex 推荐

推荐 A：本轮执行为纯前端真实读接线，交付真实 renew 状态视图；不实现也不伪造一键 renew。理由是 Owner 当前只解冻前端，且后端没有直接 renew 端点。A 能移除 mock 数据并保留功能意图，不触碰高风险后端 credential core。若 Owner 要求 `/renew` 必须包含可点击的真实 renew 动作，则需要先选择 B 并单独走后端高风险确认与计划。

## 具体执行步骤与估时

1. 对齐交叉计划并等 Owner 选择 A/B/C：15-30 分钟。
2. 若选择 A，补齐前端类型：20-30 分钟。
   - 在 `frontend/lib/api/types.ts` 增加 `AccountCredentialMetadata` 与 `AccountCredentialList` 类型。
   - 保留 `AuthCredentialRenewStatus`，但去掉 "Mock" 注释语义。
3. 改造 `frontend/lib/api/renew.ts`：45-75 分钟。
   - 引入 `apiGet` 和 `listProviderAccounts`。
   - 新增内部 `listAccountCredentials(accountId, tenantId)`。
   - 实现账号到 credential metadata 的并发拉取，设置合理 limit。
   - 实现 `deriveRenewStatus()` 与 `pickDisplayCredential()`。
   - `triggerRenew()` 不再修改本地 mock；推荐改为抛出明确错误或删除页面调用，避免假成功。
4. 改造 `frontend/app/renew/page.tsx`：30-60 分钟。
   - 移除 `MOCK` badge 和 mock 说明。
   - 改为真实数据说明或后端 action 缺口提示。
   - Actions 列不再触发 mock renew；如果保留按钮则 disabled，文案为 `Renew unavailable`。
   - 表头全英文。
5. 错误、空态、加载态检查：20-30 分钟。
   - 沿用其它 admin 页面 `ApiError.isNotImplemented()` 处理方式。
   - credentials 子请求失败时决定是整页失败还是单行显示错误；推荐整页失败，避免部分数据被误解为完整状态。
6. 本地验证：20-30 分钟。
   - 运行可用的 frontend type/lint/build 检查。
   - 若本地后端可用，手动打开 `/renew` 验证真实请求、空态、失败态、按钮禁用态。

## 状态映射建议

`renew_status` 保持当前三态，避免扩大 UI 范围：

| 输入状态 | 输出 `renew_status` | 说明 |
|---|---|---|
| account `credential_state` 为 `refreshing` / `refreshing_with_grace`，或 credential `state` 为 `refreshing` / `refreshing_with_grace` | `renewing` | 表示后端认为凭据处于 refresh 中或 grace refresh 中。 |
| account `credential_state` 为 `refresh_failed` / `revoked`，或 credential `state` 为 `expired` / `temp_unschedulable` / `needs_rotation` / `revoked` / `operator_attention`，或存在 `failure_class`，或 `last_refresh_outcome` 为 `refresh_failed` | `failed` | 保守呈现需要操作员关注的失败或不可用状态。 |
| 其它状态 | `idle` | 有真实记录但当前没有 refresh 进行中或失败信号。 |

## 表头英文化方案

| 当前表头 | 新表头 |
|---|---|
| `account_id` | `Account ID` |
| `account_name` | `Account Name` |
| `last_renew_at` | `Last Renewed` |
| `next_renew_at` | `Next Renew Due` |
| `status` | `Renew Status` |
| `error_msg` | `Error` |
| `操作` | `Actions` |

## Blast radius / 可能出错的点

- N+1 请求：账号数多时 `/renew` 会对每个账号额外请求 credentials。短期 admin 页面 limit 50/100 可接受；后续如性能不足，再走 B 的聚合只读端点。
- 字段语义差异：`refresh_before_at` 是最接近 `next_renew_at` 的真实字段；`access_expires_at` 是过期时间，不应冒充下次续期时间。
- 多 credential 账号：同一 provider account 可能有多个 credential。前端必须稳定选择 "最需要关注" 的 credential，否则状态可能跳动。
- `error_msg` 不完整：现有 list metadata 只有 `failure_class` / `last_refresh_outcome`，没有详细 redacted error message。计划中应显示保守错误摘要，不编造详情。
- Action 误导：现有 `rotate` 和 acquisition finalize 都需要新 credential payload，不是自动 renew。不能把它们接到 `Trigger Renew` 上制造假成功。
- 类型漂移：`frontend/lib/api/types.ts` 当前 `CredentialState` 范围比后端 credential store state 窄；新增 account credential metadata 类型时不要复用错误的 account-level `CredentialState`。
- 局部失败：一个账号 credentials 请求失败时，如果页面继续展示其它账号，Owner/运营可能误读为完整列表；推荐本轮先整页错误，后续再做 per-row partial error。

## 需要 Owner 决策的点

1. 是否接受推荐方案 A：本轮只做真实状态视图，不提供可执行的一键 renew。
2. 如果必须保留动作列，Owner 选择：
   - `Actions` 保留 disabled `Renew unavailable`；
   - 或完全去掉按钮，仅保留空 `Actions`；
   - 或改为跳转/提示去 credential acquisition/rotate 流程，但不命名为 renew。
3. 是否允许未来新增后端只读聚合端点以消除 N+1 请求。
4. 是否允许未来新增真正 `POST renew` 后端端点；这是高风险 backend credential core 变更，需要单独计划和确认。

## Pre-execution checklist

1. 等 Claude 独立计划完成后做交叉对比，记录 agreements / conflicts / gaps。
2. Owner 明确选择 A/B/C；若选择 B，先停止前端执行并起草后端高风险计划。
3. 确认执行范围只触碰前端三文件，除非 Owner 另行批准。
4. 确认不使用参考项目源码，不引入新依赖，不改 `LICENSE`、schema、auth、billing、quota、secrets。
5. 执行前再次读取最新 `/renew`、`renew.ts`、`types.ts`，防止并行改动冲突。
6. 实现后运行可用检查，并按项目规则做 Codex review 流程后再进入 commit 阶段。
