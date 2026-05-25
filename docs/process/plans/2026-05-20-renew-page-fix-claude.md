# /renew 页面修复 — Claude 计划

> 平行计划之一(CLAUDE.md #10)。Claude 独立调查后起草, 未参考 codex 版本推理过程。
> 配对文件: `2026-05-20-renew-page-fix-codex.md`。
> 触发: Owner 2026-05-20 解冻前端, 指派 codex 执行 /renew 修复; 要求双方各起草计划交叉讨论。

## 背景与现状

`frontend/app/renew/page.tsx`(141 行)+ `frontend/lib/api/renew.ts`(53 行)是
"面板 5 — Auth Credential Renew 状态"。当前**整页 mock**:
- `renew.ts` 的 `listRenewStatus()` / `triggerRenew()` 返回硬编码 `MOCK_DATA`。
- 页面显式打 `MOCK` 紫标 + 说明文字 "后端尚未实现 GET /admin/v1/auth-credentials/{id}/renew-status"。

### 后端真实能力(已核实)

后端**已有完整的 credential 续期数据模型**, 不是空白:
- 表 `account_credentials`(迁移 0016)字段: `state`、`last_refresh_at`、
  `refresh_before_at`、`last_refresh_outcome`、`failure_class`、`failure_count`、
  `access_expires_at`、`next_attempt_at`。注释明确 "OCAW-34: refresh scheduler
  扫描 refresh_before_at" —— **自动续期调度器是既有功能**。
- `credential_audit_events` 有 `credential_refresh_succeeded` / `credential_refresh_failed`。
- 既有端点 `GET /admin/v1/provider-accounts/{id}/credentials` 返回
  `{credentials: [CredentialMetadata]}`, `CredentialMetadata` JSON **恰好暴露**
  页面所需全部字段: `state` / `last_refresh_at` / `refresh_before_at` /
  `last_refresh_outcome` / `failure_class` / `failure_count` / `access_expires_at`。
- 既有端点 `POST .../credentials/{credentialID}/rotate` —— **手动轮换, 需提交新凭据**,
  不等于"一键续期"。

### 缺口

1. 没有"跨所有账号聚合续期状态"的端点 —— 列出全部需 N+1(先列账号, 每账号再列凭据)。
2. 没有"立即触发续期"的动作端点 —— 续期由调度器自动做, 无手动 trigger。
3. 页面表头中英混排("操作"是中文); 前端 `renew_status` 枚举只有 idle/renewing/failed
   三值, 而后端 `state` 有 8 值(active/refreshing/refreshing_with_grace/expired/
   temp_unschedulable/needs_rotation/revoked/operator_attention)—— 三值映射会丢信息。

## 方案选项

### 方案 A — 纯前端接线到既有端点(推荐主体)

`renew.ts` 去 mock: `listRenewStatus()` 调 `listProviderAccounts()` → 对每个账号
调 `GET /provider-accounts/{id}/credentials` → 展平凭据, 直接用 `CredentialMetadata`
真实字段。一行一个**凭据**(不是一行一账号 —— 每个凭据按 vendor/auth_mode 独立续期,
账号聚合会掩盖单凭据失败)。

- 优点: 不碰后端、不碰 credential core 高风险路径; 立即移除 mock, 展示真实状态;
  与 Go 是否冻结无关。
- 代价: N+1 请求(账号数量级, admin 面板可接受); 表头/枚举需重做。

### 方案 B — 新增后端端点

- B1(中风险, 只读): 新增聚合端点 `GET /admin/v1/auth-credentials/renew-status`,
  一次 join 返回全部, 消除 N+1。Go backend 本 session 已有 case C 大量改动 ——
  冻结实际已松, B1 可行, 但仍需独立小计划。
- B2(高风险): 新增真实 `POST .../renew` 触发端点。触碰 credential refresh /
  secret handling, 属 CLAUDE.md 高风险项, **必须单独计划 + Owner 确认**, 不并入本批。

### 方案 C — 保留 mock, 仅英文表头

最小改动, 但不交付真实价值, 且违反 HUAKAI"透明 / 商家不能做假"核心差异
(`project_core_trust_chain_differentiator`)—— 一个假装展示真实数据的 mock 面板。**不推荐**。

## Claude 推荐

**A 为本批主体 + B1 进 roadmap + B2 单独高风险立项。**

关键补充(与 codex 计划的潜在差异点):

1. **"Trigger Renew" 按钮不能留成假按钮**。HUAKAI 核心差异是"商家不能做假 / 链路透明"。
   一个看起来能点、点了却无真实后端动作的按钮, 本身就是"掺水"。本批必须二选一:
   - 删除该按钮; 或
   - 禁用并改为诚实文案(如 "自动续期 · 由调度器管理"),
   并在页面注明真实手动触发待 B2。**不要把 `rotate` 冒充成 renew**(rotate 要新凭据)。
2. **状态展示用后端真实 `state`(8 值), 不要硬压成 3 值**。前端枚举扩展为与后端一致,
   或加一张清晰的 state→颜色/标签映射表; 透明优先于"好看的三色灯"。
3. 行模型: 一行一个凭据(account + vendor + auth_mode), 不做账号级聚合。

## 子步骤拆解(方案 A)

| 步骤 | 内容 | 估时 |
|---|---|---|
| A-1 | `types.ts`: `AuthCredentialRenewStatus` 重定义为对齐 `CredentialMetadata` 真实字段; `RenewStatus` 扩成后端 8 态(或加映射表) | 0.5–1 hr |
| A-2 | `renew.ts`: 删 `MOCK_DATA`; `listRenewStatus()` 用 `listProviderAccounts` + 逐账号 `listAccountCredentials` 拼装; 删 `triggerRenew` 或改成 no-op-with-note | 1–2 hr |
| A-3 | `page.tsx`: 去 MOCK 紫标与说明; 行模型改一行一凭据; 表头英文化; 按钮按上文处理; 真实 state 渲染 | 2–3 hr |
| A-4 | 本地 `npm run build` / lint 通过; 截图自检; 无后端时空列表与错误态可读 | 0.5–1 hr |

合计约 0.5–1 天, 1 commit(`frontend renew 页面接真实凭据状态`)。

## 表头英文化(旧 → 新)

`account_id`→`Account ID` · `account_name`→`Account` · (新)`vendor`→`Vendor` ·
(新)`auth_mode`→`Auth Mode` · `last_renew_at`→`Last Refresh` ·
`next_renew_at`→`Refresh Before` · `status`→`Status` · `error_msg`→`Last Error` ·
`操作`→`Actions`。

## 成功标准

- 页面展示**真实** credential 续期状态, 无 `MOCK_DATA`、无 MOCK 紫标。
- 表头全英文。
- 无假按钮: 触发动作要么真实、要么明确标注不可用。
- `npm run build` 通过; 后端不可达时优雅降级(错误条, 不白屏)。
- 不新增依赖; 不碰 `.go` / schema / auth / billing。

## Blast radius / 可能出错点

- 仅 3 个前端文件(`page.tsx` / `renew.ts` / `types.ts`)。零后端、零 schema。
- N+1: 账号多时首屏慢 —— 接受, B1 解决。
- `CredentialMetadata` 是凭据级、页面原是账号级 —— 行模型必须改, 否则字段对不齐。
- 前端处于"解冻"首个改动, 注意 Next.js 构建与既有 admin 页风格一致。

## 需 Owner 决策的点

1. 接受 A 作为本批主体吗?
2. "Trigger Renew" 按钮: **删除** / **禁用+诚实文案** —— 选哪个?
3. B1(聚合只读端点)进 roadmap 即可, 还是本批一起做(需 Go 改动小计划)?
4. B2(真实触发续期端点)确认为独立高风险立项、暂不做?

## 交叉讨论待办

与 `2026-05-20-renew-page-fix-codex.md` 对比 agreements / conflicts / gaps,
合成单一方案后再交 Owner 定 A/B/C 与按钮处理。
