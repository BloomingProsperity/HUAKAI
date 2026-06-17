# Wave2 切片计划 — 渠道测试模板 admin CRUD（前端接线）

日期：2026-06-17 · Lane：Claude PM 自驱 · 风险：低（纯前端接线，接已存在后端 CRUD，无 schema/money/auth 核心改动）

## 选刀理由

Wave2 已合并订阅生命周期(#14)、配额策略(#15)、运维数据面(#16)。本刀挑隔离 CRUD=渠道测试模板。
查证：coordination 板无 live edits；并行 proxies 分支(feat/frontend-admin-proxies)仅动 credentials/proxies 页 +
Sidebar，**不触** channel-test-templates → 撞车风险消除。后端 adminhttp CRUD done-active，前端零覆盖。不动 Sidebar.tsx，
新页 `/admin/channel-test-templates` URL 可达即可。

## 真契约（实读后端真码 channel_test_template_handler.go，禁止凭记忆）

前缀 `/admin/v1/channel-test-templates`（管理 token 轨）。鉴权 resolveChannelTestTemplateAdmin：platform_admin 或
tenant_operator。租户 parseAdminCatalogTenant：platform_admin 必带 `?tenant_id`(否则 400 tenant_id_required)，
tenant_operator 用 ScopeTenantID。分页 parseAdminCatalogPage：limit 默认 50 范围 1-500，offset 默认 0 ≥0。

| 操作 | 方法+路径 | 体/参 | 响应 |
|---|---|---|---|
| list | GET `/admin/v1/channel-test-templates?tenant_id&limit&offset` | — | `{object,items,limit,offset}` |
| get | GET `/admin/v1/channel-test-templates/{id}?tenant_id` | — | 裸 item |
| create | POST `/admin/v1/channel-test-templates?tenant_id` | body | 裸 item(201) |
| update | PUT `/admin/v1/channel-test-templates/{id}?tenant_id` | body | 裸 item(200) |
| delete | DELETE `/admin/v1/channel-test-templates/{id}?tenant_id` | — | `{object,id,deleted}` |

### 请求体 channelTestTemplateRequest + 校验（validateChannelTestTemplateRequest 真码）

- `name` 必填 trim 非空且 ≤128 字符（否则 invalid_template_name）。
- `method` upper(trim)，必须 ∈ {GET,POST,PUT,PATCH,DELETE}（否则 invalid_template_method）。
- `path` trim，必须以 `/` 开头且 ≤2048（否则 invalid_template_path）。
- `headers` 可选 JSON 对象；非对象 → invalid_template_headers；**含凭证头**（authorization / proxy-authorization /
  cookie / x-api-key / api-key / x-auth-token，大小写不敏感）→ credential_header_not_allowed。空 → `{}`。
- `body_template` 自由字符串（无校验）。
- 冲突：409 channel_test_template_name_conflict（uq_channel_test_templates_tenant_name，租户内 name 唯一）。

item DTO：id, tenant_id, name, method, path, body_template, headers(JSON 对象), created_at。

## 三家对照（specifier lane 实读 ~/refs，§11/§12/§16，融合未抄码）

- **sub2api**(tiebreaker)：有可复用渠道监控请求模板（channel-monitor-templates CRUD + apply-to-monitors 快照 + 定时探测）；
  头部黑名单挡的是 **HTTP 层头**（host/content-length/transfer-encoding 等），**非凭证头**。
- **new-api**：无模板存储；test payload 按 endpoint/model 模式**硬编码**；手动 `GET /channels/test/:id` + 定时 AutoTest。
- **CLIProxyAPI**：无渠道测试模板/端点（凭证代理，无等价物，源码已证）。

### HUAKAI fusion delta（融合即升级）

| 维度 | sub2api | new-api | HUAKAI delta | 维度 |
|---|---|---|---|---|
| 模板对象 | provider/api_mode/body_override 形 | 无（硬编码探测） | 通用 HTTP 请求形（method 白名单 + path 前缀 + body_template + headers） | 架构 |
| 头部守门 | 挡 HTTP 层头(host/content-length) | 无（头不可配） | **挡凭证头(authorization/x-api-key/cookie…)→防密钥写入测试配置** | 生态-安全 |
| 作用域 | 每账号每渠道 monitor | 每渠道 TestModel | 每租户可复用模板 + name 租户内唯一 | 架构 |

### 诚实 roadmap（Feature Preservation；后端当前仅模板 CRUD，运行/调度未接 → 前端无法接，登记）

- sub2api apply-to-monitor 快照 + 定时周期执行；new-api 一键「测全部渠道」/立即跑。
- 「用模板对渠道 X 立即测一次」运行端点（HUAKAI 后端当前只有模板 CRUD，无 run 端点）。

## 改动（3 新文件 + 1 测试）

1. **新建 `frontend/lib/api/channel-test-template-form.ts`**（零依赖纯逻辑）：常量 TEMPLATE_METHODS/CREDENTIAL_HEADER_NAMES
   + validateChannelTestTemplateForm（逐条镜像后端）+ parseHeadersField（JSON 对象 + 凭证头拒绝）+ isCredentialHeaderName
   + buildChannelTestTemplateBody（method 大写、name trim、headers 解析为对象）。
2. **新建 `frontend/lib/api/adminChannelTestTemplates.ts`**（client）：标准 /admin/v1 助手(adminPut/adminDelete/tenantQuery,
   沿 adminCredentials.ts 约定) + 类型 + list/get/create/update/delete。
3. **新建 `frontend/app/admin/channel-test-templates/page.tsx`**：列表(租户+分页) + 新建/编辑弹窗(method 下拉 + path + body_template
   textarea + headers JSON textarea) + 删除。

## 强测试（CLAUDE.md §14，变异验证）

`frontend/lib/api/channel-test-template-form.test.ts`：
- 直接单测纯逻辑（判别 fixture）：name 必填/≤128、method 白名单(且小写输入合法→大写)、path 前缀/≤2048、
  **headers 凭证头拒绝**(authorization/x-api-key 大小写不敏感)、非对象 headers 拒绝、builder method 大写/headers 对象化。
- 源文本接线断言 adminChannelTestTemplates.ts：五端点路径(锚定定界符) + PUT/DELETE 方法 + tenantQuery + builder 使用。
- 每条 mutation 实测转红再还原（路径锚定定界符 + 切下一顶层声明边界）。
- ultracode：实现后跑 adversarial-review workflow 多 agent 对抗核验（契约保真/测试判别/clean-room/凭证头守门/假绿）。

## 成功判据

- `tsc --noEmit` 干净；`node --test` 全绿；每测变异红验证；adversarial review 无 S0/S1。
- 开 PR squash 合并入 feat/frontend-portal，清 worktree + 释放 coordination 锁。

## blast radius / 风险

- 纯前端、低风险；不碰后端/schema/auth；不动 Sidebar。
- 凭证头守门前端镜像后端（防御纵深：连传都不传密钥头）；浏览器实操需部署后手测，逻辑层单测+源文本接线兜住。
- follow-up：新页登记进 Sidebar（待 proxies IA 落地统一加）；模板「运行/调度」待后端 run 端点。
