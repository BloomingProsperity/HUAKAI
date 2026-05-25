# L2-A5.5 Per-Commit Cross-Review (Codex)

| Slice | R-C Lane 2 L2-A5.5 - OpenSSL extension list ordered-subset preflight |
| Review target | Uncommitted working tree before commit/push |
| Review run | 2026-05-15 |
| Reviewer | Codex GPT-5 (lane=REVIEWER, workspace-write only this review file) |
| Verdict | APPROVE_WITH_CHANGES |

## Findings

### MEDIUM: `wire_extension_extras` 目前只是 adapter getter / test plumbing，RUNBOOK 的 "ops 暴露" 说法比实现更强。

Evidence: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:42`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:136`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:351`; `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:487`; `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:511`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:19`

Impact: `PreflightExtras::wire_extension_extras` 已可从 adapter 读取，测试也验证了 extras，但当前没有看到 dispatch / ops event / log / metrics 层消费它。RUNBOOK 写成 "暴露给 ops" 容易让 Owner 以为已经进入上层可观察通道。

Suggested follow-up: 本 commit 若只承诺 "available for ops plumbing"，RUNBOOK 应改成这个措辞；若要兑现 "ops observable"，需要后续 slice 把 extras 接到实际 ops surface。

### MEDIUM: `extensions_missing_independent_vector` 的 missing ID 仍是硬编码 `0xfafa`，不是完全 runtime-derived。

Evidence: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:413`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:414`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:427`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:791`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:799`

Impact: subset / wrong-order tests do use runtime preflight and profile-derived vectors, but missing test直接 push `0xfafa`。这大概率稳定触发缺失，但严格说不满足 "not hardcode" 的测试构造要求；如果未来 runtime/GREASE 行为碰巧出现该 ID，测试意图会变弱。

Suggested follow-up: 让 missing test 复用一个返回 `(runtime_extensions, synthetic_missing_id)` 的 probe helper，或先从 runtime actual 中排除后再选择合成 ID。

### LOW: 提供的 cargo combo log 只保留了每个 combo 的 `14 passed` 摘要，未包含背景里声明的 `175/191/178/194` 全量计数。

Evidence: `/tmp/l2a55_4combo.log:10`; `/tmp/l2a55_4combo.log:22`; `/tmp/l2a55_4combo.log:34`; `/tmp/l2a55_4combo.log:46`

Impact: 我能验证该 log 中 4 个 combo 都是 `0 failed`，但无法从这个文件本身复核 Owner 背景里给出的全量 test count。代码结论不受影响，只是 review evidence 不完整。

Suggested follow-up: commit/push 前保留完整 cargo 输出或汇总文件，避免后续 reviewer 只能看到截断片段。

### LOW: SampleSetRandomized 测试名/断言文案仍说 "set"，但 extensions 现在是 subset + extras 语义。

Evidence: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_capture_diff_test.rs:36`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_capture_diff_test.rs:57`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:177`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:181`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:314`

Impact: 实现按 L2-A5.5 ordered-subset 方向运行，`unexpected` extras 不再使 `diff_has_mismatch` 为 true；但测试标题和 message 仍暗示 exact set semantics，后续读者容易误判这是保留了旧 `SetMatch/SetMismatch` 语义。

Suggested follow-up: 改名/改文案为 "subset + missing" 或补一个 SampleSetRandomized actual-extra case，明确 extras 是否应为 non-mismatch。

## Positive Checks

- 无 HIGH finding。
- `ExtensionsListStatus` 覆盖 `Subset` / `Missing` / `WrongOrder` 三态：enum 在 `tests/common/capture_diff.rs:38`，ordered path 先判 missing，再判 ordered subset，最后落 wrong-order：`tests/common/capture_diff.rs:196`。在 extension ID 不重复的 profile/capture 前提下三态互斥，未见漏 case。
- `diff_capture_against_profile` 已把 extensions 从通用 `ListFieldStatus` 迁移到 `ExtensionsListStatus`：`tests/common/capture_diff.rs:74`。`SampleSetRandomized` 走 missing-only subset check，`ExactStable` 和 `KnownGapBlocked` 走 ordered-subset check：`tests/common/capture_diff.rs:164`。
- `diff_has_mismatch` 对 extensions 只把 `Missing` / `WrongOrder` 算 mismatch，`Subset { unexpected }` 不算失败：`tests/common/capture_diff.rs:98`; `tests/common/capture_diff.rs:314`。这符合本 slice 的 "profile subset of wire, extras recorded" 方向。
- ext 22 apply-time guard 没被新 preflight 蒙住：`new_with_profile` 仍先调用 `apply_extensions`，再跑 runtime preflight：`openssl_adapter.rs:103`; `openssl_adapter.rs:110`。缺 22 profile 仍返回 `UnsupportedExtension`：`openssl_adapter.rs:352`; `tests/mimicry_openssl_adapter_test.rs:273`。
- runtime 不再发送 profile 声明的 extension 时，新 preflight 会进入 `PreflightFailed { field: "extensions", missing, unexpected, .. }`：`openssl_adapter.rs:391`; `openssl_adapter.rs:402`。因此如果 runtime 漏发 22，而 profile 仍含 22，会被 missing 路径 fail-closed。
- `extensions_subset_independent_vector` 通过 failing probe 提取 runtime actual，再计算 expected extras：`tests/mimicry_openssl_adapter_test.rs:347`; `tests/mimicry_openssl_adapter_test.rs:355`; `tests/mimicry_openssl_adapter_test.rs:390`。
- `extensions_wrong_order_independent_vector` 从 baseline profile 中寻找 22 后一个 extension 再反转相对顺序，不是直接复制完整 hardcoded list：`tests/mimicry_openssl_adapter_test.rs:449`; `tests/mimicry_openssl_adapter_test.rs:457`。
- RUNBOOK §10 准确描述 ordered-subset、extras、missing、wrong-order 和 operator action 列表；唯一需要收紧的是 "ops 暴露" 的实现状态措辞：`tools/recapture/RUNBOOK.md:483`; `tools/recapture/RUNBOOK.md:490`; `tools/recapture/RUNBOOK.md:506`。
- `git diff --cached --check` 无输出，cached whitespace check clean。当前 staged 文件只有 `docs/process/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md`，本 review 未执行 `git add`。
- clean-room 边界未发现违规；本次 review 未读取 forbidden reference repos，修改文件中也未发现 AGPL/GPL/LGPL 或 forbidden repo source 标记。

## Verdict Rationale

本次改动的核心 preflight / diff 语义可以进 commit：没有发现会让 profile 缺失、runtime 漏发或 wrong-order 被误放行的 HIGH 缺陷。`APPROVE_WITH_CHANGES` 的原因是两个 MED follow-up：ops 可观察性还只是 getter plumbing，missing test 仍有硬编码合成 ID。Owner 若接受这两个作为紧随 follow-up，本 commit 不阻塞；若要求 "ops observable" 和 "no hardcode" 在本 commit 内完全闭合，应先补小改再 push。

## Source Files Read

- `docs/templates/codex-reviewer.md`
- `docs/process/plans/2026-05-15-l2-a5-5-extension-list-codex.md`
- `docs/process/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_capture_diff_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs`
- `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`
- `/tmp/l2a55_4combo.log`

Lane: REVIEWER

Agent: Codex GPT-5

UTC timestamp: 2026-05-15T09:57:03Z

## Owner Summary (中文)

总体结论是没有 HIGH 阻塞，extension ordered-subset / missing / wrong-order 和 ext 22 fail-fast 协同都成立。最高优先级补改是把 `wire_extension_extras` 的 ops 可观察性说清楚：现在只是 adapter getter，不是已经进入上层 ops channel。第二个补改是让 missing 测试不要直接硬编码 `0xfafa`，而是从 runtime probe 派生合成缺失 ID。clean-room 方面本次只读 HUAKAI 内部文件和 cargo log，未读 forbidden reference repos，未发现复制风险。是否 push 取决于 Owner 是否接受两个 MED 作为紧随 follow-up；若要本 commit 完全闭合，需要先做小修。
