# L2-A5.4 Retrospective Cross-Review (Codex)

| Slice | L2-A5.4 - OpenSSL extension 22 (encrypt_then_mac) preflight + dispatch |
| Commit | 5b7483e (origin/claude/phase-1) |
| Review run | 2026-05-15 (retrospective 补跑；初次 commit 时未跑) |
| Reviewer | Codex GPT-5 (lane=REVIEWER, sandbox=read-only) |
| Verdict | APPROVE_WITH_CHANGES |

## Findings

### MEDIUM: L2-A5.4 只证明 extension 22 存在，不证明完整 extension list/order；L2-A5.5 的 ordered-subset + extras 设计应继续推进。

Evidence: `docs/plans/2026-05-15-l2-a5-4-extension-22-codex.md:8`; `5b7483e:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:366`; `5b7483e:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:238`; `/home/codex/l2a54-rescue-backup/huakai-l2-a5-4-target-profile-driven-extension-22.log:59`; `docs/plans/2026-05-15-l2-a5-5-extension-list-codex.md:5`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:389`

Impact: 5b7483e 对 "ext 22 safe equivalent" 可接受，但不能把它当成完整 extension-list parity gate；实际日志里 wire 多出 `27`，L2-A5.4 仍允许通过。

Suggested follow-up: 继续 L2-A5.5，把 full extension ordered-subset / missing / wrong-order / extras provenance 落地后，再关闭最后一个 extension field family。

## Positive Checks

- 无 HIGH finding。
- `apply_extensions` 对缺 22 fail-fast 为 `UnsupportedExtension`，含 22 后进入 runtime preflight：`5b7483e:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:84`, `:334`, `:366`
- Codex template 确实声明 `native-tls/openssl`、`stable`，且 `extensions` 含 22：`tools/fingerprint-collector/templates/codex-cli.json:14`, `:16`, `:60`
- dispatch 对缺 22 的 native OpenSSL profile 返回 `BlockUnsupportedTemplate`：`5b7483e:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:27`
- dispatch 测试已改为独立 test-only `FingerprintProfile`，不是从 KiroCli mutate：`5b7483e:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:14`, `:37`, `:253`
- targeted ext22 3/3 通过，mimicry-openssl combo 中 dispatch test 也通过：`/home/codex/l2a54-rescue-backup/huakai-l2-a5-4-target-profile-driven-extension-22.log:58`, `/home/codex/l2a54-rescue-backup/huakai-l2-a5-4-mimicry-openssl.log:142`
- RUNBOOK §10 已加入 deploy 前 preflight 与 deploy 后 surfacing：`5b7483e:exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:483`, `:492`
- `git diff --check 5b7483e^..5b7483e` 无输出；reviewer 未改文件，未跑 commit。
- clean-room 边界未发现违规；本次 review 未读禁止参考项目源码。

## Source files read (per CLAUDE.md #11)

Retrospective 说明：以下列表来自原始 review log `/tmp/codex_l2a54_retro_review.log`；本归档 lane 未重新读取 reference repos，也未逐字搬运任何 reference repo 内容。

Source files read: `docs/templates/codex-reviewer.md`; `docs/plans/2026-05-15-l2-a5-4-extension-22-codex.md`; `docs/plans/2026-05-15-l2-a5-5-extension-list-codex.md`; `tools/fingerprint-collector/templates/codex-cli.json`; `5b7483e` commit diff/stat/message; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs`; `5b7483e:.../dispatch.rs`; `5b7483e:.../openssl_adapter.rs`; `5b7483e:.../tls_profile.rs`; `5b7483e:.../tls_capture.rs`; `5b7483e:.../tests/mimicry_dispatch_test.rs`; `5b7483e:.../tests/mimicry_openssl_adapter_test.rs`; `5b7483e:.../tests/mimicry_profile_test.rs`; `5b7483e:.../tests/mimicry_capture_diff_test.rs`; `5b7483e:.../tests/common/capture_diff.rs`; `5b7483e:.../tools/recapture/RUNBOOK.md`; `/home/codex/l2a54-rescue-backup/l2a54-staged.diff`; `/home/codex/l2a54-rescue-backup/codex-l2a54-*.txt`; `/home/codex/l2a54-rescue-backup/huakai-l2-a5-4-*.log`.

Lane: REVIEWER
Agent: Codex GPT-5
UTC timestamp: 2026-05-15T09:39:58Z

## Owner Summary (中文)

5b7483e 可以作为 L2-A5.4 的 ext 22 safe-equivalent commit 保留，没有发现 HIGH 阻塞项；最高优先级 follow-up 是不要把 L2-A5.4 误认为完整 extension-list parity，因为它只检查 22 是否存在，实际 wire 额外 extension 仍会 pass。L2-A5.5 的 ordered-subset + extras 方向更正确，可以继续推，但应作为 MED follow-up 完成后再宣布最后一个 extension field family closed。

## Retrospective Notes

- 本次 review 在 commit 已 push 后**补跑**，违反 CLAUDE.md #7 "BEFORE next slice"，作为流程瑕疵记录。
- 修补措施：已装 `.git/hooks/pre-push`，从 L2-A5.5 起每个 slice push 前必须有 `docs/reviews/` 下匹配 slice id 的 review artifact。
- 本次 review 结论可作为 L2-A5.4 commit 的事后合规证明。
