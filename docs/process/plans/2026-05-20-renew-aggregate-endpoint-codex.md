# 2026-05-20 renew aggregate endpoint Codex 独立执行计划

| Owner directive | "HUAKAI /renew 方案 B — 跨租户续期状态聚合端点 · 独立起草执行计划(平行交叉计划之一)" |
| Scope | 本计划只覆盖新增只读聚合端点、credentialstore 元数据查询、admin 审计、OpenAPI/前端 renew 面板接入与测试。不改 auth core、quota、billing、schema、密钥材料处理逻辑。 |
| Success criteria | 平台管理员不带 `tenant_id` 时能看到全租户凭据续期状态；租户操作员只能看到自己租户；前端不再通过 `listProviderAccounts` + 逐账号 credentials N+1 拼装；响应和审计均不包含明文、密文、nonce、AAD hash 或 credential fingerprint。 |
| Time estimate | 计划后实施约 0.5-1 天：后端 3-4 小时，前端 1-2 小时，测试/修正 2-3 小时。 |
| Blast radius | 中等：新增 admin 只读端点会暴露跨租户 credential 元数据给 platform_admin；实现面集中在 `credentialstore`、`gatewayhttp` admin handler、`routes.go`、前端 renew API/页面与 OpenAPI 类型。 |
| Failure modes | 误复用 `resolveProviderAccountAdmin` 导致继续静默默认租户 1；SQL 误选密文字段；审计 action/target_type 不在白名单导致 500；分页造成静默截断；tenant_operator 查询其它租户导致越权；前端把 `has_more` 忽略成完整结果。 |
| Decision points | D1: Owner 是否接受推荐路径 `GET /admin/v1/credentials/renew-status`。D2: Tenant 列一期是否只显示 `tenant_id`，还是需要额外 join `tenants.name`。D3: 前端是否必须一次 HTTP 请求取完，还是允许同一聚合端点 cursor 翻页直至完整。 |
| Pre-execution checklist | 1. 与 Claude 平行计划交叉对比并形成无后缀合成计划。2. Owner 确认 D1-D3 或接受默认推荐。3. 保持不读参考项目源码。4. 实施前确认没有新 schema 迁移需求。5. 实施后运行后端相关 Go 测试、前端 type-check、Codex per-commit review。 |

## 独立调查结论

本计划未读取 Claude 计划文件，未读取任何参考项目源码，未执行 git 操作。

已观察到的 HUAKAI 代码事实：

- `CredentialMetadata` 只包含元数据字段：`id`、`tenant_id`、`provider_account_id`、`vendor`、`auth_mode`、`state`、版本、过期/刷新时间、失败分类/次数和创建更新时间；不包含密文。见 `backend/internal/credentialstore/postgres_store.go:69`。
- 现有 `ListByAccount` 是 raw SQL，查询 `account_credentials` 时只 select 元数据列，并按 `tenant_id` + `provider_account_id` + `deleted_at IS NULL` 过滤。见 `backend/internal/credentialstore/postgres_store.go:286`。
- `account_credentials` 表确实包含高敏字段 `encrypted_payload`、`key_id`、`nonce`、`aad_hash`、`payload_fingerprint`、`refresh_token_fingerprint`，计划中的新查询必须明确排除这些字段。见 `backend/sql/migrations/0016_account_credentials.up.sql:9`。
- `provider_accounts` 有 `tenant_id`、`id`、`name`、`credentials`、`deleted_at`；新查询只需要 `pa.name`，不得读取 legacy `pa.credentials`。见 `backend/sql/migrations/0001_pool_routing.up.sql:108`。
- 现有 admin 路由把 provider account admin routes 挂在 `/admin/v1/provider-accounts`、`/v1/admin/provider-accounts`、`/v1/admin/pool-accounts`，并把 credential acquisition helper 挂在 `/admin/v1/credentials`。见 `backend/cmd/gateway/routes.go:133` 和 `backend/cmd/gateway/routes.go:167`。
- 现有单账号 credential list 只允许 platform_admin，要求显式 `tenant_id` query，并审计 `list_account_credentials`。见 `backend/internal/gatewayhttp/admin_credentials_handler.go:57` 和 `backend/internal/gatewayhttp/admin_credentials_handler.go:178`。
- 问题根源明确存在：`resolveProviderAccountAdmin` 对 platform_admin 如果没有 `ScopeTenantID` 会落到 `defaultAdminProviderAccountTenantID = 1`，`listProviderAccounts` 因此只返回租户 1。见 `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:25` 和 `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:490`。
- `admin_audit_events` 当前白名单已经包含 `list_account_credentials`，target_type 已包含 `provider_account` 与 `account_credential`；如果复用该 action，不需要新增审计白名单迁移。见 `backend/sql/migrations/0047_admin_audit_billing_setting_action.up.sql:9`。
- 前端 `renew.ts` 当前先分页拉 `listProviderAccounts`，再按账号并发 GET `/admin/v1/provider-accounts/{id}/credentials?tenant_id=...`，这是 N+1 且受租户 1 默认逻辑影响。见 `frontend/lib/api/renew.ts:20`。
- `AuthCredentialRenewStatus` 当前在 `CredentialMetadata` 之外补了 `account_id`、`account_name`，页面还没有 Tenant 列。见 `frontend/lib/api/types.ts:319` 和 `frontend/app/renew/page.tsx:82`。

## 端点设计

推荐新增：

`GET /admin/v1/credentials/renew-status`

原因：

- 这是跨 provider account 的 credential 状态聚合，不应挂在某个 provider account 子路径下。
- `/admin/v1/credentials` 已经是 credential acquisition helper 的顶层挂载点，新增只读 renew status 语义自然。
- 避开 `listProviderAccounts` 的租户默认路径，从接口形态上阻断 "先列账号再逐账号查凭据"。

查询参数：

- `tenant_id` 可选。platform_admin 不传表示全租户；传正整数表示显式缩小到某租户。tenant_operator 不传表示自己的 `ScopeTenantID`；传值必须等于自己的 `ScopeTenantID`，否则 403。
- `limit` 可选，默认 100，最大 500。
- `cursor` 可选，opaque base64 cursor，建议前缀 `credential_renew_id:`，内容为最后一条 `account_credentials.id`。

响应建议：

```json
{
  "credentials": [
    {
      "id": 201,
      "tenant_id": 7,
      "provider_account_id": 77,
      "provider_account_name": "openai-main",
      "vendor": "openai",
      "auth_mode": "api_key",
      "state": "active",
      "credential_version": 1,
      "access_expires_at": null,
      "refresh_before_at": null,
      "last_refresh_at": null,
      "last_refresh_outcome": null,
      "failure_class": null,
      "failure_count": 0,
      "created_at": "2026-05-20T00:00:00Z",
      "updated_at": "2026-05-20T00:00:00Z"
    }
  ],
  "page": {
    "cursor": null,
    "next_cursor": null,
    "has_more": false
  }
}
```

鉴权解析要新增专用 helper，例如 `resolveCredentialRenewStatusAdmin`，不要复用 `resolveProviderAccountAdmin`：

- `platform_admin`：`tenant_id` 缺省时返回全租户 scope，不得填充租户 1；`tenant_id` 存在时只过滤该租户。
- `tenant_operator`：必须有 `ScopeTenantID > 0`；请求缺省时使用该租户；请求指定其它租户时 403。
- 其它角色 403，未认证 401，admin backend transient error 503。

## 后端实现计划

1. 在 `credentialstore` 中新增只读元数据类型：
   - `CredentialRenewStatus` 或等价命名，包含 `CredentialMetadata` 同等字段，加 `ProviderAccountName string json:"provider_account_name"`。
   - `ListRenewStatusInput`：`TenantID *int64` 或 `TenantID int64 + AllTenants bool`、`AfterID int64`、`Limit int32`。
   - `ListRenewStatusResult`：`Rows []CredentialRenewStatus`、`HasMore bool` 或 handler 层用 `limit+1` 计算。

2. 在 `backend/internal/credentialstore/postgres_store.go` 新增 Store 方法，使用 raw SQL：

```sql
SELECT
  ac.id,
  ac.tenant_id,
  ac.provider_account_id,
  pa.name AS provider_account_name,
  ac.vendor,
  ac.auth_mode,
  ac.state,
  ac.credential_version,
  ac.access_expires_at,
  ac.refresh_before_at,
  ac.last_refresh_at,
  ac.last_refresh_outcome,
  ac.failure_class,
  ac.failure_count,
  ac.created_at,
  ac.updated_at
FROM account_credentials ac
JOIN provider_accounts pa
  ON pa.tenant_id = ac.tenant_id
 AND pa.id = ac.provider_account_id
 AND pa.deleted_at IS NULL
WHERE ac.deleted_at IS NULL
  AND ac.id > $after_id
  AND ($tenant_id_is_null OR ac.tenant_id = $tenant_id)
ORDER BY ac.id ASC
LIMIT $limit_plus_one
```

硬性禁止 select：

- `ac.encrypted_payload`
- `ac.encryption_scheme`
- `ac.key_id`
- `ac.nonce`
- `ac.aad_hash`
- `ac.payload_fingerprint`
- `ac.refresh_token_fingerprint`
- `pa.credentials`

3. 在 `gatewayhttp` 新增 renew status handler：
   - 可放在 `admin_credentials_handler.go` 或拆 `admin_credential_renew_status_handler.go`。
   - `AdminCredentialStore` interface 增加 `ListRenewStatus`，测试 stub 同步更新。
   - handler 解析 `limit/cursor/tenant_id`，调用 Store，按 `limit+1` 生成 page。
   - routes.go 在 `/admin/v1/credentials` 下同时 mount acquisition helpers 和 renew status read handler。

4. 审计写入：
   - 推荐复用 action `list_account_credentials`，因为白名单已存在。
   - 推荐 target_type 使用 `account_credential`，target_id 为 nil。跨租户 platform_admin 全量读取时 `tenant_id` 为 nil；租户过滤读取时 `tenant_id` 为该租户。
   - payload 只放安全元数据：`scope`、`tenant_id`（如果有）、`count`、`limit`、`has_more`、`cursor_present`，不放 credential id 列表、不放账号名、不放错误原文。
   - 不复用 `writeProviderAccountAudit`，因为它硬编码 `target_type="provider_account"` 且要求单一 target_id，不适合聚合 list。

5. schema 迁移：
   - 预期不需要。新功能是只读查询；复用现有 `list_account_credentials` action 和 `account_credential` target_type 时审计 CHECK 白名单已覆盖。
   - 只有 Owner 强制要求新 action 名，例如 `list_credential_renew_status`，才需要新增迁移扩展 `admin_audit_events_action_check`。本计划不推荐这样做。

## 分页方案

推荐 cursor/keyset，而不是 limit/offset：

- cursor 基于全局单调的 `account_credentials.id`，查询条件 `ac.id > after_id ORDER BY ac.id ASC`。
- 与现有 provider account cursor 形态保持一致：base64 URL-safe opaque cursor，加固定前缀防串用。
- `limit` 默认 100，最大 500；handler 取 `limit+1` 判定 `has_more`。
- 这不是按"最紧急续期时间"排序；一期目标是完整、稳定、无越权的 inventory。前端可在已取结果内按 `refresh_before_at` 或 state 做展示排序。若未来要服务端按 `refresh_before_at` 排序，再独立评估索引和 cursor 复合键。

## 前端改动计划

1. `frontend/lib/api/types.ts`
   - 新增 `CredentialRenewStatus`：复用现有 `CredentialMetadata` 字段，加 `provider_account_name`。
   - 新增 `CredentialRenewStatusList`：`credentials` + `page`。
   - 更新或替换 `AuthCredentialRenewStatus`，避免继续依赖 `account_id/account_name` 这种前端 N+1 拼装字段；页面可用 `provider_account_id/provider_account_name`。

2. `frontend/lib/api/renew.ts`
   - 删除 `listProviderAccounts` 依赖、`listAllProviderAccounts`、`listAccountCredentials`、`mapWithConcurrency`。
   - 新增 `listCredentialRenewStatusPage(cursor?)` 调 `GET /admin/v1/credentials/renew-status`。
   - `listRenewStatus()` 只调用新聚合端点；如果 Owner 接受多页完整读取，则循环同一个聚合端点 cursor，仍然不是 N+1；如果 Owner 要严格一次 HTTP 请求，则用 `limit=500` 并在 `has_more=true` 时向 UI 返回显式 partial 状态，不能静默截断。
   - 403 继续抛 `RenewCredentialsForbiddenError`，文案改为平台管理员或本租户操作员可读，避免误导 tenant_operator。

3. `frontend/app/renew/page.tsx`
   - 新增 Tenant 列，显示 `tenant_id`，例如 `Tenant #7`。若 Owner 确认需要人类可读租户名，再扩展后端 join `tenants`。
   - Account 列改用 `provider_account_name` + `#provider_account_id`。
   - 空态/错误态保持，但如果分页未取完必须显式显示"结果未完整加载"或继续拉完，不能表现成完整列表。

4. OpenAPI/类型来源
   - 更新 `docs/openapi/openapi.yaml` 增加 `GET /admin/v1/credentials/renew-status`、响应 schema、403/401/400/503。
   - 当前 `frontend/lib/api/types.ts` 是手写同步的，实施时与 OpenAPI schema 保持字段一致。

## 测试计划

后端 handler 单元测试：

- platform_admin 不带 `tenant_id`：store 收到 all-tenants scope，不是 tenant 1；响应包含两个不同 tenant 的 rows；审计 tenant_id nil。
- platform_admin 带 `tenant_id=7`：只查租户 7；审计 tenant_id=7。
- tenant_operator 不带 `tenant_id`：只查 `ScopeTenantID`。
- tenant_operator 带其它 `tenant_id`：403，store 不被调用。
- invalid `tenant_id` / `limit` / `cursor`：400。
- unauthorized：401；backend transient：503。
- 审计 payload 不包含账号名、credential id 列表、密文或可关联 fingerprint。

credentialstore 测试：

- seed 两个租户、两个 provider_accounts、多个 account_credentials，确认全租户和单租户过滤均正确。
- 删除的 `account_credentials` 和删除的 `provider_accounts` 不返回。
- 查询结果带 `provider_account_name`。
- 响应结构没有 `encrypted_payload`、`nonce`、`aad_hash`、`payload_fingerprint`、`refresh_token_fingerprint`。
- cursor `AfterID` 和 `limit+1` 行为正确。

前端检查：

- `npm run type-check`。
- `renew.ts` mock/fetch 层如无测试框架，至少通过 type-check 覆盖字段变更。
- 浏览器手工验证 renew 页面列宽和 Tenant 列不挤压现有状态列。

建议执行命令：

- `go test ./backend/internal/credentialstore ./backend/internal/gatewayhttp`
- `npm run type-check`，工作目录 `frontend`
- 若涉及 OpenAPI consistency，按现有仓库脚本或 `go test ./backend/cmd/gateway` 补跑。
- 实施完成并暂存后，按项目规则运行 `codex exec review --uncommitted --full-auto`，再进入提交流程。

## 风险评估

风险等级：中风险。

原因：

- 这是 credential 元数据跨租户读取面。即使没有明文或密文，`vendor/auth_mode/state/refresh_before_at/failure_class` 也会暴露租户的上游账号库存和健康状态。
- 新端点如果鉴权写错，会把多个租户的 operational metadata 泄给租户操作员。

可控因素：

- 端点只读，不修改 credential、provider account、quota、billing、auth core。
- SQL 明确只取元数据列，不读取 `account_credentials` 密文字段、fingerprint 字段，也不读取 `provider_accounts.credentials`。
- platform_admin 全租户读取有审计，且 audit row 使用 `tenant_id=NULL` 表示跨租户 platform action。
- tenant_operator 始终由 `ScopeTenantID` 限制；请求中传其它 `tenant_id` 直接 403。
- 不复用 `resolveProviderAccountAdmin`，因此不会出现静默默认租户 1。
- 分页有上限，避免一次请求在账号很多时拖垮 admin API。

## 子步骤拆解与估时

1. 合成计划确认：30-45 分钟。对比 Claude/Codex 两份独立计划，Owner 确认路径、tenant 显示、分页策略。
2. 后端 Store：60-90 分钟。新增类型、raw SQL、cursor 输入、store 测试。
3. 后端 Handler + Routes + Audit：90-120 分钟。新增 resolver、handler、audit helper、route mount、handler 测试。
4. OpenAPI + 前端 API/types/page：60-90 分钟。移除 N+1，接新字段，加 Tenant 列。
5. 验证与修正：90-120 分钟。跑 Go 测试、前端 type-check、修正失败。
6. per-commit review：30-60 分钟。暂存后运行 Codex review，处理 HIGH/MED finding。

## 需要 Owner 确认

1. 是否确认推荐路径 `GET /admin/v1/credentials/renew-status`。
2. Tenant 列一期是否只显示 `tenant_id`；如果必须显示 tenant name，需要把查询扩展到 `tenants` 表，但仍不需要 schema 迁移。
3. 前端完整加载策略：允许同一聚合端点 cursor 翻页直到完整，还是要求严格一次 HTTP 请求并显示 partial 状态。
4. 审计语义是否接受复用 `list_account_credentials` + `target_type=account_credential` + `target_id=NULL`。若要求新 action 名，则需要一条审计白名单迁移，风险从"无迁移"提高到"低风险 schema 迁移"。

## Owner 中文摘要

本计划基于 HUAKAI 内部源码只读调查起草：真实观察到现有 renew 前端通过 `listProviderAccounts` 触发平台管理员默认租户 1 的漏数问题，也观察到 credential 元数据结构、raw SQL 范式、admin 路由/鉴权和审计白名单现状。合理推断是新增 `GET /admin/v1/credentials/renew-status` 聚合端点可以在不做 schema 迁移的前提下解决问题，前提是新增专用鉴权 resolver、只查元数据列、复用 `list_account_credentials` 审计动作。Open questions 共 4 个：端点路径、Tenant 列是否要租户名、前端是否必须单 HTTP 请求、审计 action 是否接受复用。
