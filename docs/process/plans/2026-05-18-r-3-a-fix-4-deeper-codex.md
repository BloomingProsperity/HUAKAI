---
plan_id: 2026-05-18-r-3-a-fix-4-deeper-codex
lane: codex implementer
status: executing under Owner continuous authorization
utc: 2026-05-18T00:00:00Z
---

# 2026-05-18 R-3-A-fix-4-deeper

| Owner directive | "R-3-A-fix-4-deeper: Codex + Gemini 2 vendor JA3 mismatch 根因 + 修" |
| Scope | 仅诊断并修复 Rust core_gateway mimicry-boring wire test 中 codex-cli 与 gemini-advanced 的 JA3 mismatch。允许读/改 HUAKAI Rust mimicry 测试、profile、vendor/boring 与 BoringSSL C/C++ source。禁止读取 rquest / curl_cffi / wreq / utls / chrome-impersonate source。 |
| Out of scope | 不动 frontend / Go / control plane / R-2-B/R-3-A 已落 mimicry/proxy_engine 主体；不引入非 boring 系依赖；不做生产部署。 |
| Success criteria | 产出 observed vs expected 的 JA3 五段诊断表；补丁新增不超过 100 行；更新 vendor/boring/MODIFICATIONS.md attribution；`cargo check -p core_gateway --features mimicry-boring` 通过；`cargo test -p core_gateway --features mimicry-boring --lib` 尽量通过，若仍 FAIL 列出具体 diff。 |
| Time estimate | 1.5-2 天任务中的本次 executor slice，预计 9-13 小时；当前回合先完成可运行诊断、小补丁和验证。 |
| Blast radius | BoringSSL fork 的 ClientHello 构造路径、Rust mimicry profile-to-BoringSSL 配置、wire fixture tests。 |
| Failure modes | 误读非授权参考源码：只访问用户列出的 HUAKAI/boring 文件；补丁过大：每次修改前后用 diff 统计；测试环境写入失败：按 TMPDIR/LIBCLANG_PATH/CARGO_TARGET_DIR fallback；仍 JA3 mismatch：保留 ignore 状态并报告下一轮精确字段。 |
| Decision points | 涉及数据库、auth、billing、quota、runtime dependency、LICENSE、生产 secrets、删除文件时必须停下；本任务预计不触发。 |
| Pre-execution checklist | 1. 读 fix-3 Claude plan；2. 读 Rust builder/wire tests/profile；3. 读授权 BoringSSL extensions/ssl config 区域；4. 跑不忽略 wire tests 捕获 observed；5. 解析 JA3 五段；6. 按实际 mismatch 最小修复；7. 更新 attribution；8. 验证。 |

## Concrete Execution Order

1. 建立当前 worktree 状态基线，避免覆盖用户改动。
2. 用测试输出和必要的临时 test edit 取得 codex-cli / gemini-advanced observed ClientHello 字节与 JA3 五段。
3. 对比 profile.tls 的 ciphers / extensions / groups / ec point formats / supported versions。
4. 只在诊断证明需要时修改 vendor/boring 或薄 Rust 配置层。
5. 恢复临时 ignore 状态，只保留真实修复、文档 attribution 和必要测试改动。
6. 运行 cargo check 与 cargo test，记录四个 vendor wire 状态。
