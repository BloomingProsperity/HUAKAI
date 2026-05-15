# 2026-05-15 Round 2 Cross-Discuss Synthesis (Claude × Codex — Round 2-B 5 Go features)

| Method | CLAUDE.md #10 parallel-draft. Claude (strategic, 70-100 行/feature) + Codex (specifier 读 backend/ 源码, 280-396 行/feature) 独立 5 个 feature plan. 本文对比 + Owner OCAW 终单. |
| Owner directive | 2026-05-15 "解冻啊。蠢啊" (Go backend 临时解冻 for Round 2-B 5 features), 实施前必须 Owner OCAW 决策 |
| Round 2-B scope | F-OBS-003 / F-OBS-004 / F-OBS-005 / F-CACHE-001 / F-AUTH-005 |

## 总评

| Feature | Codex plan | Claude plan | Agree | Conflict | Gap |
|---|---|---|---|---|---|
| F-OBS-003 (4-state billing) | 280 行 specifier (读 usage_records/billing_events 现状, columns 级 migration) | 75 行 strategic (4-state enum ASCII diagram, F-TRUST 链接) | 4-state 概念一致;migration 必要 | OCAW 数量差异 (Codex 7 vs Claude 5);Codex 详 column 级,Claude 详 stream state transition | Codex 漏 F-TRUST 卖点连接;Claude 漏 request_id/attempt_id 引入决策 |
| F-OBS-004 (async processor) | 326 行 (具体表名 async_processor_*, hot path release rule 4 选项) | 80 行 (5 handler ASCII, channel + worker pool + DLQ 集成) | handler 链架构一致,DLQ 关联 F-OBS-005 一致 | Codex 8 OCAW (含 raw body + admin replay UI + worker topology + 双跑 mode), Claude 4 OCAW | Codex 完整覆盖 generic DLQ vs usage_record_dlq 扩展决策 (重要); Claude 漏 |
| F-OBS-005 (DLQ + priority + dual-write) | 350 行 (现状: usage_record_dlq 已存在, 缺 generic kinds/lane/lease/replica/idempotency fields; 7 OCAW) | 80 行 (3 priority tier, exponential backoff 5 attempts, 10s lag) | DLQ 必要 + dual-write 一致 | Codex 揭示 Tx2 ordering bug (usage insert fail → billing_event 丢) 必须 F-OBS-005 内修, Claude 没列 (这是 money-path 修复, HIGH); priority tier 数差异 (Codex 不明示, Claude 3 tier) | Codex 漏 priority tier 具体数 (HIGH/MED/LOW); Claude 漏 Tx2 ordering bug fix (must-fix) |
| F-CACHE-001 (L2 cache) | 390 行 (路径: chat_completions_handler 入口 lookup; cachemetrics 复用; non-streaming v1, streaming Phase 2) | 80 行 (key = sha256 canonical, vendor TTL override, stream cache 全 SSE replay) | 简单 L2 起步 一致, in-memory LRU 推荐一致 | Codex 推 non-streaming 先做,Claude 推 stream cache 全 SSE replay v1; Codex 推 cache-hit 0 charge (Claude 没决策); placement OCAW Claude 没列 (after vs before pool acquire) | Codex 漏 per-vendor TTL override (Claude 提); Claude 漏 tenant_id physical key 决策 (重要,跨租户隔离) |
| F-AUTH-005 (credential mgmt) | 396 行 (现状: provider_accounts.credentials JSONB, refresh_worker 已雏形, antigravity_token_provider 已存在; 推 account_credentials 新表 + AES-256-GCM + KeyProvider interface) | 95 行 (5 mode × 3 vendor 显式枚举, KMS 选项 OCAW, F-TRUST 透明卖点) | 5 mode × 3 vendor 一致, 加密 at rest 一致, AES-GCM 一致 | Codex 推 v1 本地 key + KeyProvider interface (Personal Edition 不卡); Claude 推 KMS day 1; Codex 推 cloud SDK 不入 (用 stdlib SigV4); Claude 没明示 | Codex 列出 subscription OAuth 法务门 (claude_code/chatgpt_oauth 等是否启) — 这是 HIGH 决策, Claude 漏; Claude 列出 F-TRUST audit 卖点, Codex 漏 |

## Owner OCAW 终单 (合并去重, 25 项 → 必须决策)

按优先级排序 (HIGH = money-path / schema / auth 核心; MED = 实施细节; LOW = 实施时可定):

### F-OBS-003 (4-state billing)

- **HIGH-1**: migration 时机 — Phase 4.5 内同 wave 一起 vs R-E mainline 切换前? (Claude D1)
- **HIGH-2**: client_gone (用户中途断) 计费策略 — 收已发 token 还是按 operator policy 限额/退款? (Codex)
- **HIGH-3**: upstream_5xx after partial delivery 提交模式 — committed+partial 还是 aborted+zero-cost adjustment? (Codex 推 committed+partial + append-only refund)
- **MED-4**: stream_terminated_reason 字段 — first-class column 还是 billing_events.payload JSON? (Codex 推 first-class)
- **MED-5**: request_id/attempt_id/lease_id 三 ID 是否本 feature 引入,还是用 claim_id+attempt_seq+provider_account_id? (Codex)
- **LOW-6**: dashboard/frontend (Gemini UI) 是同 slice 还是 follow-up? (Codex)
- **LOW-7**: Partial state 是否给客户端可见 hint (F-TRUST 链路公开)? (Claude D2)

### F-OBS-004 (async processor chain)

- **HIGH-8**: hot path release rule — critical prefix (Billing + Audit 必须 finish 才 200) 还是 durable handoff 即可 (弱化"Tx2 before response")? (Codex 推 critical prefix)
- **HIGH-9**: schema approval — 新增 `async_processor_*` 表族 vs Go interfaces/memory runtime 限定? (Codex)
- **MED-10**: generic DLQ 还是 usage_record_dlq 扩展? (Codex 推 generic DLQ + 兼容桥, Claude 隐含 generic)
- **MED-11**: backpressure 策略 — drop oldest / drop newest / block hot path? (Claude D3, Codex 隐含)
- **MED-12**: raw body archive 是否本 feature 加 (Codex 推默认 only redacted ref + payload hash)
- **LOW-13**: 双跑 (旧同步 + 新异步) 7-14 天对账期 是否设? (Claude D4)
- **LOW-14**: admin replay UI/API 是否暴露 (Codex 推 idempotency tested 前不暴露)
- **LOW-15**: worker topology — single-process first vs multi-instance Postgres queue? (Codex 推 single-process first)

### F-OBS-005 (DLQ + priority + dual-write)

- **HIGH-16**: migration approval — usage_record_dlq 加 generic event kinds + lane + lease + replica fields (Codex 已分析现表缺什么)
- **HIGH-17**: dual-write mode — primary commit + async replica (推) 还是 sync replica fail-closed (零 RPO)? (Codex)
- **HIGH-18**: Tx2 ordering bug fix — F-OBS-005 内修 "usage insert fail rolls back Tx2 + 丢 billing_event" (Codex 揭, money-path) ✅ Claude 漏掉的关键
- **MED-19**: replica target — 单独 PG DSN / 同集群 table / 对象存储 append log / staged local-only? (Codex)
- **MED-20**: backoff 常数 — base 1s/cap 5m/max 10 attempts/DLQ 15m (Codex 推默认值)
- **LOW-21**: admin replay 权限 — platform_admin only 还是 SaaS tenant_operator scoped?  (Codex 推 platform only)

### F-CACHE-001 (L2 cache)

- **HIGH-22**: backend 选 in-memory LRU (Codex+Claude 一致推) 还是 Redis day 1? (Codex 推 LRU v1 + Redis Phase 2)
- **HIGH-23**: physical key 包含 tenant_id (Codex 推 yes — 跨租户隔离) ✅ Claude 漏的关键
- **HIGH-24**: cache-hit charging policy — 0 charge / 折扣 / 正常? (Codex 推 0; Claude 未决)
- **MED-25**: stream cache 语义 — non-streaming v1 + streaming Phase 2 (Codex) 还是 stream 全 SSE replay v1 (Claude)?
- **MED-26**: enablement default — off + env enable (Codex 推) 还是 memory default-on Personal Edition?
- **MED-27**: per-vendor TTL override (Claude 提) 是否引入 v1?
- **LOW-28**: cache hit placement — after pool acquire (low risk) 还是 before pool acquire (better outage)? (Codex 推 after)
- **LOW-29**: invalidation 操作员手动 + auto-on-account-rotate (Claude D5)

### F-AUTH-005 (credential mgmt)

- **HIGH-30**: account_credentials 表 cutover — 新表 + 新写迁出 provider_accounts.credentials? (Codex 推 yes)
- **HIGH-31**: encryption backend — v1 本地 key + KeyProvider interface (Codex 推, Personal Edition 不卡) vs KMS day 1 (Claude D1)?
- **HIGH-32**: subscription OAuth 法务门 — claude_code / chatgpt_oauth / codex_cli_oauth / code_assist / google_one / antigravity 6 模式是否启 / feature-flag / manual-first 等法务/ToS 审查? (Codex) ✅ Claude 漏的 HIGH
- **MED-33**: cloud auth dependencies — AWS/Azure/GCP 官方 SDK 入还是用 stdlib SigV4 mock? (Codex 推 no new SDK dep)
- **MED-34**: refresh window — expire 前多久触发 (5min / 15min / 30min)? (Claude D2)
- **MED-35**: refresh 失败 fallback — 立刻切换备用 vs 报错给用户? (Claude D3, Codex 推: static no grace, OAuth/cloud 可 grace 但无 stream side-effect 后)
- **LOW-36**: rotation 操作员 2FA / approval? (Claude D4)
- **LOW-37**: credential 使用 audit_events F-TRUST 链路公开? (Claude D5)

## Claude 推荐的 Owner 路径

按 risk + 实施时机:

1. **F-CACHE-001 立刻启**: backend LRU v1 + tenant_id physical key + after pool acquire + 0 charge cache-hit + non-streaming v1 (streaming Phase 2). 5 OCAW Codex 推得清,直接 codex 实施。3-5 天。
2. **F-AUTH-005 立刻启**: account_credentials 新表 + AES-256-GCM + KeyProvider local interface (KMS Phase 2) + cloud auth 不入 SDK + 6 subscription OAuth 法务 mode 全 feature-flag off (等法务审). Owner 确认 OCAW-32 法务门是关键 P0. 7-10 天。
3. **F-OBS-005 接 next**: 必含 Tx2 ordering bug 修 (HIGH-18, money-path) + dual-write async primary + commit + admin platform_admin only replay. 5-7 天。这是 4-state billing (F-OBS-003) 的 storage 基础。
4. **F-OBS-003 + F-OBS-004 并行 wave**: F-OBS-003 4-state enum + first-class column + commit partial+append-only refund; F-OBS-004 generic DLQ + critical prefix release rule + single-process worker first. 共 8-10 天。
5. **总时长**: ~3-4 周 codex 实施 + Claude review/synthesis + 真上游 R-D 烟雾 (15 cell, Owner 凭证 populate 后即可)。

## 决策快速参考

每 OCAW 都按 [HIGH = 必须 Owner 拍板] / [MED = Codex 实施前 Claude × Codex 共识即可] / [LOW = 实施时 codex 自决] 分级。HIGH 37 项中 **必拍板** 14 项 (HIGH-1..3, 8-9, 16-18, 22-24, 30-32). 其余 23 项可走 Codex × Claude 平行计算 + 我合议。

## Source files read

- 5 Claude plan + 5 codex plan (10 docs/plans/2026-05-15-f-*.md)
- docs/03_FEATURE_PARITY_MATRIX.md F-OBS-003/004/005, F-CACHE-001, F-AUTH-005 rows
- memory: project_core_trust_chain_differentiator, project_sub2api_scaling_bottleneck, project_pasr_real_diff_matrix
- (未读) sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway 源码 — 仅声誉级引用

Lane: synthesizer (Claude 决策视角)  
Agent: Claude Opus 4.7 (1M context)  
UTC timestamp: 2026-05-15T15:55:00Z
