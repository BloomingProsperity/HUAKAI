# 信任链 A+B 合一 — Synthesis

Lane: claude-pm-synthesis
Time: 2026-05-27T11:30:00Z
Cross-lane inputs:
- [Claude lane](2026-05-27-trust-chain-ab-claude.md) (TRUST-A 2 + TRUST-B 4 切片 + 6 D 决策)
- [Codex lane](2026-05-27-trust-chain-ab-codex.md) (TRUST-A 4 + TRUST-B 5 切片 + 9 D 决策)

## 0. Lane 差异 + Synthesis 决定

| 维度 | Claude lane | Codex lane | Synthesis 选择 | 理由 |
|---|---|---|---|---|
| Slice 数 | 6 (TRUST-A 2 + B 4) | 9 (TRUST-A 4 + B 5) | **采 Codex 9 切片更细** | Codex 把 wire contract + status vocabulary 独立成 TRUST-A-1 (Claude 隐含), gateway + panel + 测试分独立 slice, signer + receipt 派生 + pubkey + verify 各独立 commit, review 面更聚焦 |
| D 决策数 | 6 (D-1..D-6) | 9 (D-1..D-9) | **采 Codex 9 D** | Codex 抓出 Claude lane 漏的 3 个: D-7 header vs body, D-8 signer 不可用产品策略, D-9 schema migration 边界 |
| D-1 payload 范围 | A 最小集 (provider+model+request+cost_cents) | A 加 token counts + price snapshot + validation state + redacted metadata allowlist | **让 Owner 拍** (Claude 保守 vs Codex 全) |
| D-2 pubkey 分发 | well-known endpoint | `.well-known/huakai-pubkey.json` 标准 + `/v1/audit/pubkey(s)` 兼容 | **采 Codex** | RFC 标准 well-known 路径 |
| D-3 verify endpoint | `POST /v1/trust/verify` | `/v1/trust/verify` 新增 + `/v1/audit/verify` Merkle 旧不破 + `/v1/receipts/{id}/verify` 同 status 模型 | **采 Codex** | 三入口分清楚 detached/inline/inline-by-receipt |
| D-4 mismatch 显示 | warning banner 黄字 | 红色 badge + warning banner, API response 仍成功但信任状态不成功 | **采 Codex** (一致 + 更细) |
| D-5 签名时机 | per-response inline | **双轨**: response inline provisional + final billing event detached | **让 Owner 拍** (单轨 vs 双轨) |
| D-6 Merkle 边界 | schema forward-compat 不破 | 同 + docs 标 Phase 2/C Mandatory Roadmap | **采 Codex** (一致 + 文档化路径) |
| D-7 body vs header (Codex 新增) | header only | header 默认 + body extension mode optional | **让 Owner 拍** |
| D-8 signer 不可用 (Codex 新增) | 隐含 fail-fast (启动断言 signer 配置) | runtime fail-open `unverified` + paid request 可选 fail-closed | **让 Owner 拍** |
| D-9 schema migration (Codex 新增) | 严格复用 0013 schema | 默认复用, 若 ledger 无法派生 lite payload 停下问 Owner | **让 Owner 拍** (允许新表 vs 严格不破) |

## 1. Synthesis 切片清单 (采 Codex 9 切片)

| Slice | 工时 | Commit |
|---|---|---|
| TRUST-A-1 wire contract + status vocabulary (5 状态枚举 + header schema) | 0.25 天 | 第 1 commit (A 接通) |
| TRUST-A-2 Gateway response fields (X-Huakai-Upstream-Provider / Model header) | 0.5 天 | 同上 |
| TRUST-A-3 User panel provider/model/status column | 0.5 天 | 同上 |
| TRUST-A-4 A 验收测试 + 弱测试清理 (mutation 守门) | 0.25 天 | 同上 |
| TRUST-B-1 Lite signed payload canonical contract (JSON canonical form) | 0.5 天 | 第 2 commit (B signer) |
| TRUST-B-2 Signer integration + receipt 派生 | 0.75 天 | 同上 |
| TRUST-B-3 Public key well-known distribution | 0.5 天 | 第 3 commit (B verifier) |
| TRUST-B-4 Detached verify endpoint + CLI mode | 0.5 天 | 同上 |
| TRUST-B-5 Docs / acceptance tests / release gate | 0.5 天 | 第 4 commit (docs) |
| **合计** | **4.25 天** (中位) | 4 commits |

## 2. 默认采纳 (Owner 无需拍)

- **D-2** pubkey 分发: `.well-known/huakai-pubkey.json` 标准 + `/v1/audit/pubkey(s)` 兼容
- **D-3** verify endpoint: 新 `/v1/trust/verify` (detached) + 旧 `/v1/audit/verify` (Merkle, 不破) + `/v1/receipts/{id}/verify` (inline by request_id)
- **D-4** mismatch 显示: 红色 badge + warning banner, API response 仍 200 OK 但信任状态明示 unverified/mismatch
- **D-6** Merkle 边界: schema forward-compat (Merkle chain fields 本切片填 NULL), docs 标 Phase 2/C Mandatory Roadmap

## 3. 需要 Owner 拍的 4 个 D 决策

### D-1: 签名 payload 范围

- A (最小集, Claude lane): provider + model + request_id + cost_cents — 卖点必签的最少字段
- B (推荐扩展, Codex lane): + token counts + price snapshot + validation state + redacted metadata allowlist — 商家 audit 更全
- C (扩展全): + tenant_scope_ref + requested/routed/delivered model + rate snapshot + currency/minor unit + occurred_at — 最全但 payload 大

### D-5: 签名时机

- A (单轨 inline, Claude lane): response settle 时一次签 (含 cost), latency +1ms
- B (双轨, Codex lane 推荐): response inline 签 provisional (provider/model/request, 不含 cost) + final billing event detached 签 (含 cost) — 更稳但实现复杂

### D-8: signer 不可用怎么办

- A (fail-open, Codex 推): signer down 时 API 仍 200 OK, 信任状态 unverified; paid request 进 operator review 队列
- B (fail-closed): signer down 时 API 拒绝 (返 503), 用户能感知但商业损失大

### D-9: schema migration 边界

- A (严格复用 0013): 严格只用现有 audit_ledger_entries schema, 不加新表/新列, 若派生不出 lite payload 停切片 Owner 拍
- B (允许新表 trust_receipts): 允许加 trust_receipts 表 / receipt payload column, schema gate 走标准流程

## 4. 参考项目对照 (CLAUDE.md #15, 借鉴双 lane evidence)

(参考 docs/process/plans/2026-05-27-trust-chain-ab-claude.md §4 + codex lane §4)

| 项目 | 暴露方式 | 签名? | HUAKAI 升级 |
|---|---|---|---|
| LiteLLM | `_hidden_params["custom_llm_provider"]` (main.py:931) | 不签 | header + 签名 |
| Portkey | n/a | n/a | 全新 |
| Helicone | `providerResponse.headers` 透传 | 不签 | + 签名 |
| LLMGateway | request 侧 only | n/a | + 响应侧 |
| CLIProxyAPI | 透传 vendor 原 response | n/a | 全新 |

**HUAKAI fusion-upgrade (CLAUDE.md #12)**: **没有任何借鉴项目签 response**, A+B 信任链是 HUAKAI 唯一卖点。

## 5. 与 F-TRUST-001 (Merkle 完整) 路径关系

- 本切片 = F-TRUST-001 phase 1 lite (A+B)
- phase 2 (Merkle 完整 C) Mandatory Roadmap, schema 字段 prev_root/curr_root/merkle_proof 本切片填 NULL
- 不删除现有 F-TRUST-001 编号 / spec / Merkle schema

Lane: claude-pm-synthesis
Time: 2026-05-27T11:30:00Z UTC
