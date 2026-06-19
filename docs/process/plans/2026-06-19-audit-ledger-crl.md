# audit-ledger 验签忽略 key 吊销(CRL)修复(wave-2 审计 wy94u3tn9 最后一个 S1)

## 背景 / 来源
审计确认 S1:audit-ledger 验签路径只查"签名 + key 有效窗口",**从不查吊销列表**;而 trust-receipt
路径却查(cost_receipt 的 applyReceiptRevocationOverlay)。后果:audit signing key 泄露后运维登记吊销
(HUAKAI_TRUST_REVOKED_KEYS_JSON/FILE),持有泄露私钥的攻击者仍能伪造 audit 条目,而 /v1/audit/verify、
/export、/proof 以及 huakai-verify 的 request-id 流都报 signature_valid —— 吊销机制对 audit-ledger 形同虚设
(这正是该子系统"独立防篡改"目的所在)。

## 真码摸透(已读,主分支 cbf7ae34)
- audit 验签:gatewayhttp/audit_verify_handler.go:313 verifyAuditLedgerEntrySignature = EntryHash→
  VerifySignatureWithRegistry→LookupPubkey→signatureOutsideKeyWindow,返回 SignatureVerification{Valid,KeyStatus,Reason};
  KeyStatus 来自 key.Status()(active/rotated/unknown,**无 revoked**)。**注意**:窗口外已返回 Valid:false(非密码学失效),
  故"吊销→Valid:false"与既有模式一致。
- 消费方:audit_verify_handler.go:149(/v1/audit/verify)、auditexporthttp/handler.go:137(proof)/:301(export 批量),
  都经 AuditVerifyResponseForEntry(ctx, entry, registry)。
- 吊销机制(要复用的 HUAKAI 自有先例,均在 gatewayhttp 同包/trusthttp):
  trusthttp.Revocations = map[string]Revocation,Lookup(fp)(内部 normalizeFingerprintString 归一);
  trusthttp.LoadRevocationsFromEnv()(读 HUAKAI_TRUST_REVOKED_KEYS_JSON/FILE);
  cost_receipt 的 applyReceiptRevocationOverlay:Lookup 命中→KeyStatus="revoked"+Reason="key_revoked"。
  **路由层 receiptDeps.Revocations 不显式 wire(nil)→ 走 LoadRevocationsFromEnv 兜底**——audit 沿用同法,
  故 **routes.go 无需改**(nil→env)。
- CLI:cmd/huakai-verify/main.go request-id 流 fetchAudit→fetchPubKey(只返 ed25519.PublicKey,**丢掉 Revoked**)→
  verifyEntryProof,不查吊销;detached 流(selectPubkeyRecord→record.Revoked→key_revoked,line 169)却查。doc.Revoked
  已被 revokedFingerprintSet(doc)解析,只是 request-id 流没用。

## #16 三镜像
sub2api/new-api 有 **session/JWT 吊销**(auth_session_revocation、jwt middleware)——是会话令牌吊销,**非 audit-ledger
签名 key 的 CRL**(防篡改用途),概念不同。CLIProxyAPI 纯 relay 无审计链。**no-equivalent**:audit-ledger key CRL 是
HUAKAI trust-chain 自有;本切片把 HUAKAI 自己的 trust-receipt 吊销语义补到 audit-ledger 验签路径,保持子系统一致
(生态升级:同一吊销来源覆盖 receipt + audit + CLI 三个验证面)。

## 修复(additive,不动 schema/money;失败安全)
### A. 服务端(gatewayhttp + auditexporthttp)
- verifyAuditLedgerEntrySignature 加 revocations 参数:签名+窗口通过(Valid=true)后,若
  revocations.Lookup(TrimSpace(entry.PubkeyFingerprint)) 命中 → 返回 {Valid:false, KeyStatus:"revoked", Reason:"key_revoked"}
  (与 signature_outside_key_window 同模式)。
- 把 revocations 穿过 auditVerifyResponseWithRegistry / AuditVerifyResponseForEntry(加参数)。
- AuditVerifyStaticDeps 加 Revocations trusthttp.Revocations 字段(默认 nil);新 auditRevocationsFromDeps(d)
  解析器(nil→LoadRevocationsFromEnv,镜像 receiptRevocationsFromDeps)。verify handler **每请求解析一次**、传入。
- auditexporthttp.Deps 加 Revocations 字段;export/proof handler **每请求解析一次**(env-load 失败→503 失败安全,
  绝不静默跳过吊销检查),传入 AuditVerifyResponseForEntry / verifyResponses。
- routes.go 不改(nil→env,与 receipts 一致);新字段默认 nil 故既有 struct literal 仍编译。

### B. CLI(cmd/huakai-verify request-id 流)
- fetchPubKey 额外返回该 fingerprint 是否在 doc.Revoked 集合内(doc 已解析 revokedFingerprintSet);request-id 流
  在验签通过后,若 fingerprint 命中吊销 → 把结果标记/失败为 key_revoked(与 detached 流 line 169 一致)。独立第二道:
  即便不信网关响应,CLI 也据自取的 well-known doc.Revoked 拒吊销 key。

## 测试(变异可证)
- 服务端:吊销 fingerprint 的 entry 经 AuditVerifyResponseForEntry(注入含该 fp 的 Revocations)→ KeyStatus="revoked"、
  SignatureValid=false、Reason="key_revoked"(变异:删吊销 overlay → KeyStatus 仍 active/SignatureValid=true → RED)。
  未吊销 fingerprint → 不受影响(签名有效仍 valid)。export/proof 经吊销 key 的条目同样降级。
- env-load 失败 → 503 失败安全(变异:改成静默跳过 → 吊销 key 仍 valid → RED)。
- CLI:request-id 流对吊销 fingerprint → key_revoked(变异:不查 doc.Revoked → valid → RED)。
- 干净基线 -count=1:gatewayhttp / auditexporthttp / cmd-gateway(含 wiring/openapi)/ huakai-verify / codebudget 全绿。

## blast radius
gatewayhttp/audit_verify_handler.go + auditexporthttp/handler.go + cmd/huakai-verify/main.go(+ 测试)。碰 gatewayhttp
谨慎 additive(只加参数 + 加吊销 overlay,不改既有签名/窗口逻辑)。对抗审查零 S0/S1 后合并。
