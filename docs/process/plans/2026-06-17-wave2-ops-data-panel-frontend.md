# Wave2 切片计划 — 运维数据面（audit-events / DLQ / cache-L2）前端接线

日期：2026-06-17 · Lane：Claude PM 自驱（ultracode：understand workflow 5 agent 已并行调研）· 风险：低（纯前端接线，接已存在后端，无 schema/money/auth 核心改动）

## 选刀理由

Wave2 已合并订阅生命周期(PR#14)、配额策略(PR#15)。本刀挑隔离可观测性面=运维数据面，
不撞 channel/proxy（并行 wt-proxies-preview）。后端三面 done-active，前端零覆盖（无 adminAudit/DLQ/cache 模块）。
不动 Sidebar.tsx（proxies 分支 IA 重排），新页 `/admin/ops-data` URL 可达即可。

## 真契约（understand workflow 实读后端真码，禁止凭记忆）

### 1. 审计事件 GET /admin/v1/audit-events（管理 token；platform_admin 跨租户/tenant_operator 自 scope）
- 过滤：tenant_id / from,to(RFC3339) / event_class(billing|pool_routing|rate_limit|oauth_refresh) /
  event_type / severity(info|warning|error) / ledger_id / actor_id / limit(1-200,默认100) / cursor(opaque keyset)。
- 响应：`{items:[AuditEvent], next_cursor:string|null, total:int64}`。keyset 游标分页（非 offset），cursor 不透明 base64。
- AuditEvent：id,tenant_id,event_class,event_type,severity,ledger_id,claim_id?,provider_account_id?,
  pool_group_id?,request_id?,actor_id?,actor_role?,reason?,payload(object),created_at。
- 守门：invalid_limit(1-200)、invalid_tenant_id、invalid_cursor(空 cursor= 也拒)、invalid_from/to。只读无幂等键。

### 2. DLQ GET /admin/v1/dlq/{handler}（**platform_admin only**，不按租户过滤，跨租户可见）
- {handler}=EventKind 枚举 9 值：usage_record,billing_event_replica,audit_event_replica,audit_mismatch_refund,
  audit_ledger_entry,account_health,metrics,post_delivery_settlement,cost_receipt_append。**非法名静默 0 行不报错**→前端先白名单守门。
- list 查询：limit(1-200,默认100)、status?(pending|inflight|delivered|operator_review|dlq|quarantined,空=全部)。
- list 响应：`{items:[DLQRecord]}`。DLQRecord：id,tenant_id,claim_id?,event_kind,lane(HIGH|MED|LOW),status,
  payload,failure_reason,failure_at,replay_attempts,last_replay_at?,replayed_at?,replay_failure_reason?,
  next_retry_at,lease_owner?,lease_until?,replica_status,replica_target,replica_committed_at?,idempotency_key,
  source_table,source_id?,operator_review_at?。
- replay POST /admin/v1/dlq/{id}/replay（id 正 int64）→ `{item:DLQRecord, replayed:true}`。守门：invalid_dlq_id(非正)、
  404 dlq_not_found、409 dlq_handler_missing、503 dlq_replay_failed。**不幂等、无客户端 X-Request-Id**（前端不假造）。
- usage-record-dlq/{id}/replay：同一 handler 同契约（别名）。

### 3. 缓存 L2 GET /admin/v1/cache/l2/stats + DELETE /admin/v1/cache/l2/{key}（platform_admin 或 tenant_operator 限 scope）
- stats（无参）：`{enabled:bool, size_bytes, max_size_bytes, ttl_seconds, entries:[EntryStats], metrics:{"vendor=V,model=M":{hit_total,miss_total,size_bytes}}}`。
  EntryStats：key,tenant_id,vendor,model,status(int),size_bytes,stored_at,expires_at。tenant_operator：entries 过滤本租户 + metrics 抹空。
- delete /{key}（**key 须 URL 编码**，可含 / :）→ `{key, deleted:bool}`。404 cache_l2_not_found（禁用或不存在）、403 admin_forbidden（scope）。幂等。

## 三家对照（understand workflow specifier lane 实读 ~/refs，§11/§12/§16，融合未抄码）

- **sub2api**：indexed system-logs（GET /api/v1/admin/ops/system-logs，富过滤）为审计 tiebreaker；**无 DLQ replay**；**无 cache purge 端点**（仅 dashboard cache stats）。
- **new-api**：audit 中间件自动记 40+ 动作；cache admin 最强（stats + 清 disk_cache + 强制 GC）；**无 DLQ**。
- **CLIProxyAPI**：无审计、无 DLQ；部分=日志文件管理（取/删日志）。

### HUAKAI fusion delta（融合即升级）

| 面 | sub2api | new-api | HUAKAI delta | 维度 |
|---|---|---|---|---|
| audit | 富过滤 system-logs | 中间件自动记 | 多事件类(billing/pool/rate/oauth)统一面 + **keyset 游标分页**(非 offset) | 架构 |
| DLQ replay | 无 | 无 | **两家皆无的死信队列查看 + 逐条 replay**(运维恢复面) | 生态 |
| cache | 仅 stats | stats+全清+GC | 逐条 tenant-scoped 检视 + **按 key 选择性清除**(非全清) | 算法 |

### 诚实 roadmap（Feature Preservation，后端当前无→前端无法接，登记）

- new-api 的全量清缓存 + 强制 GC（HUAKAI cache L2 当前仅按 key 删 + stats）。
- sub2api 的 saved-filters / 审计批量导出。

## 改动（3 新文件 + 1 测试）

1. **新建 `frontend/lib/api/ops-data-form.ts`**（零依赖纯逻辑，可直接 strip-types 单测）：
   枚举常量(EVENT_CLASSES/SEVERITIES/DLQ_EVENT_KINDS/DLQ_STATUSES)+ `buildAuditEventsQuery`(省略空过滤 + 限额钳制)
   + `clampAuditLimit` + `isValidEventKind`(白名单守门) + `encodeCacheKey`(URL 编码 delete 路径段) + `validateDlqId`。
2. **新建 `frontend/lib/api/adminOpsData.ts`**（client）：标准管理 token 助手 + apiGet/apiPost + adminDelete + 类型
   + listAuditEvents/listDLQ/replayDLQ/replayUsageRecordDLQ/getL2CacheStats/evictL2CacheKey + 展示辅助（severity/status 徽章）。
3. **新建 `frontend/app/admin/ops-data/page.tsx`**：3 tab（审计事件 / DLQ / 缓存 L2）。审计=过滤+游标翻页表；
   DLQ=选 handler+status 列表+逐条重放；缓存=stats 卡 + entries 表 + 按 key 清除。

## 强测试（CLAUDE.md §14，变异验证）

`frontend/lib/api/ops-data-form.test.ts`：
- 直接单测纯逻辑（判别 fixture）：query 省略/限额钳制、event-kind 白名单、**encodeCacheKey 对含 / : 的 key 编码**、dlq id 守门。
- 源文本接线断言 adminOpsData.ts：六端点路径(锚定定界符)+ 方法(apiGet/apiPost/adminDelete)+ evictL2CacheKey 用 encodeCacheKey。
- 每条 mutation 实测转红再还原（路径锚定 + 切下一顶层声明边界）。
- ultracode：实现后跑 adversarial-review workflow 多 agent 对抗核验（契约保真/测试判别/clean-room/假绿）。

## 成功判据

- `tsc --noEmit` 干净；`node --test` 全绿；每测变异红验证；adversarial review 无 S0/S1。
- 开 PR squash 合并入 feat/frontend-portal，清 worktree + 释放 coordination 锁。

## blast radius / 风险

- 纯前端、低风险；不碰后端/schema/auth；不动 Sidebar（防与 proxies 撞）。
- DLQ replay 是写操作但后端不幂等无客户端键 → 前端不假造幂等键，UI 二次确认 + 重放中禁连点兜一层。
- 浏览器实操需部署后手测（本地无 admin token/真库）；逻辑层单测 + 源文本接线兜住。
- follow-up：新页登记进 Sidebar（待 proxies IA 落地统一加）。
