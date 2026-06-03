# 2026-06-03 gitleaks allowlist

| Field | Plan |
| --- | --- |
| Owner directive | "Add a repo-root .gitleaks.toml allowlist to govern the ~95 gitleaks findings ... so a scan is clean WITHOUT hiding any real secret." |
| Scope | In: inspect likely gitleaks noise sources, add repo-root `.gitleaks.toml`, verify with `gitleaks` if available or deterministic grep fallback. Out: commits, dependency installs, suppressing plausible real credentials. |
| Success criteria | `.gitleaks.toml` extends default gitleaks rules and only allowlists vendor/test/docs/example placeholder evidence; any credible secret remains unsuppressed and is reported. |
| Time estimate | 30-60 minutes wall time for scan inference, config, and verification. |
| Blast radius | A broad allowlist could hide a real leaked secret; a narrow allowlist may leave known fixture noise for PM to tune after a scanner run. |
| Failure modes | Overbroad path regex hides non-test secrets; placeholder regex matches real-looking token; `gitleaks` unavailable prevents exact before/after counts. Mitigation: restrict paths to vendor/tests/docs/markdown examples and keep value regexes tied to obvious dummy words. |
| Decision points | Owner/PM must remediate any finding that appears to be a real credential. No high-risk file such as `LICENSE`, real secrets, auth core, billing ledger, quota enforcement, DB schema, or deployment scripts will be changed. |
| Pre-execution checklist | Confirm `gitleaks` availability; inventory existing gitleaks config; grep likely placeholder secret patterns; inspect representative files before allowlisting; write comments for each allowlist condition; rerun available verification; report unresolved risks. |

## Concrete Execution Order

1. Check whether `gitleaks` is installed and whether a prior config exists.
2. If available, run `gitleaks detect --source . --no-banner -f json -r /tmp/gl.json` and group findings by path prefix and rule.
3. If unavailable, use `rg` to inspect likely fixture/example tokens in `vendor/`, tests, `docs/`, markdown, and example paths.
4. Create a conservative `.gitleaks.toml` that extends default rules and uses comments to document each safe-noise allowlist entry.
5. Verify the config syntax and rerun gitleaks if possible; otherwise verify the targeted noise paths/patterns with `rg`.
6. Report category counts, allowlist entries, scanner availability, and any credible secret candidates left unsuppressed.
