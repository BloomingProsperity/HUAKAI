# 2026-05-15 L2-A6 HTTP/2 fork adapter

| Owner directive | "L2-A6: HTTP/2 fork adapter (settings/pseudo-header order, feature-gated)" |
| Scope | In: merged Rust core gateway crate, optional pinned `http2` git dependency, `mimicry-http2-fork` feature, feature-gated H2 adapter, minimal H2 profile schema/template fields, and feature-gated encoder/capture-level tests. Out: ProxyEngine wiring, TLS server integration unless fork requires it for encoding, non-approved reference projects, auth, billing, quota, database schema, deployment, and `LICENSE`. |
| Success criteria | Default build/test do not compile the fork; feature build/test prove SETTINGS wire identifier order and request pseudo-header order follow the profile; `mimicry-openssl,mimicry-http2-fork` builds together; dependency is pinned to `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892`; `git diff --check` is clean. |
| Time estimate | 90-150 minutes wall clock, mainly API discovery, Cargo lock resolution, and Rust test runtime. |
| Blast radius | Limited to exploratory Rust core gateway mimicry code, fingerprint template schema/data, tests, and Cargo metadata/lockfile. Default runtime behavior remains unchanged because the dependency and module are feature-gated. |
| Failure modes | The fork may expose order controls only through connected client handshakes rather than standalone encoders; mitigate by capturing bytes over an in-memory duplex H2 client path and documenting if pseudo-header decoding needs a follow-up. Profile data may not yet include H2 order fields; mitigate by adding explicit optional/validated schema fields with conservative template values. Cargo may resolve extra transitive crates; inspect licenses against prior L2-A0 evidence and keep feature optional. |
| Decision points | Stop for Owner only if implementation needs high-risk files, runtime dependency beyond the pinned fork, ProxyEngine dispatch, forbidden reference projects, or schema/auth/billing/quota changes. None is expected. |
| Pre-execution checklist | Confirm crate name/license from `~/refs/http2/Cargo.toml`; read only HUAKAI internal files plus allowed local `~/refs/http2`, `~/refs/wreq`, and `~/refs/h2` API regions; cite observed API lines; avoid copying raw wreq/http2 code; inspect current profile field names and tests; verify worktree dirt before edits. |
| Concrete execution order | 1. Inspect allowed fork API for builder settings order and pseudo-header order controls. 2. Extend HUAKAI H2 profile types/templates with explicit order/value fields if absent. 3. Add pinned optional dependency and feature. 4. Implement feature-gated adapter around the fork's public builder controls plus an in-memory byte capture helper if direct encode-only is unavailable. 5. Add feature-gated tests for SETTINGS frame order and pseudo-header order. 6. Run fmt, requested build/test commands, and `git diff --check`. |
| Cross-discussion note | This is the Codex independent plan artifact for L2-A6. No same-descriptor Claude draft was read in this session; prior architecture/license plans were used only as HUAKAI-internal context. |
| UTC timestamp | 2026-05-15T05:05:00Z |

## Clean-Room And Dependency Notes

- Allowed reference reads are restricted to `/home/codex/refs/http2`, `/home/codex/refs/wreq`, and `/home/codex/refs/h2`.
- No Sub2API, New-API, Portkey, Helicone, LiteLLM, or All-API-Hub source will be read.
- The `http2` fork is MIT and remains optional behind `mimicry-http2-fork`; `wreq` and upstream `h2` are read only for API comparison/evidence, not copied.

Owner 中文摘要：这是 Codex 对 L2-A6 的独立执行计划。范围只覆盖 feature-gated HTTP/2 fork adapter、必要 profile 字段、测试和 Cargo 元数据；不接 `ProxyEngine`，不触碰高风险文件，不读禁止参考项目。关键风险是 fork 是否支持纯 encoder 测试；若不支持，将用内存连接捕获初始 SETTINGS/HEADERS bytes，并把限制写入结果。
