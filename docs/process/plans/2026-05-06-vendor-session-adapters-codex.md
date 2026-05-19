# 2026-05-06 vendor session adapters codex

| Owner directive | "写 6 个 vendor session 反转 adapter Go 文件。Lane = specifier（公开反转项目 README 读行为摘要 → 改写为 HUAKAI 风格 adapter；不复制源码）。" |
| Scope | In: write six standalone Go adapter drafts to `C:\tmp\parallel-vendors-codex\`; read HUAKAI provider interface and Codex session template only for local interface shape. Out: no repo integration, no runtime dependency, no vendor source-code reading, no tests added. |
| Success criteria | Six files exist with requested package names, endpoint constants, user-agent constants, adapter structs, provider interface assertion, credential validation, POST request construction, header injection, private helper, and lane stamp. Each file stays around 80-120 LoC and is gofmt-formatted. |
| Time estimate | Wall clock: 20-35 minutes. Agent time: one implementation pass plus formatting and line-count verification. |
| Blast radius | Low: files are written outside the repo implementation tree. The only repo mutation is this plan artifact. |
| Failure modes | Accidental template phrase reuse: mitigate by rewriting comments/errors and checking for known forbidden wording. Invalid Go syntax: mitigate with `gofmt`. Over/under line count: mitigate with `Measure-Object -Line`. Clean-room ambiguity: rely on Owner-provided header hints and local HUAKAI interface only; mark unresolved vendor-specific captures as TODO/OCAW. |
| Decision points | None for this safe draft output. Antigravity endpoint and headers remain TODO because Owner explicitly marked them unknown. |
| Pre-execution checklist | 1. Read local adapter interface. 2. Read local Codex session template for shape only. 3. Create output directory. 4. Write six independently worded files. 5. Run gofmt. 6. Verify line counts and list output directory. |

Concrete execution order:

1. Create `C:\tmp\parallel-vendors-codex`.
2. Generate cursor, copilot, gemini advanced, antigravity, kiro, and windsurf adapter drafts with package-specific naming.
3. Use `provider.CredentialTypeSessionToken` and `provider.CredentialTypeUpstreamPassthrough`; reject `apikey`.
4. Keep endpoint placeholders explicit where OCAW capture is pending.
5. Format and inspect generated files before reporting completion.
