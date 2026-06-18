# DEFERRED — API-key expiry 更新写路径 (PR pending) 审查后续

对抗审查 (wykz5rogo, 13 agents) 判 `BLOCKERS_FOUND`: 1×S1 + 6 minor(2×S2, 4×S3)。本切片内**已修** S1 + 全部 6 个
minor 中除一个 pre-existing/out-of-scope 项;下记一个延后项。

## 已在切片内修复
- **S1 — 组合 name/status + expires_at PATCH 未测,动态 UPDATE 占位符 $n 算术只在 argIdx==1 被验**: 加
  TestUserKey_PatchExpiry_TriStateAndRejectPast 两个组合子用例(name+expires → expires_at@$2 WHERE $3/$4/$5;
  name+status+expires → expires_at@$4 WHERE $5/$6/$7),Get-readback 双字段。变异验证: 把 expires_at 子句硬编成 $1
  → 组合用例红、单字段用例仍绿。
- **S2 — clear/never-expiring 响应省略 expires_at 未测**: 加 TestKeyPatchExpiresAtClearResponseOmits(nil ExpiresAt
  → 断言 body 不含 expires_at;mutation 删 ,omitempty/换值类型 → 红)。
- **S2 — plan 缺 #12 首引 recency block**: 补 sub2api/new-api/CLIProxyAPI 的 archived/disabled/pushed_at + HEAD 时间戳
  + commit message(核验于 2026-06-18 UTC,三者皆 live)。
- **S3 — JSON null vs absent 未测**: 加 TestKeyPatchExpiresAtNullIsUnchanged(`{"expires_at":null}` → ClearExpiry false
  + ExpiresAt nil,钉死 null==absent 契约)。
- **S3 — OpenAPI 请求 schema type:string 但描述称接受 JSON null**: 删描述里 "(or send JSON null)" 短语(最小改面;
  handler 仍宽容接受 null=unchanged,但文档/校验契约只声明 omit=unchanged)。
- **S3 — plan 散文逐字复现参考标识符(#11(c))**: 把 `token.expired_time`(new-api)与 `expires_in_days`(sub2api)改成
  paraphrase 描述,保留 file:line 引用作证据锚。

## 延后 (pre-existing, out-of-scope)
- **FU-1 — Patch 内死的 whereArgs 自赋值循环 (userkey.go ~735-739)**: `for i, a := range whereArgs { _ = a; whereArgs[i] = a }`
  是纯 no-op 死代码(whereArgs 后续不读;真实 WHERE args 来自 allArgs)。审查 verifier 已证: **pre-existing**(git show HEAD
  即有,非本切片引入)、零安全/正确性影响(WHERE 绑定独立且正确)、纯可维护性 nit。按 scope 纪律不并入本切片 diff(避免
  扩散无关改动)。**跟进**: 独立 cleanup 切片删除该死代码块。
