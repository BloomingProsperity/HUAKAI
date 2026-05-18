---
plan_id: 2026-05-18-r-3-a-fix-3-deeper-claude
lane: claude (PM)
status: dispatched
prereq: R-3-A-fix-2-deeper (commit a7ccdc9)
utc: 2026-05-18T11:05:00Z
---

# R-3-A-fix-3-deeper — 3 vendor JA3 mismatch 根因

## 0 缘起

R-3-A-fix-2-deeper (a7ccdc9) 加了 kExtensions[] ext 22 + strict_mode 跳 65281,
但 4 vendor wire test 结果:
- anthropic: byte-level PASS ✓
- codex-cli: JA3 mismatch (left `687fb78f6ca0b877e5d3edbfdefc7ddf` vs right `0e0088de64e0c3adf8e9d8c19c811eb3`) ✗
- kiro-cli: JA3 mismatch (left `3309ead7bbf4c356272a951be9fdc21a` vs right `ed5338278fb7f0fb5cfd4ad58a98241f`) ✗
- gemini-advanced: JA3 mismatch (left `fdf6db6f657ddef2a21d7434aa547536` vs right `55ba290366f110228d176d92fe6f6180`) ✗

3 vendor 暂 #[ignore]. 本 wave 调根因.

## 1 假设 (按优先序)

### H1: strict_mode flag 未为这 3 vendor 设上

build_boring_connector / configure_boring_connection 调 set_extension_order 时,
profile 没显式 list extension 顺序 → SSL_CTX_set_extension_order 未调 → strict_mode false → 默认追加 internal extensions.

验: grep build_boring_connector 看哪里调 set_extension_order, profile 加载时 ext 列表是否非空.

### H2: ext 22 / 23 / 65037 等 HUAKAI 新加 ext 写入序错位

R-3-A-fix-2-deeper 实现 ext 22 add_clienthello 只在 has_explicit_order_strict_mode + TLS1.2 capable 时写,
profile 含 ext 22 但 boring 内部位置在 kExtensions[] 后段, 严格枚举时位置不对.

验: 抓 ClientHello 字节, 看 ext 顺序跟 profile.tls.extensions[] 是否完全一致 (含 ext 22 位置).

### H3: 某些 ext 在 boring kExtensions[] 没 entry 仍被滤掉

profile.tls.extensions 含某个 ext 不在 boring kExtensions[] (ext 22 已加, 但可能还有其它).
strict_mode 严格枚举 ext_type 时, find_extension_by_type 返 null → 报错 OR 跳过.

验: codex_cli profile.tls.extensions list 对比 boring kExtensions[] 全表, 看缺哪些.

### H4: ext 65037 (ECH) / ext 5 (status_request OCSP) / ext 18 (SCT) HUAKAI 注入路径出错

build_boring_connector 路径已注入 ECH/OCSP/SCT (per R-2-B-2-extend), 但注入位置可能不在 strict_mode 列表中, 实际 ClientHello 写出时被 strict_mode 滤掉.

验: 配合 H2 抓字节看 ext 65037 实际位置.

## 2 调查步骤

1. 收集 3 vendor profile.tls.extensions list (从 profiles_anthropic.yaml / codex_cli.yaml 等)
2. 收集 boring kExtensions[] 全表 (vendor/boring/.../extensions.cc:3500-3700)
3. 跑 cargo test 不 #[ignore], 抓 actual ClientHello bytes (test 已用 capture fixture)
4. parse ext 顺序对比 profile + observed
5. 按 H1-H4 顺序排查
6. 找到根因后:
   - 若 H1: 加 set_extension_order 调用补漏
   - 若 H2: 调整 ext_etm_add_clienthello 写入位置 OR kExtensions[] 排序
   - 若 H3: 找出缺 ext + 加 entry (跟 ext 22 套路)
   - 若 H4: 注入路径改在 strict_mode 写入逻辑之内

## 3 范围与硬约束

- 不动 R-2-B/R-3-A 已落 mimicry/proxy_engine 代码主体
- 允许动 vendor/boring/ + ssl_lib.cc / extensions.cc 继续 patch
- patch 总 ≤ 250 行累计 (R-3-A-fix-2-deeper 已用 ~100, 还剩 ~150)
- 不动 frontend / Go / control plane
- 注释中文 (HUAKAI patch 内, RFC reference)

## 4 跟 F-AUDIT-1-B parallel 关系

F-AUDIT-1-B 是 Go gateway, 本 wave 是 Rust C debug. 不同语言/不同目录, 完全不冲突.
