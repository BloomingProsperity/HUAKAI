# 跨模块接入协作逻辑审计 — 2026-06-16

**触发:** Owner 质疑「别的功能模块接入协作逻辑是不是也没摸清楚」(代理↔账号绑定缺口被 Owner 一句话点出后)。
**方法:** workflow `cross-module-wiring-audit`(19 agents,1.27M tokens,852 tool-uses,~17min)。每个跨模块集成点:先 clean-room specifier lane 追三家(sub2api/new-api/CLIProxyAPI)的接线逻辑(数据模型→写入路径→请求时解析),再审 HUAKAI 全链(DB 列 / 后端写路径 / UI / 请求时解析)是否端到端贯通。代理绑定作校准项(回来确为 partial-写路径缺,验证方法成立)。
**结论一句话:** 代理那个缺口**不是孤例,是个反复出现的结构性模式** —— DB schema + 请求时 resolver 装好了,但 admin 写路径(SQL mutation + HTTP 端点 + 前端表单)缺一段或全缺(「能力建了却够不着」)。#1–#4 是这个模式(且高度可合并),#5–#6 更重(真·逻辑回路缺失),#7 纯前端收尾,#8–#9 已贯通不用碰。

---

## 集成点判定总表(够不着的排最前)

| # | 集成点 | 判定 | Gap | 修法 | 严重度 |
|---|--------|------|-----|------|--------|
| 1 | 模型映射 / 分组定价倍率 | **inert_capability_gap** | 映射列有 schema+resolver 但**零**写端点+**零** UI;定价倍率后端 `PUT /{pool_group_id}` 完整但前端无面板 | 建 model_pool_bindings CRUD(含 override)+ `/admin/models/bindings` 页;前端补 `/admin/settings/pricing` 调已有 PUT | high |
| 2 | Account↔Proxy 绑定 | **partial(写路径 missing)** | resolver 正确读 proxy_id+JOIN,但无 SQL UPDATE、无绑定端点、前端无代理选择控件 | 加 `UPDATE provider_accounts SET proxy_id/proxy_group_id`;`PATCH /provider-accounts/{id}/proxy`;账号编辑加三档选择器 | high |
| 3 | Account↔Pool/Group↔Routing | **partial(model→pool 写 API+UI missing)** | DB+resolver+account 写 API 齐,缺 model_pool_bindings 写 API(仅 seed 裸 INSERT);前端 bindings 页自承"需后端接口" | 加 model_pool_bindings_admin SQL+handler(原子 bump snapshot.version);前端每 pool 下 model 分配表单 | high |
| 4 | Credential↔account / proxy_id 写路径 | **partial(与 #2 同源)** | account create/update 请求体缺 proxy_id 字段,Insert 无 proxy_id 参数 | 与 **#2 并案**:请求体加 proxy_id + update PATCH + 前端 modal 选择器 | high |
| 5 | Channel-health↔routing 反馈 | **partial(手动通道+反馈环 missing)** | 缺手动 set health_state 的 API+UI(clear-rate-limit 不重置 health_state);缺 channel→account 自动降级回路(channelhealth 服务孤立,无 provider_accounts FK,只喂 dashboard) | (A) `POST /provider-accounts/{id}/set-health-state`(尊重 Transition 终态保护+审计)+ UI;(B) sync worker:通道 disabled/degraded → 该通道账号施加冷却 | high |
| 6 | Group/Tier↔User↔APIKey 作用域 | **partial(4 子缺口)** | (1) api_keys 无 group_id(同用户所有 key 共享分组);(2) 订阅升降级回路断(有快照列无更新 service/无到期降级 worker/无前端订阅页);(3) `/v1/admin/routes` 后端全但前端无 UI;(4) SetUserGroupForTenant 直 UPDATE 无事务、无升级白名单 | (1) api_keys 加 group_id/allowed_groups;(2) 写订阅激活 service+到期降级 worker+前端订阅页;(3) 前端 `/admin/routes` 页;(4) 改事务性+routes 可用性校验 | high |
| 7 | 内容审核 关键词+哈希 配置 | **wired_end_to_end(缺 CRUD UI)** | 后端 DB→内存→请求路径执行全贯通;前端只暴露 config/logs/banned,缺关键词/哈希 CRUD 表单 | 前端加 Keywords/Hashes Section + adminSystem.ts 包装函数(后端 routes 已齐) | medium |
| 8 | Subscription↔Quota↔Request | **wired_end_to_end** | 无 gap(plans/subscriptions/policy_links + AssignSubscription→installCapsTx + AssignModal + Reserve 强制全贯通) | 无需修复 | low |
| 9 | Payment/Topup↔Balance↔Quota | **wired_end_to_end** | 无 gap(orders/credits/balances/holds + admin 确认→CompleteFulfill + billing 页 + ClaimGate.Reserve→402 全贯通) | 无需修复 | low |

---

## 模式归纳 + 去重(关键)

**#1–#6 共享同一结构性缺陷:** DB schema 装了、请求时 resolver/读路径装了,但 admin 写路径(SQL mutation + HTTP 端点 + 前端表单)缺一段或全缺。

**可并案去重(4 条 high → 2 个工单):**
- **#2 ≡ #4**:同一个 proxy_id 写路径缺口的两份判定,**并案一次做**。
- **#1 ∩ #3**:同一张 model_pool_bindings 表的同一个写 API 缺口的不同字段视角(#3 看 priority/weight/fallback,#1 看 provider_model_id_override),**合并为"建一个 model_pool_bindings admin handler,字段一次配齐"**。

**#5 / #6 比"够不着"更重:** 多了真正的逻辑回路缺失(channel→account 降级环、订阅升降级环),工作量更大、风险更高,须单独排期+测试。

---

## 建议修复顺序

**第 0 步 — 并案去重(不写码):** #2≡#4(proxy_id 写路径)、#1∩#3(model_pool_bindings 写 API)各合并成单一工单。

**第 1 批 — 一次写路径补齐(高收益、低逻辑风险:读路径已正确,只补写路径):**
1. **model_pool_bindings admin handler**(合并 #1+#3):一个 admin SQL + 一个 handler,字段一次配齐,原子 bump `model_registry_snapshots.version`;前端 bindings 编辑页。→ 同时关 #1+#3 两条 high。
2. **proxy_id 写路径**(合并 #2+#4):SQL UPDATE + `PATCH .../proxy` 端点 + 请求体加字段 + 前端账号编辑三档选择器(对齐 sub2api 单 FK + HUAKAI 组轮换 delta)。
3. **定价倍率前端面板**(#1 剩余):纯前端,调已有 PUT,半天。

**第 2 批 — 补真·缺失的运维回路(逻辑重,需测试):**
4. #5 手动 set-health-state 端点+UI(先做,给运维急停手段,接 Transition 终态保护+审计)。
5. #5 channel→account 降级 sync worker(后做,跨模块写,需 dispatcher 集成测试)。
6. #6 分级落:先 (3) 前端 routes 页(后端已全) → (4) SetUserGroupForTenant 改事务 → (2) 订阅激活/到期降级 service+worker(最重) → (1) api_keys 加 group_id(改鉴权链,影响面最大,配迁移单独排,放最后)。

**第 3 批 — 收尾 UI(低风险):** 7. #7 审核关键词/哈希 CRUD UI(纯前端)。

**不动:** #8、#9 已端到端贯通。

---

> 给 Owner 一句话:真正"能力建了够不着"的是 #1–#4,且高度可合并——**两个写路径工单就能清掉 4 条 high**;#5、#6 是更重的"回路缺失",单独排期+测试;#7 纯前端收尾;#8、#9(订阅/支付/配额/余额这些钱路)实际已贯通,不用碰。
