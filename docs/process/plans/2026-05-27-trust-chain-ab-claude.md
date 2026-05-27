# 信任链 A+B 合一 (UX 面板字段 + lite ed25519 签名) — Claude lane plan

Lane: claude-pm-spec (反代敏感 / 信任链 invariant / HUAKAI 核心卖点 — Claude 写 spec, codex 写代码)
Time: 2026-05-27T11:00:00Z
Cross-lane: docs/process/plans/2026-05-27-trust-chain-ab-codex.md (codex 独立写, parallel-draft 不互相看)
Owner 启动信号: 2026-05-27 拍 "A+B 合一", C (Merkle 完整) 保 Mandatory Roadmap

## 0. 元信息

| Item | Value |
|---|---|
| 主线优先级 | Owner 2026-05-27 拍, 主线下一个 |
| 决策依据 | docs/process/decisions/2026-05-27-trust-chain-simplification-codex-eval.md §7 |
| HUAKAI 核心卖点 | [[project_core_trust_chain_differentiator]] — 链路公开 / 透明 / 反掺水 / 商家不能做假 / 用户消费透明 |
| 现状起点 commit | 75dca6b (trust-chain eval 落档) |
| 与 F-TRUST-001 关系 | 本切片 = F-TRUST-001 phase 1 lite (A+B); phase 2 (Merkle 完整 C) 推后 Mandatory Roadmap |

## 1. 现状盘点

### 1.1 HUAKAI 现有 trust schema (commit 0013)

`audit_ledger_entries` schema 已经在数据库存在 (commit 0013), 含:
- ledger_id (primary key)
- hop_chain JSON array (每跳 vendor → provider 走向)
- model_chain verdict (模型是否被替换)
- Merkle prev/curr root + ed25519 signature (C 部分预留)
- tenant_scope_ref (防 cross-tenant 枚举)
- append-only DB trigger (commit 0013 落地)

**结论**: schema 已经是 C 完整版预留, 但 **A (response 字段) + B (verify endpoint) 还没接通**。本切片做 A+B 接入, 不破 schema forward compat。

### 1.2 现有 spec

- docs/specs/trust-chain-user-verifiable-ledger.md (commit 158c421): PM synthesis spec 含 4 user verification endpoint (entry / detached verify / pubkey registry / chain head + proof) + 90 天 pubkey rotation。**这是 C 完整形态**, 本切片不做 chain head + proof + key rotation, 但做 detached verify + pubkey registry (lite 版)。

### 1.3 借鉴项目 response upstream provider 暴露模式 (fresh evidence)

| 项目 | 暴露方式 | 签名? |
|---|---|---|
| LiteLLM | `model_response._hidden_params["custom_llm_provider"]` (litellm/main.py:931 etc.) | **不签**, 商家可改 |
| Portkey | 无 explicit response provider header pattern (focus 在 request side) | n/a |
| Helicone | `providerResponse.headers` 抓上游响应头 (worker/src/lib/managers/AsyncLogManager.ts:51) | **不签** |
| LLMGateway | request 侧 `x-llmgateway-key/model` header, response 侧 **不暴露 upstream provider** | n/a |
| CLIProxyAPI | 不在 response 字段, 透传 vendor 原 response | n/a |

**结论 (CLAUDE.md #15)**: **没有任何借鉴项目签 response**, HUAKAI A+B 是 fusion-upgrade 真升级。

## 2. 缺口分类 + 与 C 边界

### 2.1 A 缺口 (UX 面板字段)

- response body / header 没暴露 upstream_provider + upstream_model
- 没暴露验证状态 (verified/signed-only/unverified/missing/mismatch)
- user panel 没 trust column / verify button
- 用户 dashboard 看不到 per-request provider/model 来源

### 2.2 B 缺口 (lite 签名)

- gatewayhttp 出 response 时没对 (provider+model+request+cost+redacted metadata) ed25519 签名
- 没 well-known 公钥 endpoint
- 没 detached verify endpoint (verify (payload, signature, public_key_fingerprint) → verified/invalid)
- 没用户面板 verify button + 后端 verify call

### 2.3 与 C 不做边界

C (Merkle 完整) 本切片不做:
- 不写 Merkle prev/curr root (schema 字段已存在, 本切片填 NULL 或 placeholder; forward compat 保留)
- 不做 chain-head endpoint + proof endpoint
- 不做 90 天 pubkey rotation (本切片 pubkey 固定, key rotation 进 C)
- 不做 third-party verifier CLI (本切片只 HUAKAI 内 verify endpoint, CLI 进 C)

## 3. Slice 切片 (TRUST-A + TRUST-B)

### TRUST-A-1: response 字段 + 用户面板 trust column (0.5 天)

- gatewayhttp 出 response 时加 header `X-Huakai-Upstream-Provider: anthropic` + `X-Huakai-Upstream-Model: claude-opus-4`
- response body 不强加字段 (避免破坏 vendor response shape OpenAI compat); header 是更安全的 metadata layer
- user dashboard usage history table 加 2 列: provider / model + 验证状态 (本切片 default unverified, B 接通后 verified)
- 验证状态枚举: verified / signed-only / unverified / missing / mismatch (5 状态)

测试 (CLAUDE.md #14):
- TestResponseHeaderIncludesUpstreamProvider (gateway 转发后 response 含 X-Huakai-Upstream-Provider header)
- TestUsagePanelTrustColumnShowsUnverifiedDefault (新行默认 unverified)
- mutation: 删 header 设置 → 测试红

### TRUST-A-2: 缺字段 / mismatch 显示策略 (0.25 天)

- response missing X-Huakai-Upstream-Provider → panel 显示 `missing` 红字
- header value 与 audit ledger 不一致 → 显示 `mismatch` 红字 (signal 商家篡改)
- D-4 决策实施

测试:
- TestPanelDisplaysMissingForResponseWithoutProviderHeader
- TestPanelDisplaysMismatchForProviderHeaderDifferingFromAuditLedger
- mutation: 不做不一致检测 → 测试红

### TRUST-B-1: ed25519 signer + 签名 payload (0.75 天)

- backend/internal/sign 包加 `ResponseSigner.SignResponse(provider, model, request_id, cost_cents, redacted_metadata) → (signature, key_fingerprint)`
- canonical encoding (JSON canonical form, sorted keys, no whitespace) — 防 signature variance
- 签名时机: response settle 时 (与 audit ledger entry 同事务)
- ed25519 私钥从环境变量 / KMS 注入 (启动 fail-fast: 无 key → return err)

D-5 决策依赖 (synthesis 拍): per-response inline (signing 进 hot path) 或 per-billing-event async (settle 后另起 worker)

测试:
- TestResponseSignerProducesValidSignatureForCanonicalPayload
- TestResponseSignerFailsClosedWhenKeyNotConfigured
- mutation: 签名前没 canonical encoding → 不同顺序 key 产生不同签名 → red

### TRUST-B-2: 公钥分发 + well-known endpoint (0.5 天)

- `/v1/trust/public-keys` 返 active + retired pubkey list + fingerprint + activated_at
- 公钥分发模式 D-2 决策:
  - 选项 A: HUAKAI 自有 well-known endpoint (默认)
  - 选项 B: SDK 嵌入 pubkey + remote check (用户 SDK 升级时拿最新)
  - 选项 C: 第三方 mirror (例 GitHub release / IPFS) — 推后 C 切片
- 本切片实施 A, B/C 为 Mandatory Roadmap

测试:
- TestWellKnownPublicKeysEndpointReturnsActiveAndRetired
- TestPublicKeyFingerprintMatchesActualKey
- mutation: 返 stale key list / wrong fingerprint → 测试红

### TRUST-B-3: detached verify endpoint + 用户面板 verify button (0.5 天)

- `POST /v1/trust/verify` 接 (request_id, signature, key_fingerprint) → (verified: bool, audit_ledger_entry_id, mismatch_fields[])
- 用户面板每行旁 "验证" 按钮 → 调 verify endpoint → 显示 verified/invalid + details
- 离线验证: 用户也可下载 audit ledger entry + signature + pubkey 用第三方工具验 (本切片只提供 HUAKAI verifier, 第三方 CLI tool 进 Mandatory Roadmap)

测试:
- TestDetachedVerifyEndpointReturnsVerifiedForValidSignature
- TestDetachedVerifyEndpointReturnsInvalidForTamperedPayload
- TestUserPanelVerifyButtonShowsVerifiedAfterValidVerify
- mutation: verifier 不真校验 ed25519 → tampered payload 仍 verified → red

### TRUST-B-4: signing wiring fail-fast + 启动断言 (0.25 天)

- cmd/gateway/wiring.go 启动时:
  - 读 ed25519 private key from env / KMS, 缺 fail-fast (return err)
  - assertResponseSignerConfigured: settler / billing path 都注入 signer; 缺 nil panic
- 与 anthropic OAuth `IsClaudeAIOAuthExchangerWithExplicitClient` 同款 wiring fail-loud

测试:
- TestGatewayStartupFailsFastWhenSignerKeyMissing
- TestResponseSignerInjectedToSettlerAtWiring
- mutation: 启动允许 nil signer / 不注入 settler → 测试红

### TRUST-AB-Docs: parity matrix + risk register + reference ledger (0.5 天)

- docs/03 加 F-TRUST-001 Status 补 phase 1 lite (A+B) 接通 commit ref
- docs/07 加 evidence rows (litellm / portkey / helicone / llmgateway / cliproxyapi 都不签 = HUAKAI fusion upgrade)
- docs/10 加 R-TRUST-AB-* (key 安全 / signer 性能 / mismatch 误报 / Merkle phase 2 路标)

## 4. 参考项目对照 (CLAUDE.md #15)

| 维度 | LiteLLM | Portkey | Helicone | LLMGateway | CLIProxyAPI | HUAKAI A+B |
|---|---|---|---|---|---|---|
| response upstream provider 字段 | `_hidden_params["custom_llm_provider"]` (main.py:931) | n/a | `providerResponse.headers` 透传 (AsyncLogManager.ts:51) | request 侧 only | 透传 vendor 原 response | **header X-Huakai-Upstream-Provider + X-Huakai-Upstream-Model + 验证状态** |
| 签名 | **不签** | n/a | **不签** | n/a | n/a | **ed25519 lite 签名** (Owner 卖点) |
| 公钥分发 | n/a | n/a | n/a | n/a | n/a | well-known endpoint |
| 验证 endpoint | n/a | n/a | n/a | n/a | n/a | detached verify endpoint + 用户面板 button |
| Merkle/chain | n/a | n/a | n/a | n/a | n/a | (phase 2 C Mandatory Roadmap) |

**HUAKAI fusion-upgrade**: A 部分借鉴 LiteLLM `_hidden_params` 暴露模式但升级到 header layer; B 部分**无借鉴源**, HUAKAI 自主创新, 唯一签 response 的 AI gateway。

## 5. 风险登记

| Risk ID | Severity | Mitigation |
|---|---|---|
| R-TRUST-AB-KEY-001 | S1 | ed25519 私钥泄露 = 所有过往签名失效。Mitigation: 启动 fail-fast 无 key 不启动; key 进 env var 不进 cred / 不进 audit_ledger; KMS 模式进 Mandatory Roadmap |
| R-TRUST-AB-PERF-001 | S2 | signing 进 hot path 增 latency (ed25519 单签 < 1ms 但 CPU 占用)。Mitigation: D-5 决策 per-response 还是 async batch; Owner 拍 |
| R-TRUST-AB-MISMATCH-001 | S2 | header value 与 audit ledger 不一致检测可能误报 (例: vendor 模型 alias 不一致). Mitigation: D-4 拍显示策略 (warning vs 红字 vs 拒绝展示) |
| R-TRUST-AB-FORWARD-COMPAT-001 | S1 | 本切片不写 Merkle prev/curr root, schema 字段 NULL 填充。Mitigation: phase 2 C 接通时不破 schema, 仅 backfill prev/curr root |
| R-TRUST-AB-3RD-PARTY-VERIFIER-001 | S2 → Mandatory Roadmap | 本切片只 HUAKAI 内 verify endpoint, 第三方 CLI tool 推后 C |
| R-TRUST-AB-PUBKEY-ROTATION-001 | S2 → Mandatory Roadmap | 90 天 pubkey rotation 进 phase 2; phase 1 lite 用单一固定 key (足够 lite 阶段) |

## 6. Owner 决策点

### D-1: 签名 payload 范围

- **A** 最小集 (推荐): provider + model + request_id + cost_cents — UX 卖点必须签, 最小化 attack surface
- B 加 redacted_metadata (例: input_token_count + output_token_count + plan_type) — 商家审计需要
- C 加 raw input/output (违 HUAKAI [[project_core_trust_chain_differentiator]] "日志只系统报错" 隐私原则, **不推荐**)

### D-2: 公钥分发模式

- **A** HUAKAI well-known endpoint `/v1/trust/public-keys` (推荐, 本切片实施)
- B 后续加 SDK 嵌入 pubkey + remote check (Mandatory Roadmap)
- C 第三方 mirror (Mandatory Roadmap)

### D-3: 验证 endpoint 路径

- **A** HUAKAI 内置 `POST /v1/trust/verify` (推荐, 本切片实施)
- B + 第三方 CLI binary 双轨 (CLI 进 Mandatory Roadmap, 不本切片)

### D-4: 缺字段 / mismatch 显示策略

- **A** warning banner 黄字 (推荐 — 不阻止用户看 response, 但明显 unverified signal)
- B 红字阻止 (UX 影响大)
- C 默默成功 (违 HUAKAI [[project_core_trust_chain_differentiator]] "商家不能做假" 原则, **codex 评估明确反对**, **不推荐**)

### D-5: 签名时机

- **A** per-response inline (推荐): response settle 时同事务签名, 与 audit_ledger entry 一致, latency < 1ms (ed25519 单签开销)
- B per-billing-event async: settle 完成后独立 worker 签名, 不增 latency 但有窗口期 (window 内 response 已发送但未签 → 商家可改)
- C 双轨 (A 同步 + B fallback): 同步失败时降级 async — 复杂度高

### D-6: 与 F-TRUST-001 (Merkle 完整) 的边界

- **A** schema forward-compat (推荐): commit 0013 schema 字段 prev_root/curr_root/merkle_proof 本切片填 NULL, phase 2 C 接通时 backfill
- B 新 schema (本切片自有 audit_lite_entries 表): 与 phase 2 schema 隔离, 风险 = 双轨数据复杂度
- 显然 A 更稳

## 7. 工时 + 推荐起步

| Slice | 工时 | Commit groupings |
|---|---|---|
| TRUST-A-1 response header + panel column | 0.5 天 | 第 1 commit (A 接通) |
| TRUST-A-2 missing/mismatch 显示 | 0.25 天 | 同上 |
| TRUST-B-1 ed25519 signer + canonical | 0.75 天 | 第 2 commit (B signer 接通) |
| TRUST-B-2 well-known pubkey endpoint | 0.5 天 | 第 3 commit (B verifier 接通) |
| TRUST-B-3 detached verify + panel button | 0.5 天 | 同上 |
| TRUST-B-4 wiring fail-fast | 0.25 天 | 同上 |
| TRUST-AB-Docs | 0.5 天 | 第 4 commit (docs) |
| **合计** | **3.25 天** (中位) | 4 commits |

推荐起步: **Owner 拍 D-1/D-2/D-3/D-4/D-5/D-6 → TRUST-A 第一 commit (UX layer) → TRUST-B signer 第二 commit (核心安全) → TRUST-B verifier 第三 commit (用户面 enabling) → TRUST-AB-Docs 第四 commit**。

## 8. Clean-room 约束

- 借鉴 LiteLLM `_hidden_params["custom_llm_provider"]` 暴露**意图** (response carries upstream metadata), 不抄实现细节
- HUAKAI 签名实施完全自主 (无借鉴源), 不破 clean-room
- 注释中文
- Source files read 列 commit msg

Source files read (CLAUDE.md #11 specifier lane):
- ~/refs/litellm/litellm/main.py:931 etc. (MIT, E-LIC-005)
- ~/refs/helicone/worker/src/lib/managers/AsyncLogManager.ts:51 (GPL-3.0 behavior only, E-LIC-007)
- ~/refs/llmgateway/apps/playground/src/app/api/chat/route.ts:443-444 (license TBD)
- ~/refs/portkey/src/ (MIT, E-LIC-006) — confirmed no signing pattern
- ~/refs/CLIProxyAPI (MIT, E-LIC-009) — confirmed no signing pattern

HUAKAI 自有:
- docs/specs/trust-chain-user-verifiable-ledger.md (commit 158c421) — 现 spec, 本切片不破
- backend/sql/migrations 0013 audit_ledger_entries — schema 现状
- backend/internal/sign 包 — 已有 ed25519 signer (复用)
- backend/internal/auditledger — 已有 ledger Append (复用)

Lane: claude-pm-spec
Time: 2026-05-27T11:00:00Z UTC
