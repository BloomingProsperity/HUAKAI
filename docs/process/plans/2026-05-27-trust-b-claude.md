# TRUST-B Claude Lane Plan

Lane: claude-pm
Time: 2026-05-27T13:00:00Z
Trigger: TRUST-A commit 67659a6 已落 + Synthesis plan D 决策已合,parallel-draft round (CLAUDE.md #10)
Cross-lane: 见 `2026-05-27-trust-b-codex.md` (Codex lane,后续 synthesis 比对)

## §0 元信息

Owner D 决策 (已合, Synthesis plan):
- D-1=B 扩展 payload (provider+model+request_id+cost_cents+token_counts+price_snapshot+validation_state+redacted_metadata_allowlist)
- D-2=A `.well-known/huakai-pubkey.json` 标准 + `/v1/audit/pubkey(s)` 兼容
- D-3=A 三 verify endpoint (`/v1/trust/verify` 新 detached + `/v1/audit/verify` Merkle 不破 + `/v1/receipts/{id}/verify` 已存在)
- D-4=A 红 badge + warning banner (TRUST-A 落)
- D-5=A 双轨 (response inline provisional + final billing event detached)
- D-6=A schema forward-compat
- D-7 default header 主 + body extension optional
- D-8=A fail-open + unverified + paid request 进 operator review queue
- D-9=A 复用 0013 schema 不加新表

## §1 现状盘点 (TRUST-B 接通点已知)

| 组件 | 已存在 | 缺口 |
|---|---|---|
| ed25519 key | `sign/keygen.go` + `sign/signer.go` (Fingerprint = sha256[:8] = 16 hex) | 生产 key rotation grace + revocation list |
| Signer interface | `auditledger/signer.go` Sign+Verify;env `HUAKAI_TRUST_LEDGER_ED25519_KEY_BASE64` + `HUAKAI_TRUST_LEDGER_PUBKEYS_JSON` | trust receipt 签名 (不是 ledger entry 签名) 需新 Signer 路径 |
| Canonical JSON | `auditledger/canonical.go` (schema_version=trust.ledger.v1, 字段 ledger_id/occurred_at/request_id/tenant_scope_ref/hop_chain/model_chain/prev_merkle_root) | lite receipt canonical form (字段更广,含 cost+token+price) |
| Pubkey registry | `auditledger/pubkey_registry.go` GetByFingerprint+ListAll+Rotate | 新 `.well-known/huakai-pubkey.json` JWK 风格分发 |
| Verify endpoint | `/v1/audit/verify` (Merkle) + `/v1/audit/pubkeys` + `/v1/audit/pubkey/<fp>` + `/v1/receipts/{id}/verify` | 新 `/v1/trust/verify` detached;trust receipt 派生 layer |
| Audit ledger | Postgres + Memory + Noop + DLQ + Merkle | trust receipt 与 ledger 关联映射 |
| Response 接通 | TRUST-A WriteResponseHeaders 4 个 header (X-Huakai-Upstream-Provider/Model/Status/Request-Id) | 加 X-Huakai-Trust-Signature + 升 status 到 signed-only/verified |
| Status vocab | trust.Status (verified/signed-only/unverified/missing/mismatch) | ResponseStatus 升级:Persisted+signer 完成 → signed-only |

**关键**: 基础设施 90% 已建好,TRUST-B 是接通+扩展+新外露层。

## §2 切片清单

| 切片 | 范围 | 工时 | 主要文件 / 包 | 前置 | 风险 |
|---|---|---|---|---|---|
| TRUST-B-1 Lite receipt canonical contract | 定义 trust_receipt_v1 canonical form + 字段集 + sign payload | 0.5 天 | 新 `backend/internal/trust/receipt.go` (canonical + payload struct) | TRUST-A commit | canonical drift 风险 (字段顺序/编码) |
| TRUST-B-2 Signer integration + receipt 派生 + response inline | 在 handler 成功响应路径 sign provisional receipt + 写 X-Huakai-Trust-Signature header + 升 status | 0.75 天 | `backend/internal/trust/signer_bridge.go` (新) + `gatewayhttp/chat_completions_handler_headers.go` (改) + `chat_completions_stream.go` (改) | B-1 | inline header 长度 (~88 base64 bytes signature 可以);流式占位仍 unverified |
| TRUST-B-3 Final billing detached + audit_ledger 关联 | settle worker 在 cost 落库后派生 final receipt → 用 detached signer + 存 ledger.signature 字段 (复用 0013 schema) | 0.75 天 | `backend/internal/billing/...` settle path + `auditledger/postgres.go` Append 已签 (复用) | B-2 | 时序竞态 (settle 早于/晚于 inline header) |
| TRUST-B-4 Pubkey 分发 + verify endpoint | 新 `.well-known/huakai-pubkey.json` (JWK Set 风格) + 新 `/v1/trust/verify` detached + `/v1/audit/pubkey(s)` 仍可用 | 0.5 天 | `backend/internal/gatewayhttp/wellknown_pubkey_handler.go` (新? 非 frozen 包,但 gateway 路由器在 gatewayhttp 冻结包内 — 路由注册改既有 router 文件) + `trust/verify_handler.go` (新) | B-2 | 多 key (current/grace/revoked) JSON 形态 |

**合计 ~2.5 天**, 4 commits。每个 commit 含 TDD red→green→R1+R2 review。

## §3 ed25519 key lifecycle

### §3.1 启动初始化 (已现 + 加强)

现状:`auditledger.signer.go` 从 `HUAKAI_TRUST_LEDGER_ED25519_KEY_BASE64` env 读取 base64 priv key,32 bytes seed → ed25519.PrivateKey。`HUAKAI_TRUST_LEDGER_PUBKEYS_JSON` env 读取已发布公钥列表 (含 ValidUntil grace metadata)。

TRUST-B 新增: trust receipt signer 复用同 key,**不**生新 keypair (避免 fingerprint 双系统迷惑)。同一 fingerprint 对 audit ledger entry 签 + trust receipt 签。这是 D-9=A 复用 schema 的自然延伸。

### §3.2 多实例共享 key (生产)

多实例都从同一 env 加载 (Kubernetes Secret / Docker Secret 注入),所有实例签名同 fingerprint。无 leader-only 限制 (与 audit ledger Append 用 advisory lock 串行不同 — receipt 签可并发)。

### §3.3 90 天 rotation

现状 `auditledger.PubkeyRegistrar.Rotate(ctx, oldFingerprint, newPubkey, effectiveAt)` 接口已设。TRUST-B 不引入新 rotation 工具,沿用:
- 启动时 env 列出 current + grace fingerprints
- 新 receipt 用 current fingerprint 签
- verify 接口接受任一已发布 fingerprint (按 ValidUntil 排序)
- grace period: 30 天 (新 key 启用 + 老 key 仍可 verify);90 天后老 key 移出 publish list

### §3.4 key 泄露应急

不在 TRUST-B 范围 (D-OWNER OCAW)。但 record risk: key 泄露后 attacker 可签假 receipt → user verify 仍通过。 缓解:
- pubkey JSON 加 `revoked_at` 字段 (D 决策点 ④)
- verify 接口对 revoked fingerprint 返 `verified-but-key-revoked` 状态 (扩 vocab)

## §4 Canonical JSON

### §4.1 选择

**用 HUAKAI 自己的 canonical form**, 不引 RFC 8785 (JCS) 第三方包。理由:
- `auditledger/canonical.go` 已有同风格 (按 schema 显式列字段 + 显式排序),trust receipt 紧跟一致
- 减外部依赖
- 字段集是 HUAKAI 控制,不需 deep map sort 通用算法

### §4.2 trust_receipt_v1 字段集 + 顺序

```
{
  "schema_version": "trust.receipt.v1",
  "issued_at":      "<RFC3339Nano UTC>",
  "request_id":     "<request_id>",
  "tenant_scope_ref": "<scope_ref or empty>",
  "provider":       "<upstream provider>",
  "model":          "<route_decided model>",
  "cost_cents":     <int>,
  "token_counts":   { "input": <int>, "output": <int> },
  "price_snapshot": { "input_cents_per_million": <int>, "output_cents_per_million": <int> },
  "validation_state": "<verified|provisional|unknown>",
  "redacted_metadata_allowlist": [ "key1", "key2", ... ]
}
```

字段顺序固定 (canonical.go 风格 buf.Write 显式列),不用 sort。

### §4.3 数字精度

- `cost_cents`: int (整数 cents,无浮点)
- `token_counts.input/output`: int
- `price_snapshot.input/output`: int (cents per million tokens,整数;避免 float 序列化漂)

如果将来需要 float (e.g. fractional cost):用 string 表示 "1234.56" 而不是 number 字面,避免 JSON float 跨语言差异。

### §4.4 Unicode escape

- ASCII 字符不 escape (`a` 直接写)
- 非 ASCII (中文/emoji): 用 `\u` 16-bit 序列 (现有 canonical.go 已是这种)
- 防 BOM / 防尾空白

## §5 双轨签名时机

### §5.1 Inline provisional (response 路径)

**非流式 (`chat_completions_handler_headers.go`)**:
- 现在 `WriteHuakaiHeaders` 调用 `trust.WriteResponseHeaders` 写 4 个 trust header (TRUST-A 已落)
- TRUST-B 在 settle 完成 (cost 已知) 后,签 provisional receipt:
  ```
  payload = canonicalReceipt(provisional state)
  sig, fp = signer.Sign(ctx, payload)
  h.Set("X-Huakai-Trust-Signature", base64(sig))
  h.Set("X-Huakai-Trust-Pubkey-Fingerprint", fp)
  // status 升 signed-only
  ```
- 签的 payload 是 `provisional`,因为 final billing event 还可能 detach 调整 (e.g. cost recompute)

**流式 (`chat_completions_stream.go`)**:
- 流头写出时 cost 未知 → 不能签
- 流末 trailer 阶段写: 同非流式逻辑
- 现 codex 用 `DisabledLedgerResult()` 占位 → TRUST-B 改为流末 settle 后写 trust signature trailer

### §5.2 Final detached (billing settle 后)

- billing settle worker 已存在 (现 `billing/...`)
- 在 settle 完成写 audit_ledger 时,additional 派生 final trust receipt,用 detached signer 签
- 存到 audit_ledger_entries.signature 字段 (复用 0013 schema — 当前已存 ledger entry sign,不存 receipt sign)
- 但 ledger entry sign 是 entry hash 签名,receipt sign 是 receipt payload 签名 — 不同 payload,不能复用同一字段

**冲突点 → D 决策 ②** (见 §13)

### §5.3 Provisional → Final 状态升

- Inline 阶段:`signed-only` (有签名但未与 audit ledger 对账)
- 用户调 `/v1/trust/verify` 用 detached signature 比 provisional 比 → 一致 + 当前 fp 未 revoke → `verified`
- 不一致 → `mismatch`

## §6 Receipt 派生 + ID 格式

### §6.1 Receipt ID

提议:不引入新 receipt_id 字段,**直接用 request_id 作 receipt 主键**。理由:
- audit_ledger_entries 已用 request_id 作天然 unique key
- D-9=A 复用 schema 不加新表 → 不增 receipt_id 列
- `/v1/receipts/{request_id}/verify` URL 已用 request_id

### §6.2 Receipt 与 Ledger 关系

一对一: 一个 request_id → 一个 ledger entry + 一个 trust receipt。
Receipt 由 ledger entry 字段 + cost/token/price snapshot **派生**,不另存:
- 数据库只存 ledger entry (沿用 0013 schema)
- `/v1/trust/verify` 接受 receipt payload + signature → 内部派生 canonical → 验签
- `/v1/receipts/{id}/verify` 接受 receipt_id → 内部查 ledger entry + cost snapshot → 派生 receipt → 验签

**冲突点 → D 决策 ③** (见 §13: cost/token/price snapshot 从哪查?当前 billing 表是分离的)

## §7 Pubkey 分发

### §7.1 `.well-known/huakai-pubkey.json` 格式

提议: 简化版 JWK Set,不引第三方 lib:

```
{
  "schema_version": "huakai.pubkey.v1",
  "issued_at": "2026-05-27T13:00:00Z",
  "keys": [
    {
      "fingerprint": "abc12345def67890",
      "algorithm": "ed25519",
      "public_key_b64": "<base64 32-byte raw pubkey>",
      "valid_from": "2026-02-27T00:00:00Z",
      "valid_until": "2026-08-27T00:00:00Z",
      "status": "active"   // active / grace / revoked
    },
    ...
  ]
}
```

- 不 JWK 字段名 (`kty`/`crv`/`x`),用 HUAKAI 风格直观字段
- caching: `Cache-Control: public, max-age=300, stale-while-revalidate=60` (5 分钟新鲜度)
- ETag 用整 JSON sha256 前 16 字符
- TLS only (HTTPS),HSTS

### §7.2 兼容 `/v1/audit/pubkey(s)`

- 旧 endpoint `/v1/audit/pubkeys` 返同数据 (已存在)
- 新 `.well-known/huakai-pubkey.json` 是 D-2=A 标准路径
- 两 endpoint 路由器内复用同 handler 函数,JSON 输出相同

## §8 Verify endpoint

### §8.1 `/v1/trust/verify` (新 detached)

```
POST /v1/trust/verify
Content-Type: application/json

{
  "receipt": { ... canonical receipt payload ... },
  "signature_b64": "<base64 signature>",
  "fingerprint": "<pubkey fingerprint>"
}
```

返:
```
{
  "valid": true|false,
  "status": "verified|signed-only|mismatch|key-not-found|key-revoked|signature-invalid",
  "reason": "<human-readable>",
  "verified_against_fingerprint": "<fp matched, or empty>"
}
```

逻辑:
1. canonicalize receipt → bytes
2. lookup fingerprint in PubkeyRegistry
3. if not found → status=key-not-found
4. if revoked → status=key-revoked
5. ed25519.Verify(pub, canonical, sig)
6. if mismatch → status=signature-invalid
7. ok → status=verified

**公开,无 auth** (signing 是 user-facing trust 卖点,商家不能藏 receipt)。

### §8.2 `/v1/audit/verify` (旧 Merkle,不破)

保留现有 handler `audit_verify_handler.go`,不动。

### §8.3 `/v1/receipts/{request_id}/verify` (旧已存在)

现有 handler 已实现 inline-by-receipt 路径。TRUST-B 后扩展返同新 status vocab。

### §8.4 Rate limit

公开 endpoint 必有 rate limit。提议:
- IP-based: 60 req/min (匿名)
- API key authn 用户:600 req/min
- 复用现有 rate limiter 中间件 (查 `internal/middleware/`)

## §9 D-8 fail-open 实施

### §9.1 Signer 不可用检测

启动期:
- 读 env `HUAKAI_TRUST_LEDGER_ED25519_KEY_BASE64`
- 若空 → log warn + signer = nil → **生产模式拒启动** (signer never configured 是配置错)
- 若非空但 decode 失败 → 同上拒启动

runtime:
- Signer.Sign() return error → 触发 fail-open path
- 5 分钟窗内 Sign 失败率 > 50% → 上 Prometheus alert

### §9.2 "unverified" 状态出现的触发点

1. signer down (runtime Sign error)
2. signer never configured (env 空,只在 dev 允许)
3. ledger Append 失败 (TRUST-A 已有 → 升 unverified)
4. provider hop 缺失 (meta.Provider 空)
5. model chain 缺失
6. request_id 不一致

### §9.3 Operator review queue

paid request (cost > 0) 进 unverified state → 写 `operator_review_queue` 表:
- request_id, tenant_id, reason, created_at
- 现有 DLQ 表是 audit ledger DLQ (`audit_ledger_dlq`),复用还是新表? → D 决策 ①

operator UI (frontend admin panel) 可读 queue 处理。本切片不实现 UI,只实现写队列。

### §9.4 区分 signer down vs never configured

启动期 fail-closed (never configured 是配置错,拒启动)。
runtime fail-open (down 是临时错,paid request 进 queue)。

## §10 Mismatch detection

### §10.1 商家 fake provider/model

场景:HUAKAI operator 改 ledger entry provider 字段 (fake anthropic 当 openai)。
- response header 仍标 dispatch path 真实值 (operator 改不到 header 内存)
- TRUST-A 已 detect (provider/model/request_id 比对 ledger vs header)
- TRUST-B 加 trust receipt signature:operator 若仅改 ledger DB 不改 signed receipt header → user verify trust receipt 通过 + 状态 verified;若也改 receipt header → signature verify fail → status=signature-invalid

**多层防御**:
- L1 wire header (TRUST-A): operator 无法实时 forge
- L2 inline signature (TRUST-B-2): operator 无法 forge 未来 receipt (key 不在 operator 手)
- L3 detached final (TRUST-B-3): operator 不能改 audit ledger 的 entry (Merkle 链)
- L4 verify endpoint (TRUST-B-4): user 独立 verify

### §10.2 Verify pass 但 meta mismatch

- ledger entry signature valid
- 但 receipt provider != ledger provider
- → 状态返 `mismatch` (不是 verified)
- reason: "signature valid but metadata mismatch"

### §10.3 D-4 红 badge 是否新场景

TRUST-A 已配 red badge for missing/mismatch。TRUST-B 不新增 tone vocab,只升 status:
- verified (绿)
- signed-only (黄) — 新引入
- unverified (灰)
- missing (红)
- mismatch (红)

## §11 测试策略

### §11.1 每切片 mutation 自检

**TRUST-B-1**:
- canonical receipt 字段顺序变 → sign payload 变 → verify fail (red)
- schema_version 改 → verify path 走错 schema 解析 → red
- timestamp 精度 lose (RFC3339Nano vs RFC3339) → sign payload 变 → red

**TRUST-B-2**:
- 删 inline signature 设置 → status 仍 unverified (red,期望 signed-only)
- signer.Sign() 永远 return 同样 sig → verify fail on different payloads (red)
- fingerprint 不取 signer 的实时 → 取错 fp → verify fail (red)

**TRUST-B-3**:
- ledger entry 不存 final receipt 派生 → /v1/receipts/{id}/verify 找不到 sig (red)
- settle worker 跳过 detached 签 → final state 缺签 (red)

**TRUST-B-4**:
- pubkey JSON 漏 fingerprint → verify endpoint return key-not-found (red, mutation 自检证明 endpoint 真用 registry)
- `/v1/trust/verify` accepts unsigned payload (空 sig) → 错返 valid=true (red)
- rate limit 不生效 → 100 req/sec 通过 (red)

### §11.2 集成测试

E2E flow:
1. 发请求 → 接收 headers (含 trust signature)
2. 调用 `/v1/trust/verify` 用 header data → 状态 verified
3. 篡改 receipt payload → status mismatch
4. 用过期 key fingerprint → status key-revoked

复用 `gatewayhttp/trust_chain_e2e_test.go` 框架。

## §12 参考项目对照 (CLAUDE.md #15)

| 项目 | 路径 | 暴露方式 | 签名? | HUAKAI 升级 (架构/算法/生态) |
|---|---|---|---|---|
| Sigstore (Rekor) | ~/refs/(未 clone, 标准 transparency log) | DSSE envelope + ed25519/ECDSA + Trillian log | ✓ | 架构: 移除 Trillian 依赖 (over-engineered for chat); 算法: 用 ed25519 而非 cosign 默认; 生态: 内建 well-known + 5 状态 vocab |
| LiteLLM | ~/refs/litellm (待 verify) | `_hidden_params` 含 cost/model | ✗ 不签 | 升: 全 receipt 签 + canonical form;LiteLLM 用户拿到 dict 但无法 verify |
| Portkey | ~/refs/(未 clone) | virtual key + response include cost/model | ✗ 不签 | 同 LiteLLM |
| Helicone | ~/refs/helicone | proxy 透传 + log | ✗ 不签 | 升: signature on wire + audit ledger Merkle; Helicone 只 log 不 sign |
| envoy-ai-gateway | ~/refs/envoy-ai-gateway | upstream filter + log | ✗ 不签 | 同上 |
| CLIProxyAPI | ~/refs/CLIProxyAPI-latest | request 端 only | n/a | TRUST 链全是 HUAKAI 原创 |
| anthropic-sdk-python | ~/refs/anthropic-sdk-python | 客户端 SDK,无 server-side sign | n/a | n/a (上游) |

**HUAKAI fusion-upgrade**: 7 项目无任何项目签 response side. TRUST 链是 HUAKAI 与所有现有 AI gateway 的根本差异 (project_core_trust_chain_differentiator).

三维: **架构**(自研 5 状态 + 三 verify endpoint 入口) + **算法**(canonical form HUAKAI-tuned + Merkle hash chain 简化) + **生态**(`.well-known` 标准 + operator review queue + user-facing CLI verify).

## §13 风险登记

| Risk ID | 描述 | 严重度 | 缓解 |
|---|---|---|---|
| R-TB-001 | key 泄露后 attacker 可签假 receipt | 高 | pubkey 加 revoked_at;verify endpoint return key-revoked 状态;OCAW key-rotation drill |
| R-TB-002 | canonical form drift (HUAKAI dev 新加字段未更新 schema_version) | 中 | schema_version 严格 v1 锁定; CI 校 canonical bytes 不变 |
| R-TB-003 | replay attack: 旧 receipt 伪装新请求 | 中 | receipt 含 issued_at + request_id (request_id 已 unique) |
| R-TB-004 | pubkey CDN 中间人替换 (HTTPS pin 缺失) | 中 | HSTS + Public-Key-Pinning header;客户端 SDK 内嵌 trusted fingerprint set (D 决策 ⑤) |
| R-TB-005 | `/v1/trust/verify` DoS (公开 endpoint 大量请求) | 中 | rate limit 60/min anon;Prometheus alert |
| R-TB-006 | provisional 与 final receipt cost 不一致 (settle 改了 cost) | 中 | provisional sig payload 含 `validation_state=provisional`,user 知道未 final;final sig 由独立 endpoint 提供 |
| R-TB-007 | tenant scope_ref leak in pubkey JSON | 低 | pubkey JSON 不含 tenant 任何字段 (全局) |

## §14 Owner D 决策点 (本切片专属)

### D-OWNER ① operator_review_queue 表设计
- A: 复用 `audit_ledger_dlq` 表 (现有 DLQ infra)
- B: 新表 `operator_review_queue` 字段更专 (request_id, tenant, reason, status, assigned_to)
- 推荐 B (clearer separation)

### D-OWNER ② Final detached receipt 存哪
- A: 复用 audit_ledger_entries.signature 字段 (但当前签的是 entry hash,不是 receipt)
- B: 加新 column `audit_ledger_entries.receipt_signature TEXT` + `receipt_pubkey_fingerprint TEXT` (违 D-9=A 不加新列?)
- C: 不存,每次 verify 重新派生 + 实时签 (浪费 CPU)
- D-9=A 严格不加表/列 → A 或 C
- 推荐 A:audit ledger entry 签的是 canonical entry hash;**receipt signature 派生于 entry**,因此 receipt 自身不需要独存 signature;`/v1/receipts/{id}/verify` 内部查 ledger entry → 用同一 fingerprint signature 验

实际等价:**audit ledger entry signature ≈ receipt signature**(因为 receipt 是 entry 的 view,canonical form 相同)。
→ **隐藏决策**:trust_receipt_v1 canonical = audit ledger entry canonical (同字段集) 还是不同?
- 若同: 复用 sign 字段 ✓ ,但 D-1=B 的 "cost_cents/token_counts/price_snapshot" 字段 ledger entry 未包含
- 若不同: 需新签
- → 这是真冲突 → 见 D-OWNER ②.2

### D-OWNER ②.2 Cost/Token/Price snapshot 存哪
- ledger entry 当前不存 cost/token/price (只存 hop_chain + model_chain)
- D-1=B receipt 含这些字段
- D-9=A 不加表/列
- **当前 ledger payload 缺 cost** → receipt 不能完全派生于 ledger entry
- 选 A: 在 ledger entry 加 `cost_cents_snapshot int` + `token_input int` + `token_output int` (违 D-9=A)
- 选 B: 不签 cost (lite payload 缩减) (违 D-1=B)
- 选 C: cost 留 billing 表 (现有 billing schema),receipt 派生时 join 查 (违 D-9=A 不加列? — 不,只 join 不加列)
- 推荐 C:`/v1/receipts/{id}/verify` 内部 join `billing_costs` 表派生 receipt → 签

### D-OWNER ③ Pubkey 客户端 trust anchor
- A: 客户端 SDK 内嵌 trusted fingerprint set (release-time pin)
- B: 客户端启动从 HUAKAI 自身拉 (HTTPS),TLS 是唯一 trust root
- C: TOFU (Trust on first use) + 后续校 fingerprint 不变
- 推荐 A (release-time pin) — 公开 transparency log + 客户端验

### D-OWNER ④ Pubkey revoked_at 字段
- A: pubkey JSON 加 `revoked_at` 字段 (optional);verify endpoint 返 `key-revoked` 状态
- B: 不引入 (key 泄露事件极少,人工通知足够)
- 推荐 A (低成本高安全)

### D-OWNER ⑤ Rate limit endpoint authentication
- A: 公开 endpoint,IP-based rate limit (60/min anon)
- B: 公开但要求 API key (login required)
- 推荐 A (公开 transparency 是 trust 卖点)

## §15 工时 + commit 计划

| Commit | 内容 | 工时 | review |
|---|---|---|---|
| 1 | TRUST-B-1 receipt canonical + payload type + test | 0.5 天 | R1+R2 |
| 2 | TRUST-B-2 inline provisional sign + response header X-Huakai-Trust-Signature | 0.75 天 | R1+R2 |
| 3 | TRUST-B-3 final detached sign + billing join + audit ledger 关联 (C 路径 join billing) | 0.75 天 | R1+R2 |
| 4 | TRUST-B-4 pubkey well-known + /v1/trust/verify + rate limit | 0.5 天 | R1+R2 |

每 commit 闭环 (CLAUDE.md #8 ≤2 round review)。

## §16 与 F-TRUST-001 (Merkle 完整 C) 路径关系

- TRUST-B 完成 = F-TRUST-001 Phase 1 lite 完毕
- Phase 2 (full Merkle C) Mandatory Roadmap,本切片不实施;schema 字段 prev_root/curr_root/merkle_proof 已存,Phase 2 启用 inline 签 + 公开 root

## §17 Clean-room 约束

不读 sub2api / new-api / all-api-hub / one-api (LGPL)。
读 Sigstore (Apache-2.0) + envoy-ai-gateway (MIT) + Helicone (MIT) + LiteLLM (MIT) 可参考。
所有借鉴必 cite + 升级 delta 明示。

---

Lane: claude-pm
Time: 2026-05-27T13:00:00Z UTC
