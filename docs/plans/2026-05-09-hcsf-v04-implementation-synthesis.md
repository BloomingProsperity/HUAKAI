# HCSF v0.4 Phased Delivery — Claude × Codex Synthesis

**日期**: 2026-05-09
**前置 lanes**:
- `docs/plans/2026-05-09-hcsf-v04-implementation-claude.md`（Sonnet 893 行 / 8 phase / 13 capability / 8 DECISION-POINT）
- `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md`（Codex 68KB / 8 phase / 14 capability / 9 DECISION-POINT）
**触发**: Owner 2026-05-09 "按照最优的进行" 批准 HCSF v0.4 + L3+L4 PMF + inference spend metric

## TL;DR — 两 lane 高度一致

**共识 (95%)**：
- 8 个 Phase 名几乎一致（P-0 → P-7）
- 时间盒 12-15 周
- Vendor Tier-A 一致：Anthropic / OpenAI Chat / OpenAI Responses / Gemini / Bedrock-on-Anthropic
- 测试 4 层一致：Unit / Property / Capability matrix integration / 真账号 smoke (Owner 本机)
- IR 选型一致：Anthropic-rich primary，OpenAI-compatible storefront
- Native passthrough 路径一致：`/v1/native/<vendor>/<capability>`

**分歧 (5%)**：
- Capability 数：Sonnet 13 vs Codex 14（图像/音频/视频/文件是否单独节点）
- DECISION-POINT 焦点不同：Sonnet 偏 algorithm 细节（SHA-12 / mid-stream fallback scope），Codex 偏 architecture 决策（HCSF 是否落库 / Responses public timing）

## 1. 8 Phase 切分（两 lane 综合）

| Phase | 名称 | 时间 | 关键产物 | Owner gate |
|---|---|---|---|---|
| P-0 | HCSF struct 实化 + 命名空间清理 | Week 1-2 | `proto.HCSFEnvelope` v0.4 真实化（当前 `proto.HCSF struct{}` 空），版本字段 + alias | **D1**: Go type 名 + JSON 字段名锁定 + **D3**: 是否持久化数据库 |
| P-1 | Capability Graph IR Schema | Week 2-4 | 13/14 capability schema + edge model + protocol_loss 一等公民 | **D2**: 13 vs 14 capability + **D12**: data_retention 词汇枚举 |
| P-2 | ClientAdapter 落地 | Week 4-6 | Anthropic / OpenAI Chat / OpenAI Responses ClientAdapter（当前全 nil-fallback raw passthrough） | **D11**: `/v1/responses` 是否 public 在 v0.4 |
| P-3 | Phase B Canonical→Provider | Week 6-8 | 4 vendor 的 `CanonicalToProviderRequest`（当前全 ErrNotImplemented） | (技术决策，无 Owner gate) |
| P-4 | Native passthrough 端点 | Week 8-10 | `/v1/native/<vendor>/<capability>` 显式入口 + 鉴权 | **D5**: native passthrough 鉴权策略 |
| P-5 | Capability matrix 测试 | Week 10-12 | property test + cross-vendor invariant test + protocol_loss assertion | **D10**: capability matrix cell 数 + **D13**: P-5 release gate 阈值 |
| P-6 | 真账号 smoke (Owner 本机) | Week 12-14 | 4 vendor 真凭证测试，per `feedback_owner_local_verification.md` | **D7**: smoke 范围 + budget |
| P-7 | Spend metric + L3+L4 PMF gate | Week 14-15 | monthly annualized inference spend dashboard + capability supported % | **D8**: spend 数字来源标记 |

P-8 (roadmap, 12-15 周窗口外): 中段 fallback 实现（参 LiteLLM `MidStreamFallbackError`）/ Bedrock-on-Llama+Cohere+Mistral / xAI / Cursor / Copilot / Kiro / Windsurf / Antigravity / 跨账号 cache 复制 (Direction 1) / 预测式迁移 (Direction 3 暂缓)。

## 2. Capability 设计（两 lane 合并）

13 capability 共识 + 1 个 Codex 主张多分（图像/音频/视频/文件）：

| # | Capability | IR schema | Native trigger | 来源 |
|---|---|---|---|---|
| 1 | `text` | role / 有序 content blocks / stop_reason / finish_class / usage / stream event 边界 | provider 文本事件无法表达为有序 HCSF events | 共识 |
| 2 | `tool_use` / `tool_result` | tool_call_id / display name / input JSON / result blocks / partial argument deltas / normalized error | hosted tools / external server actions | 共识 |
| 3 | `thinking` / `reasoning` | reasoning budget / emitted blocks / hidden token accounting / redaction class / signatures | reasoning 文本/签名/budget 不能保真 | 共识 |
| 4 | `cache_control` | scope / breakpoints / cache_key hint / read+write usage / safety warning | cache 语义依赖 vendor 元数据 | 共识 — **D6**: 是否含跨账号复制意图 |
| 5 | `structured_output` | json_mode intent / strict schema / parser mode / failure recovery / fallback strategy | 不可表达的 schema dialect / constrained decoder | 共识 |
| 6 | `computer_use` | env target / action / screenshot/input blocks / approval / audit | 默认走 native（HUAKAI 无 sandbox） | 共识 |
| 7 | `file` | source_kind / media type / file id/URL digest / size / retention label | 上传 lifecycle / assistant resource binding | Codex 主张分开 |
| 8 | `image` | URI/base64/file_id / media type / dimensions / loss audit | 图片 transport 无法 normalize | Codex 主张分开 |
| 9 | `audio` | transport / format / sample / transcript policy / live compat | websocket/codec 不可表达 | Codex 主张分开 |
| 10 | `video` | URL/base64/file ref / time range / size/codec | 上传/chunk/live video | Codex 主张分开 |
| 11 | `live_session` | connect params / bidi event stream / modality set / tool availability / resume token / close reason | Gemini Live / OpenAI Realtime 默认 native | 共识 |
| 12 | `batch` | async job graph / input file / endpoint / validation / output/error / cost / retry | vendor 特定批处理 lifecycle | 共识 |
| 13 | `mcp_server` | server label / allowed ops / approval / invocation events / result blocks | MCP transport / server auth | 共识 |
| 14 | `data_retention` | no-train / ZDR / regional / request_store / audit / enforcement | 不能从 generic API 字段推断 ZDR | 共识 |

**D2 综合推荐**: 14 capability（接受 Codex 多分）。理由：
- Issue mining 显示图像 / 音频 / 视频 / 文件**断点不同**（不同 issue # 列）
- 合并到 `multimodal` 单节点会失去三维 metric 切片维度
- 14 vs 13 IR schema 工作量差异 < 0.5 周

如果需收敛工作量，可在 P-1 末做一次 capability merge review；现在锁定 14 是更安全的默认。

## 3. Vendor adapter 优先级

两 lane 完全一致（L3+L4 PMF 推论）：

| Tier | Vendor | 完整度目标 | 阶段 |
|---|---|---|---|
| **A** | Anthropic Messages | rich 完整 (cache_control / thinking / tool_use 一等公民) | P-2 → P-3 → P-6 |
| **A** | OpenAI Chat Completions | 必须 | P-2 → P-3 → P-6 |
| **A** | OpenAI Responses | 必须（agent workload） | P-2 → P-3 → P-6 |
| **A** | Gemini API | 必须 | P-2 → P-3 → P-6 |
| **A** | Bedrock-on-Anthropic | 已有，补 capability matrix | P-3 |
| **B** (P-8 roadmap) | Bedrock-on-Llama / Cohere / Mistral | lossy / native passthrough only | P-8 |
| **B** | xAI Grok | lossy + native | P-8 |
| **B** | Cursor / Copilot / Kiro / Windsurf / Antigravity | native passthrough only（CLI 兼容） | P-8 |

## 4. 测试策略

| 层 | 内容 | 阶段 |
|---|---|---|
| Unit | per capability mock vendor | P-1, P-3 |
| Property | canonical round-trip (Anthropic→IR→Anthropic 应保真) | P-5 |
| Capability matrix integration | per (vendor, capability) cell 测试 + protocol_loss 断言 | P-5 |
| 真账号 smoke | Owner 本机，4 Tier-A vendor 真凭证 | P-6 |
| Cross-repo issue regression | 用 issue mining 的 #4678 (cache_read=0) / #4168 (4-state billing) 等作 fixture | P-5 |

## 5. 失败模式（两 lane 一致，不许 silent drop）

- 跨 vendor capability 不存在 → emit `protocol_loss` + `unsupported_capability` + 建议 native passthrough（**不许 silent drop**）
- streaming 中段中断 → 当前不重试（D9 决定是否做）；P-8 实现 LiteLLM `MidStreamFallbackError` 模式
- Anthropic-rich → OpenAI lossy → emit visible audit + downgrade with warning
- 真凭证错误 → 走 PASR demote + 错误归一化（不影响 PASR HasCacheBitmap）
- Cross-tenant prefix 污染 → 测 (tenant, account, prefix) 三全；空 tenant no-op

## 6. PMF + Metric 连接

每 phase 结束时 dashboard 更新：

| Phase 出口 | 月度年化 inference spend (mock/smoke/real) | L3+L4 capability 支持度 |
|---|---|---|
| P-0 | mock $1K | 0% |
| P-2 | mock $10K | 30% (Anthropic + OpenAI Chat 基本) |
| P-4 | mock $100K | 60% (含 Responses + Gemini) |
| P-6 | smoke $300K | 80% (4 vendor 真凭证) |
| P-7 | real $1M+ year 化 | 95% (除 P-8 roadmap) |

## 7. Fusion-upgrade 三维 delta（汇总）

| Feature | Upstream A | Upstream B | HUAKAI delta | Dimension |
|---|---|---|---|---|
| Canonical 数量 | LiteLLM 单 OpenAI canonical (`BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1797-1876`) | Portkey bi-canonical (`Portkey-AI/gateway@351692fd:src/index.ts:233-253`) | 14 capability graph IR + per-vendor native passthrough | 架构 |
| Protocol loss | LiteLLM silent drop | Portkey strict OpenAI compliance flag | 一等公民 `protocol_loss` 输出 + capability matrix 测试 | 算法 + 生态 |
| Tool name truncation | LiteLLM SHA-8 (`<55prefix>_<8sha256>`) | (Portkey 不做) | SHA-12 with collision detection（D4 决定） | 算法 |
| budget_tokens 透传 | LiteLLM bucket 到 reasoning_effort（lossy） | (Portkey/envoy 各自) | 原值保留 + per-vendor mapping table | 算法 |
| Mid-stream fallback | LiteLLM `MidStreamFallbackError` (`router.py:2063+2209`) | Portkey 不重试 | P-8 roadmap 实现 | 算法 |
| Cache hit on relay | (上游普遍丢失) | new-api#4678 报"cch=xxx" sentinel 破坏 prefix cache | Gateway-injected metadata sanitizer | 算法 + 生态 |
| Capability matrix UI | (上游手写表) | (上游手写表) | property-test 自动生成 + per-vendor metric 切片（已部分落 PASR-lite D2） | 生态 |

## 8. Owner 必决策的 7 点（按优先级）

剩余 7 个 (D4/D9/D10/D13/D14...) 是技术 implementation 细节，可推到对应 phase 实施时决定。

### **D1**: HCSF struct 命名 + 持久化
- 锁定 `proto.HCSFEnvelope` v0.4 Go type 名 + JSON 字段名
- 是否在 P-0 做 schema migration 入库？（high-risk per #risk-based-confirmation）
- **推荐**: P-0 仅在内存（不入库）；schema migration 推到 P-7 后单独立项

### **D2**: 13 vs 14 capability
- 14: 文件/图像/音频/视频分开（Codex 主张 + issue mining 证据）
- 13: 合 multimodal 单节点（Sonnet 起草默认）
- **推荐**: 14（接受 Codex 多分；P-1 末可做 merge review）

### **D3**: HCSF 是否落数据库（high-risk）
- 是 → P-0 包括 schema migration（涉 Owner 高风险确认 per `Risk-Based Confirmation Rule`）
- 否 → 仅内存 IR；P-7 后单独立项做持久化
- **推荐**: 否（不混入 v0.4 sprint）

### **D5**: Native passthrough 鉴权
- 选项 (a) 同 standard route 鉴权 / (b) 单独 token / (c) 直接禁用 enterprise opt-in
- **推荐**: (a) 同 standard，但加 audit log 标 native 调用比例

### **D6**: cache_control 是否含跨账号复制意图（Direction 1）
- 是 → IR 含跨账号 cache 信号字段；P-8 智能预热 roadmap
- 否 → 仅单账号 cache_control
- **推荐**: 否（per Direction 1 物理前提未验证；smoke test 后再决定）

### **D7**: 真账号 smoke 范围（涉 Owner budget）
- 推荐 4 vendor: Anthropic / OpenAI / Gemini / Codex（per `project_real_vendor_account_scope.md` 限定 4 vendor）
- budget 上限：Owner 自定（推荐 < $100 / 周 budget）
- **推荐**: 4 vendor + $100/周 budget

### **D11**: `/v1/responses` 是否 public 在 v0.4
- public → 早 announce，可能踩坑（OpenAI Responses 还在迭代）
- 仅 native passthrough → `/v1/native/openai/responses` 暴露但 `/v1/responses` 不暴露
- **推荐**: 仅 native passthrough（保守，agent workload 仍可访问）

### **D12**: data_retention 词汇枚举
- Codex 推荐：`unknown / request_store_false / provider_contract_required / regional_asserted / zdr_verified`
- `zdr_verified` 必须 Owner 提供 vendor/account proof 才能用
- **推荐**: 接受 Codex 枚举

## 9. 推迟到 implementation 时决定的 7 个技术细节

不需要 Owner 现在决定，phase 实施时 PM 自己拍板：

- D4 SHA-8 vs SHA-12 collision detection（算法 — P-2/P-3 时定）
- D8 P-7 spend 数字来源标记（dashboard tile — P-7 时定）
- D9 中段 fallback 范围（P-8 roadmap，不在 12-15 周窗口）
- D10 capability matrix cell 数（P-5 时根据 P-3 实际产物定）
- D13 P-5 release gate 阈值（P-5 时定）
- D14 测试依赖（P-5 时定）

## 10. 风险与盲点（综合自评）

- **Capability schema 设计在 P-1 锁定后调整成本高**——两 lane 已 cite `proto.HCSF struct{}` 空 + 现有 CanonicalRequest 部分形态。如果 P-1 schema 决策错，后续 phase 全要返工
- **真账号 smoke 在 Owner 本机跑**——sandbox 内只能 mock 验证（per `feedback_owner_local_verification.md`）。P-6 真账号验证是 PMF gating 的硬依赖
- **codex review 工具在 sandbox 挂掉**——sonnet 顶班 review。每 commit per #8 codex 工具问题需要 Owner 本机解决（apt install bubblewrap 或 settings permission）
- **L5 enterprise 不打**的判断假设 HUAKAI 不 raise 大额——若商业目标变化，决策要重看
- **5% markup 天花板**是 OpenRouter benchmark；合规离岸场景或许有溢价空间，需 P-7 真客户验证
- **synthesis 是 PM-orchestrator (Claude opus-4-7) 自己合成**——理论应再过 codex synthesis review，但 codex 工具坏；当前 sonnet review 已查过 docs

## 11. 决策路径

如果 Owner 同意：
- D1 推荐（不入库）
- D2 推荐（14 capability）
- D3 推荐（否）
- D5 推荐（standard auth + audit）
- D6 推荐（否，等 smoke）
- D7 推荐（4 vendor + $100/周）
- D11 推荐（仅 native passthrough）
- D12 推荐（5 词汇）

→ 立即开 P-0 实施。

如要 override 任意 D，标出来，PM 调整 plan 后再开。

## Tail block (per AGENTS.md template)

Source files read: `docs/plans/2026-05-09-hcsf-v04-implementation-{claude,codex}.md` (HUAKAI internal — exempt per #12)；`docs/research/2026-05-09-*.md` (HUAKAI internal)
Lane: synthesizer (cross-discuss + agree/conflict/gaps)
Agent: Claude opus-4-7 [1m]
UTC timestamp: 2026-05-09T16:10Z
