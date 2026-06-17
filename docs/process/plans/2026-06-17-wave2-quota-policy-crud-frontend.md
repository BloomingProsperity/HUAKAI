# Wave2 切片计划 — 配额策略 admin CRUD（前端接线）

日期：2026-06-17 · Lane：Claude PM 自驱 · 风险：低（纯前端接线，接已存在后端 CRUD，无 schema/money/auth 核心改动）

## 选刀理由

Wave2 第一刀（订阅生命周期，PR#14）已合并。本刀挑【最小且未被占用的高价值】：配额策略 CRUD。
查 coordination 板=无 live edits；并行 `wt-proxies-preview`(feat/frontend-admin-proxies) 在做代理池/IA 重排,
配额策略与之无关 → 撞车风险低。后端 `adminquotahttp` CRUD 全 done-active，前端零覆盖（grep 无引用）。
**避开** provider/channel/代理 相邻面以防与并行线程撞；**不动 Sidebar.tsx**（proxies 分支在重排导航树），
新页 `/admin/quota-policies` 直接 URL 可达即可（接线测功能,非追设计）。

## 真契约（已读后端真码 adminquotahttp，禁止凭记忆）

前缀 `/admin/v1/quota-policies`（用户管理 admin 轨,`huakai_admin_token` Bearer）。
鉴权 resolveTenantIdentity：platform_admin 必带 `?tenant_id`;tenant_operator 用自身 scope。
幂等：审计 RequestID 取自 chi 中间件（**非客户端 X-Request-Id** → 前端不造幂等键,不假装）。

| 操作 | 方法+路径 | 体/参 | 响应 |
|---|---|---|---|
| list | GET `/admin/v1/quota-policies?tenant_id&scope_kind&scope_id&metric&enabled&limit&offset` | 过滤可选;limit 默认50封顶100 | `{object,items:[item],limit,offset}` |
| get | GET `/admin/v1/quota-policies/{id}?tenant_id` | — | 裸 item |
| create | POST `/admin/v1/quota-policies?tenant_id` | body | 裸 item(201) |
| update | PUT `/admin/v1/quota-policies/{id}?tenant_id` | body | 裸 item(200) |
| delete | DELETE `/admin/v1/quota-policies/{id}?tenant_id` | 可选 body{reason} | `{object,id,deleted}` |

### 请求体 quotaPolicyRequest + 校验（validate.go 真码）

- `scope_kind` 必填,枚举 {global,user,api_key,channel,pool_group,provider_account}
- `scope_id` 必填(trim 非空,'*' 表 global),≤255 字符
- `metric` 必填,枚举 {requests,tokens_estimated,cost_usd,concurrency}
- `window_kind` 空→默认 fixed;枚举 {none,fixed,calendar_day,calendar_week,manual}
- `window_seconds` *int32;<0 报错;**window_kind=fixed 时必填 >0**
- `limit_value` 必填非负十进制字符串
- `burst_value` *string 非负十进制,缺省 "0"
- `mode` 空→默认 enforce;枚举 {enforce,observe,manual_first,disabled}
- `priority` *int32 缺省 100;`enabled` *bool 缺省 true
- `valid_from` *RFC3339 缺省 now;`valid_until` *RFC3339,设了则**必须晚于 valid_from**
- `reason` 审计可选
- 冲突:create 可 409 quota_policy_conflict(同 scope+metric+window+priority 已有 live);delete 可 409 quota_policy_in_use

item DTO：id,tenant_id,scope_kind,scope_id,metric,window_kind,window_seconds,limit_value(字符串),
burst_value(字符串),mode,priority,enabled,valid_from,valid_until?,created_by_actor?,last_modified_by_actor?,created_at,updated_at。

## 三家对照（specifier lane 实读 ~/refs,§11/§12/§16,融合未抄码）

- **sub2api**(tiebreaker,最接近):有独立配额表但 scope 硬编码(user-per-platform / api-key 字段);metric 仅 USD;
  window 日/周/30d滚动 + 5h/1d/7d 固定;**仅 enforce**;无 priority;无 per-policy 有效期;窗口 lazy-reset。
- **new-api**:配额字段内嵌 token/user/channel 实体,**非独立 policy 对象**;lifetime 无窗口;channel 级 priority;仅 enforce。
- **CLIProxyAPI**:无持久配额策略,运行时 model-quota-exceeded + 配置驱动 fallback(无等价物,源码已证)。

### HUAKAI fusion delta（融合即升级,三维度）

| 维度 | sub2api | new-api | HUAKAI delta | 维度 |
|---|---|---|---|---|
| scope_kind | 3 硬编码 | 3 内嵌 | 6,**独立通用 policy 对象**(非绑实体) | 架构 |
| metric | 仅 USD | 仅 units | 4(requests/tokens/cost/concurrency) | 算法 |
| window_kind | 日/周/固定 | 无(lifetime) | 5 含 none/manual | 架构 |
| mode | 仅 enforce | 仅 enforce | observe(dry-run)/manual_first/disabled——**两家皆无** | 算法+生态 |
| priority / 有效期 / burst | ✗/✗/✗ | channel级/token过期/✗ | 全显式 | 算法/架构 |
| 审计 | 快照 | — | 每次变更原子写 audit 行 | 生态 |

### 诚实 roadmap（Feature Preservation,非静默丢弃；多为后端侧,本前端刀不触）

- sub2api 日历窗口 lazy-reset（后端 reset 机制,本刀不涉）。
- new-api 抽象 units metric + group 继承/覆盖优先级（HUAKAI metric 枚举已含 requests/tokens;group 优先级是后端事）。

## 改动（3 新文件 + 1 测试）

1. **新建 `frontend/lib/api/quota-policy-form.ts`**（零依赖纯逻辑层,可直接 strip-types 单测）：
   枚举常量数组(SCOPE_KINDS/METRICS/WINDOW_KINDS/MODES)+ `validateQuotaPolicyForm`(逐条镜像 validate.go)
   + `buildQuotaPolicyBody`(按 *指针字段语义省略空值)。
2. **新建 `frontend/lib/api/adminQuotaPolicies.ts`**（client）：标准 /admin/v1 助手(adminToken/adminHeaders/
   parse/adminPut/adminDelete/tenantQuery,沿 adminCredentials.ts 同一约定)+ 类型 + list/get/create/update/delete。
3. **新建 `frontend/app/admin/quota-policies/page.tsx`**：列表(过滤 scope_kind/metric/enabled)+ 新建/编辑弹窗 + 删除。

## 强测试（CLAUDE.md §14,变异验证）

`frontend/lib/api/quota-policy-form.test.ts`：
- 直接单测纯逻辑(判别 fixture):枚举守门、scope_id 必填/≤255、fixed 需 window_seconds>0、limit 非负必填、
  burst 非负、valid_until>valid_from、builder 省略语义。
- 源文本接线断言 adminQuotaPolicies.ts:各端点路径(锚定闭合定界符)+ PUT/DELETE 方法 + tenantQuery。
- 每条 mutation 实测转红再还原（端点路径断言锚定定界符避免尾部追加 typo 非判别）。

## 成功判据

- `tsc --noEmit` 干净;`node --test` 全绿;每测变异红验证。
- 开 PR squash 合并入 feat/frontend-portal,清 worktree + 释放 coordination 锁。

## blast radius / 风险

- 纯前端、低风险;不碰后端/schema/auth 核心;不动 Sidebar（防与 proxies 分支撞）。
- 浏览器实操(真打端点 + admin token)需部署后手测;逻辑层用单测+源文本接线兜住。
- follow-up(可选):新页登记进 Sidebar 导航树（待 proxies 分支 IA 落地后统一加,避免现在撞）。
