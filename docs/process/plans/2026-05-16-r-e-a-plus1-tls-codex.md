# 2026-05-16 R-E-A+1 TLS Codex Plan

| Owner directive | "任务 = R-3 R-E Phase R-E-A+1 continue (PM 已批 OCAW)." |
| Scope | In: `exploratory/rust-core-gateway/merged/Cargo.lock`, Rust gateway NOTICE attribution, and this TLS-only plan update. Out: Chrome 137 profile upgrade, `rquest`/`wreq` integration, reference project source reads, backend/auth/billing/quota/db/deploy, device fingerprinting, L4/L5/L6 work, and prior R-E-A files owned by other lanes. |
| Success criteria | Cargo.lock records the PM-approved 8 transitive dependencies for the `core_gateway` `tls` feature; `exploratory/rust-core-gateway/NOTICE.md` records dependency, license, and source repo attribution; the requested TLS feature cargo test command passes using `CARGO_TARGET_DIR=/home/codex/HUAKAI/.cargo-targets/r-e-a-plus1`; Chrome 137 is explicitly deferred to R-E+1 with rquest integration. |
| Time estimate | 45-75 minutes local agent time for lock refresh, NOTICE/plan updates, and full TLS feature test rerun. |
| Blast radius | Rust core gateway lockfile and attribution docs only. Failure could leave the lockfile out of sync with the already-added `tls` feature or leave attribution incomplete. |
| Failure modes | Crates.io network access is blocked: use local Cargo cache/offline resolution and record the failed online attempt. Offline cache missing approved packages: stop and report blocker. Extra deps beyond the approved 8 appear: stop for Owner/PM confirmation. Test compile failure: report exact failing command and do not claim release readiness. |
| Decision points | PM OCAW already approved accepting the 8 transitive dependencies and keeping the Cargo.lock update. Stop before any extra dependency, `rquest`/`wreq`, Chrome 137 data-plane change, `LICENSE`, backend/auth/billing/quota/db/deploy, or reference source read. |
| Pre-execution checklist | 1. Confirm worktree and avoid unrelated changes. 2. Re-resolve Cargo.lock with workspace-local target dir. 3. Verify the lockfile delta is exactly the approved 8 packages plus expected dependency edges. 4. Add NOTICE attribution. 5. Run the requested TLS feature test with `/dev/null` stdin. 6. Report git status, diff stat, test result, and risks. |

## PM OCAW Decision Record

- Accept the 8 transitive dependencies introduced by `tonic` TLS roots support:
  `core-foundation`, `core-foundation-sys`, `openssl-probe`,
  `rustls-native-certs`, `rustls-pemfile`, `schannel`,
  `security-framework`, and `security-framework-sys`.
- Use workspace-local build output:
  `CARGO_TARGET_DIR=/home/codex/HUAKAI/.cargo-targets/r-e-a-plus1`.
- Defer Chrome 137 profile upgrade to R-E+1 because this Rust data-plane lane
  has no real Chrome 137 profile data and is not integrating `rquest`.
- Record attribution in `exploratory/rust-core-gateway/NOTICE.md`.

Concrete execution order:

1. Re-run Cargo resolution for `core_gateway` with `tls` enabled and the approved target dir.
2. Confirm Cargo.lock includes only the approved TLS transitive dependency additions.
3. Add/update NOTICE attribution for the approved 8 packages.
4. Leave Chrome 137, `rquest`, and `wreq` untouched for R-E+1.
5. Run the requested `cargo test` command and capture PASS/FAIL summary.
6. Report `git status`, `git diff --stat`, dependency risk, and test result.
