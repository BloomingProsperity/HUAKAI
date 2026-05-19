# HUAKAI 核心差异化 — 信任链 / 透明 / 反掺水 Feature Family (Claude lane)

- 日期：2026-05-13（UTC）
- 作者：Claude PM-Orchestrator（lane = specifier，独立未读 Codex lane）
- 平行 lane：`docs/process/plans/2026-05-13-trust-chain-feature-family-codex.md`（codex 独立起草中）
- 触发：Owner 2026-05-13 quote "我们的核心还有一个就是链路公开，无用户数据保留日志，模型校验用户能看到，商家无法做假，掺水，搞映射。日志只做系统报错，等等重要的东西，还有用户得消费等"

## 1. 战略定位

HUAKAI 与 sub2api / new-api / portkey / litellm / one-api / all-api-hub 等所有现有 AI gateway / 账号池的**根本差异**不是更好用，而是**用户可验证**。所有现有项目都是 operator-centric——商家拿到代码后可以任意映射、虚报、掺水、改 token 数，用户无能为力。

HUAKAI 立"用户可验证网关"商业定位：
- **默认透明**：每个请求的 hop chain（user → router → pool → account → provider）user 可见
- **默认无 body 日志**：系统 log 永不含 prompt / completion
- **模型签名校验**：response 带签名 header，user 能验证"我付 opus 的钱拿到的就是 opus"
- **可验证审计 ledger**：HUAKAI 自身签名 dispatch 决策；user 可独立 verify

这是 commercial moat。MIT 协议反而是优势：商家想 fork HUAKAI 改逻辑会破坏签名，下游用户立刻发现。

## 2. Feature ID 映射

| Owner 要求 | Feature ID | Lx 级 | Phase 建议 | HCSF 字段挂载点 | 与现有 feature 关系 |
|---|---|---|---|---|---|
| 链路公开 | **F-TRUST-001** Hop Chain Attestation | L2 Production Usable | Phase 5（与 Account Hub 同期） | 新增 `Accounting.HopChain []HopAttestation` | 全新，不重叠现有 F-* |
| 无用户数据保留日志 | **F-PRIV-001** No-Body Log Boundary | L1 MVP | Phase 4（Gateway Core 起就强制） | 已有 `Policy.DataRetention.RequestStore` + 新增 log redaction enforcer | 与 F-OBS-001 失败流计费 平行不重叠 |
| 模型校验用户能看到 | **F-TRUST-002** Upstream Model Attestation | L2 Production Usable | Phase 5 | 已有 `RequestMeta.UpstreamModel`；新增 response signature header + verify endpoint | 与 F-AUTH-005 credential mgmt 互补 |
| 商家无法做假/掺水/映射 | **F-AUDIT-001** Signed Dispatch Ledger | L2 Production Usable | Phase 5-6 | 新增 audit ledger 表（不是 `log`）+ Merkle chain + 公钥发布 | Replaces 现有 generic "Audit Logs"（17 matrix L2 行升级） |
| 日志只做系统报错 | **F-PRIV-002** System Log vs Audit Ledger 拆分 | L1 MVP | Phase 4 | log 模块边界 + zap/zerolog redaction config | 是 F-PRIV-001 的兄弟，强制 SRE log 与 audit ledger 独立 |
| 用户消费透明 | **F-TRUST-003** User-Facing Usage API | L1 MVP | Phase 4-5 | 新增 `/v1/usage/me` 公开 endpoint + signed records | 是 F-OBS-001 Usage Records 的 user-facing 延伸 |

**新建议增 1 项（Codex 可能也提）**：

| **F-AUDIT-002** Token Count Cross-Check | L2 | Phase 5-6 | new `Accounting.TokenAudit{ self_count, upstream_count, divergence }` | 防止商家虚报 token |

## 3. F-TRUST-001 详细 spec — Hop Chain Attestation

**行为**：每个 user request 完成后，response body 或 trailing header 含一个 `hop_chain` 字段（可压缩），形如：

```json
{
  "hop_chain": [
    {"hop": "ingress", "ts": "2026-05-13T07:30:00.123Z", "request_id": "req_abc"},
    {"hop": "router", "ts": "...", "route_id": "rt_anthropic_pool"},
    {"hop": "pool", "ts": "...", "pool_id": "pool_anthropic_oauth"},
    {"hop": "account", "ts": "...", "account_id_hash": "<sha1>"},
    {"hop": "provider", "ts": "...", "provider": "anthropic", "endpoint": "api.anthropic.com/v1/messages"},
    {"hop": "response", "ts": "...", "duration_ms": 1240, "upstream_model_reported": "claude-3-5-sonnet-20241022"}
  ],
  "huakai_sig": "<ed25519 signature over hop_chain bytes>",
  "huakai_pubkey_fp": "<sha256(pubkey)[:16]>"
}
```

关键设计：
- account_id 不暴露原 ID（隐私），只暴露 hash（user 可对账，不能直接定位账号给攻击者）
- 签名用 ed25519（小、快、ECDSA 一样安全）
- pubkey 在 `https://huakai.example/.well-known/huakai-pubkey.json` 公开发布 + key rotation 时旧 key 保留 90 天
- 默认每条 request 都签；高吞吐场景 hop_chain 可压缩

**HCSF schema 影响**：
- `Accounting` 加 `HopChain []HopAttestation` 字段
- 加 `Accounting.Signature` + `Accounting.PubkeyFingerprint`
- HCSFVersion 升 `"0.4.1"` profile（wire 不改，参考 P-2 synthesis Q3 决策）
- 加 INV-51（新）`hop_chain 必须 monotonically ts ↑`
- 加 INV-52（新）`Signature 非空时 PubkeyFingerprint 必须非空且 16 char hex`

**与 INV 守门关系**：不与 INV-14..49 冲突，独立增量。

**验证手段**：
- 提供 `huakai-verify` CLI（Go 单文件）：`huakai-verify <request_id> --pubkey-url=...` → 验证签名 + 与公开 audit ledger Merkle root 对账
- 提供 `/v1/audit/verify` API：传 request_id 返回签名 + Merkle proof
- 用户 dashboard 加"审计 / 我的请求" tab（前端 Round 11 后续 Round）

## 4. F-PRIV-001 详细 spec — No-Body Log Boundary

**行为**：所有 system log（zap/zerolog）严禁含：
- `prompt` / `completion` / `content` / `tool_input` / `tool_output` 字段值
- `messages[].content[].text` 字段值
- `system` prompt 字段值
- response body bytes

**允许**含：
- `request_id` / `trace_id`
- `tenant_id` / `key_id_hash`
- `model_requested` / `model_actual` / `upstream_model_reported`
- `token_count_input` / `token_count_output` / `cache_hit_ratio`
- `latency_ms_total` / `latency_ms_first_token` / `latency_ms_tta`
- `status_code` / `error_class` / `error_code`
- `account_id_hash` / `pool_id` / `route_id`

**实施**：
- log 入口 wrapper 在 `backend/internal/log/redact.go`，所有 zap field 经过 allowlist 过滤
- per-field 类型断言禁止 `interface{} field` 直传
- CI 加 `staticcheck` 自定义规则 `huakai-no-body-in-log`：grep `prompt|content|messages` 在 zap.Any / zap.Reflect 周边出现就报错
- `Policy.DataRetention.RequestStore=false` 默认；只在 tenant 显式 opt-in `debug_capture=true` 才允许 body 落盘，且写 audit event

**HCSF schema 影响**：无（Policy.DataRetention 已存在）

## 5. F-TRUST-002 详细 spec — Upstream Model Attestation

**行为**：response 加 header（也写进 hop_chain 最后一跳）：

```
X-HUAKAI-Model-Requested: claude-3-opus-20240229
X-HUAKAI-Model-Delivered: claude-3-opus-20240229
X-HUAKAI-Upstream-Account-Hash: <sha1>
X-HUAKAI-Sig: <ed25519 base64>
X-HUAKAI-Pubkey-FP: <sha256(pubkey)[:16]>
```

如果 `Requested != Delivered`（合法降级 / 上游强制 substitute），仍签名，但 user 能立刻看到差异。

**反掺水机制**：
- HUAKAI 在 dispatch 决策瞬间记录 `model_actual = <route 选择的>`
- 调上游时记录 `upstream_response.model` 字段（OpenAI / Anthropic / Gemini 都返回 model 字段）
- 三者比对：requested == route_decision == upstream_reported；任何不一致都写入 audit + 用户可见
- 商家若想偷换模型，要么伪造上游响应（需破解 ed25519，不可行）要么改 HUAKAI source（破坏签名，用户对比公开 pubkey 立刻发现）

**HCSF schema 影响**：
- `RequestMeta.UpstreamModel` 已存在
- 新增 `Accounting.ModelChain { requested, route_decided, upstream_reported }`
- 新增 INV-53 `如果 Accounting.ModelChain 非空，三字段必须都填`

## 6. F-AUDIT-001 详细 spec — Signed Dispatch Ledger

**行为**：维护一个 append-only ledger 表（PostgreSQL），每行：

```
{ ledger_id, request_id, ts, hop_chain_bytes, signature, prev_merkle_root, merkle_root }
```

- 每条记录都签名 + 链接前一条的 merkle_root
- 每 N 条（如 1000）发布一个 Merkle root 到公开 `/audit/merkle-tree.json`
- 第三方可以拉 ledger snapshot + verify Merkle chain
- 与 `system_log` 完全分离的物理存储

**HCSF schema 影响**：
- 新增 backend DB table `audit_ledger`（不在 HCSF 内，是 backend 持久化层）
- HCSF Accounting 加 `LedgerID string` 引用 ledger row

## 7. F-PRIV-002 详细 spec — Log vs Ledger 拆分

**行为**：明确两条独立 stream：

| Stream | 用途 | 受众 | 内容 | 保留 |
|---|---|---|---|---|
| `system_log` | SRE / 故障排查 | Operator / SRE | error class + system metrics + redacted metadata | 30 day rolling |
| `audit_ledger` | 用户审计 / 反作弊 | End user + auditor + tenant | hop_chain + signature + Merkle | 7 year (compliance) |
| `user_body`（仅 opt-in） | 调试 prompt 用 | Tenant 自己 + debug 时段 | full prompt/completion | 24h auto-purge unless override |

**Why 拆**：不分开就会出现 "log 太大 grep 不动" 和 "audit 不签名无法 verify" 的混淆。

## 8. F-TRUST-003 详细 spec — User-Facing Usage API

**行为**：开放 `/v1/usage/me`：

```
GET /v1/usage/me?from=2026-05-13&to=2026-05-13
Authorization: Bearer <user_api_key>

Response:
{
  "tenant_id": "...",
  "period": "2026-05-13",
  "summary": { total_requests, total_tokens_input, total_tokens_output, total_cost_usd },
  "records": [
    { request_id, ts, model_requested, model_delivered, tokens_input, tokens_output, cost_usd, account_hash, ledger_id }
  ],
  "signature": "<ed25519 over summary>",
  "pubkey_fp": "..."
}
```

User 可定时下载 + 自己 verify 累加。

## 9. 切片建议（实施顺序，估 25 engineer-day total）

| Slice | Feature | engineer-day | 依赖 | Phase |
|---|---|---:|---|---|
| T0 | log redaction boundary + system_log vs audit_ledger 拆分（F-PRIV-001 / F-PRIV-002） | 3 | P-2 D0 完成 | Phase 4 |
| T1 | Accounting.HopChain schema patch + INV-51/52（F-TRUST-001 一部分） | 2 | T0 | Phase 4-5 |
| T2 | ed25519 keypair + 签名 + pubkey 发布 endpoint | 2 | T1 | Phase 4-5 |
| T3 | Accounting.ModelChain + 三方比对 + X-HUAKAI-* response headers（F-TRUST-002） | 3 | T2 | Phase 5 |
| T4 | audit_ledger DB table + Merkle chain + ledger_id 写入 HCSF（F-AUDIT-001） | 4 | T3 | Phase 5 |
| T5 | user-facing /v1/usage/me + signed records（F-TRUST-003） | 3 | T4 | Phase 5 |
| T6 | huakai-verify CLI + /v1/audit/verify endpoint | 3 | T4 | Phase 5-6 |
| T7 | F-AUDIT-002 token count cross-check + divergence alert | 3 | T3 | Phase 5-6 |
| T8 | frontend dashboard 加"我的审计 / 我的消费"页 | 2 | T5, Round 11 完 | Phase 5 |

**总 25 day implementer + ~10 day codex review + smoke**，calendar 估 6-7 周（与 P-2 ClientAdapter 25 day calendar 平行可压缩）。

## 10. 与现有 doc 的合并/修订

需修订（合 synthesis 通过后做）：

- `docs/01_PROJECT_BRIEF.md`：升级 "运营平台" → "可验证运营平台"
- `docs/03_FEATURE_PARITY_MATRIX.md`：加 F-TRUST-001/002/003 + F-PRIV-001/002 + F-AUDIT-001/002 共 7 行
- `docs/17_FEATURE_LEVEL_MATRIX.md`：加 Trust Chain / Privacy / Verifiable Audit 3 行 capability
- `docs/16_PHASED_DELIVERY_PLAN.md`：Phase 4 加 F-PRIV-001/002 强制；Phase 5 加 F-TRUST-001/002/003 + F-AUDIT-001
- `docs/10_RISK_REGISTER.md`：加风险条 R-TRUST-001（pubkey 泄漏） / R-TRUST-002（签名性能 hot path） / R-TRUST-003（用户拒绝签名验证）
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`：加 AT-TRUST-001..003 + AT-PRIV-001..002 + AT-AUDIT-001..002 共 7 条
- HCSF spec：升级到 v0.4.1（profile bump，wire 不动）；加 INV-51/52/53

## 11. Owner 决策点（待 Codex lane 出后 synthesis）

1. **签名算法**：ed25519（推荐）/ RSA-PSS / ECDSA P-256
2. **凭据存储**：private key 放在哪？env var / KMS / Vault
3. **公钥发布**：`/.well-known/huakai-pubkey.json`（HUAKAI 自己发）vs 第三方背书（如 Sigstore Fulcio）
4. **默认开/关**：T0-T5 是否 Personal Edition 也默认开？还是只 SaaS Edition 强制
5. **Merkle root 发布频率**：每条 / 每 100 条 / 每小时 / 每天
6. **响应延迟预算**：签名 +1-2 ms 可接受还是要异步签
7. **第三方审计 endpoint**：开放给所有人 vs 只开放给注册租户
8. **PII 边界**：account_id_hash 怎么算？SHA-256(account_id || tenant_secret) 防 rainbow？
9. **violation 处理**：检测到 model substitution / token divergence 时 — 立即 refund / 标记 + 告警 / 默认拒绝路由这账号
10. **与 Vendor SDK 兼容**：上游 vendor 返回 model 字段格式可能漂移，怎么 normalize？

## 12. 与 P-2 ClientAdapter synthesis 的关系

P-2 D0 schema patch 应**预留** `Accounting.HopChain` / `Accounting.ModelChain` / `Accounting.LedgerID` 字段（即使 P-2 D1-D12 不填，只占位），避免后续大改 envelope。

**建议 P-2 D0 顺手做的 schema patch**：

```go
// backend/internal/proto/accounting.go (新增)
type HopAttestation struct {
    Hop string `json:"hop"`
    Timestamp string `json:"ts"`
    Detail json.RawMessage `json:"detail,omitempty"`
}

type ModelChain struct {
    Requested string `json:"requested,omitempty"`
    RouteDecided string `json:"route_decided,omitempty"`
    UpstreamReported string `json:"upstream_reported,omitempty"`
}

type Accounting struct {
    // ...现有字段
    HopChain []HopAttestation `json:"hop_chain,omitempty"`
    ModelChain *ModelChain `json:"model_chain,omitempty"`
    LedgerID string `json:"ledger_id,omitempty"`
    Signature string `json:"signature,omitempty"`
    PubkeyFingerprint string `json:"pubkey_fp,omitempty"`
}
```

这样 P-2 ClientAdapter D1-D12 跑通后，T0-T5 trust-chain 实施只需填字段，不动 schema。

## 13. 反例 cite（必须读 source 守 CLAUDE.md #12）

- `sub2api@dbc8ae6:backend/cmd/api/middleware/logger.go` — 待 codex 补 file:line（sub2api log middleware 含 body）
- `litellm@<HEAD>:litellm/proxy/utils.py` — 待 codex 补（LiteLLM 默认 log mode 可包 prompt）
- `portkey-gateway@<HEAD>:src/handlers/*` — 待 codex 补 cache hit logic（portkey 不验签）
- `new-api@<HEAD>:relay/*.go` — 待 codex 补 token 数信任源（new-api 信任上游 usage 字段不交叉验证）

---

Claude lane plan 起草时间：2026-05-13T07:30:00Z  
Claude session: 04d37436-9b8b-4a8e-b2c4-24538cfd6f23  
Synthesis 等 Codex lane 完成（background `bs2hxrx76`）。
