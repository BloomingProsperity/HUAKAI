# 2026-05-18 rust-vendor R-3-A-fix-5 deeper

| Owner directive | "修 rust-vendor 3 项 (audit list HIGH x 3 + MEDIUM x 1): profile setter 非原子 / cipher 不去重 / 双 GREASE / EC point format 不限." |
| Scope | In: `exploratory/rust-core-gateway/vendor/boring/` Boring/BoringSSL patch and attribution note. Out: frontend, Go, `mimicry/proxy_engine` mainline, non-boring dependencies, forbidden reference source. |
| Success criteria | Setter stages all fields before commit; cipher order is stable-deduped; strict explicit supported groups do not receive a second GREASE value; EC point format rejects values outside 0, 1, 2; `MODIFICATIONS.md` records R-3-A-fix-5 attribution; requested cargo checks are run or blocker recorded. |
| Time estimate | 45-90 minutes wall clock; one Codex implementer pass plus build/test time. |
| Blast radius | Vendor TLS ClientHello profile serialization and mimicry-boring build. Failure could alter wire fingerprints or break boring-sys compilation. |
| Failure modes | Partial setter commit on allocation failure; overly broad GREASE suppression changing default behavior; invalid error symbol; patch line budget overrun; cargo build cache or dependency blocker. Mitigation: inspect local BoringSSL shapes first, keep changes narrow, preserve defaults unless strict explicit profile is active, run requested checks. |
| Decision points | Stop for Owner only if the fix requires high-risk files, new runtime dependencies, database/auth/billing/quota changes, or reading prohibited reference source. |
| Pre-execution checklist | Locate actual vendor root; read local setter, extension writer, and header declaration; inspect existing attribution format; patch only necessary vendor files; count patch lines; run requested cargo check and test with target-dir fallback if needed; report Chinese summary. |
| Concrete execution order | 1. Inspect profile setter/header/supported_groups paths. 2. Patch atomic staging, dedup, EC validation, and GREASE suppression. 3. Add R-3-A-fix-5 attribution. 4. Run cargo check/test from `exploratory/rust-core-gateway/merged`. 5. Summarize files, completion, test result, patch-line count, risks. |
