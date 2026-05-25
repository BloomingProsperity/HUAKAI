# /renew 方案 B — 跨租户续期状态聚合端点 · Claude 计划

> 平行计划之一(CLAUDE.md #10)。Claude 独立调查后起草, 未参考 codex 版本。
> 配对文件: `2026-05-20-renew-aggregate-endpoint-codex.md`。
> 触发: Owner 2026-05-20 在 /renew 修复中拍板方案 B。

## 背景

/renew 接真实数据(方案 A)后, codex review 发现 P1: 前端靠 `listProviderAccounts`
+ 逐账号 credentials 拼装, 而 `listProviderAccounts` 对平台管理员经
`resolveProviderAccountAdmin` 静默默认 scope 到租户 1(`defaultAdminProviderAccountTenantID=1`)
—— /renew 对平台管理员只显示租户 1、静默漏掉其它租户, 违反 HUAKAI 透明原则。

方案 B: 新增一个后端**跨租户聚合只读端点**, 平台管理员一次拿到全租户凭据续期状态,
同时消除前端 N+1。

## 范围

- 后端: 1 个新只读端点 + 1 个新 credentialstore 查询方法; 无 schema 迁移。
- 前端: `renew.ts` 改为单次调新端点; `types.ts` / `page.tsx` 加 tenant 维度。

## 后端设计

### 端点

`GET /admin/v1/auth-credentials/renew-status`

- **鉴权按角色分流**(关键 —— 不重蹈"静默默认租户1"):
  - `platform_admin`(无 scope tenant)→ 返回**全部租户**的凭据续期状态。
  - `platform_admin`(有 scope tenant)/ `tenant_operator` → 仅返回该 scope 租户。
  - 其它角色 → 403。
  - 不调用 `resolveProviderAccountAdmin`(它会默认租户1); 直接用
    `d.Auth.Resolve` 拿 identity 后自行按 role + ScopeTenantID 分流。
- 查询参数: `limit`(默认 100, 上限 500)+ `cursor`(分页)。
- 响应: `{ items: [...], next_cursor: string|null }`, 每个 item:
  `tenant_id / account_id / account_name / vendor / auth_mode / state /
  credential_version / access_expires_at / refresh_before_at / last_refresh_at /
  last_refresh_outcome / failure_class / failure_count`。
- **plaintext-free**: 查询只 select 元数据列, 绝不触及 `encrypted_payload` /
  `nonce` / `aad_hash` / `key_id` / `refresh_token_fingerprint`。

### credentialstore 新方法

`Store.ListRenewStatus(ctx, ListRenewStatusParams) ([]RenewStatusRow, error)` ——
沿用 `ListByAccount` 的 raw SQL 范式:

```sql
SELECT ac.tenant_id, ac.provider_account_id, pa.name AS account_name,
       ac.vendor, ac.auth_mode, ac.state, ac.credential_version,
       ac.access_expires_at, ac.refresh_before_at, ac.last_refresh_at,
       ac.last_refresh_outcome, ac.failure_class, ac.failure_count, ac.id
FROM account_credentials ac
JOIN provider_accounts pa ON pa.id = ac.provider_account_id
WHERE ac.deleted_at IS NULL
  AND ($1::bigint IS NULL OR ac.tenant_id = $1)   -- 租户过滤(平台全局时传 NULL)
  AND ($2 IS NULL OR (ac.updated_at, ac.id) < ($3, $4))  -- cursor
ORDER BY ac.updated_at DESC, ac.id DESC
LIMIT $5
```

- cursor 用 `(updated_at, id)` 复合游标(稳定、无 offset 漂移)。
- 租户过滤参数: 平台全局传 NULL, 否则传 scope tenant id。

### 审计

复用既有 `list_account_credentials` 审计动作(0047 白名单已含), payload 记
`{scope: "all"|tenant_id, count}`。无需新迁移、无需动 CHECK 约束。

### 路由挂载

在 `routes.go` 既有 `/admin/v1` 段新增挂载, 依赖注入沿用 `AdminCredentialDeps`
(已有 `Auth` / `Credentials` / `AuditStore`)。

## 前端改动

- `renew.ts`: 删除 `listProviderAccounts` + 逐账号 N+1 + `mapWithConcurrency`;
  改为单次 `apiGet('/admin/v1/auth-credentials/renew-status', {limit, cursor})`。
  403 处理(平台管理员权限)保留。N+1 / 并发上限 / 租户1 默认问题随之全部消失。
- `types.ts`: `AuthCredentialRenewStatus` 增 `tenant_id`; 移除 N+1 相关中间类型。
- `page.tsx`: 表头加 `Tenant` 列; 其余(8 态、英文表头、禁用按钮+自动续期标注)不变。
  分页: 首版可"加载更多"按钮或一次拉默认 100, 视数据量。

## 子步骤拆解

| 步骤 | 内容 | 估时 |
|---|---|---|
| B-1 | credentialstore `ListRenewStatus` 方法 + raw SQL + 单元测试 | 0.5 天 |
| B-2 | handler + 角色分流鉴权 + cursor 分页 + 审计 + 路由挂载 | 0.5 天 |
| B-3 | handler 测试(平台全局 / 租户操作员 scoped / 403 / 分页) | 0.5 天 |
| B-4 | 前端 renew.ts/types.ts/page.tsx 切到新端点 + tenant 列 | 0.5 天 |
| B-5 | 后端 `go build` + `go test`、前端 `npm run build`、codex review | 0.25 天 |

合计约 1.5–2 天。提交: 后端 1 commit(`gatewayhttp 跨租户续期状态聚合端点`)+
前端 1 commit(`frontend renew 页面接聚合端点`)。

## 成功标准

- 平台管理员调端点 → 看到**全部租户**的凭据续期状态, 不再静默限于租户 1。
- 租户操作员 → 仅看到自己租户(不越权)。
- 响应 plaintext-free(无任何密文/指纹字段)。
- 前端 /renew 单次请求拿全量, 无 N+1; codex review P1/P2 全清。
- `go build ./...` + 相关 `go test` + `npm run build` 全绿。

## Blast radius / 可能出错点

- 新增端点, 不改既有端点、不改 schema、不动 auth 核心 —— 影响面小。
- 风险点: 角色分流写错会造成跨租户越权(租户操作员看到别人租户)→ B-3 必须有
  租户操作员 scoped 测试。
- cursor 分页边界(updated_at 相同 id 不同)→ 复合游标已规避。
- provider account 被删但 credential 残留 → JOIN 用 INNER JOIN, 自然排除;
  若需展示孤儿凭据再议(默认不展示)。

## 风险评估

中风险: 凭据**元数据**跨租户只读。可控理由 —— (a)只读, 不写; (b)plaintext-free,
不返回任何密文/密钥/指纹; (c)不改 schema、不改鉴权核心; (d)角色分流有专门测试。
按 CLAUDE.md 风险规则属"中风险实现支持, 记录理由后可进行"。

## 需 Owner 确认的点

1. 端点对 `tenant_operator` 是否也开放(返回其租户子集)—— 我推荐开放, 否则租户
   操作员的 /renew 页面没数据。
2. 分页首版: "加载更多"按钮 vs 一次 100 条 —— 我推荐先一次 100 + cursor 预留。
3. 是否展示孤儿凭据(provider account 已删)—— 我推荐默认不展示。
