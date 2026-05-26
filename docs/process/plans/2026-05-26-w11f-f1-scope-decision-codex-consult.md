# W11-F F-1 epic scope decision — codex consult + synthesis (2026-05-26)

Owner directive: "和 codex 讨论下给我选择，记得带上借鉴项目是如何做的"
(consult codex on the choice; include reference-project evidence).
Owner approval (final): "是的" — accept option A + Codex's 5 dormancy gates.

Per CLAUDE.md #10 parallel discuss. Claude's parallel view recorded in
the main chat thread; Codex's independent reply preserved below verbatim.
Synthesis at the bottom; binding policy outcomes recorded in
[W11-F-F1-status.md §11](../release-readiness/W11-F-F1-status.md) and
[AGENTS.md §"Dormant h2 outbound infrastructure gate"](../../../AGENTS.md).

---

## Clean-room lane guard (per AGENTS.md §"Per-Vendor Fingerprint Capture Discipline" + CLAUDE.md #11)

- **Lane**: review consultant (NOT specifier, NOT implementer). Output is a
  scope decision recommendation in prose; no code, no specs, no test fixtures
  derived from cited sources.
- **Risk layer**: L0 — behavior-parity discussion across reference projects.
  No algorithm/structure mimicry. No code generated. No verbatim source
  comments reused (the cliproxyapi source comment was paraphrased in this
  artifact + the §10.4 status doc per the round-1 fix).
- **Sources read for evidence** (full SHA + license + fetch context, per
  CLAUDE.md #12 first-cite recency check). All clones under `~/refs/`,
  fetched 2026-05-26:
  - `anthropics/anthropic-sdk-typescript@32ce8c0` (MIT, pushed 2026-05-23,
    2d fresh). Files cited: `src/internal/shims.ts:13-21`, `src/client.ts:528-530`,
    `src/client.ts:1301-1302`.
  - `anthropics/anthropic-sdk-python@5db69c6` (MIT, pushed 2026-05-23, 2d
    fresh). Files cited: `src/anthropic/_base_client.py:39-94`. Grep
    coverage: `src/` for `http2=|HTTP2|allow_h2` returned zero matches.
  - `router-for-me/CLIProxyAPI@21fad9d` (MIT, pushed 2026-05-25, 0d fresh).
    Files cited: `internal/runtime/executor/helps/utls_client.go:81-103,131-150`,
    `internal/runtime/executor/antigravity_executor.go:195-225`.
  - `Xerxes-2/clewdr@5762680` (**AGPL-3.0**, pushed 2026-05-09, 16d fresh).
    Files cited: `src/utils/mod.rs:61`. **L0 behavior-only read; no
    vendoring, no copying of code or comments, paraphrase summary only.**
  - `0x676e67/rquest@e8781fb` (MIT/Apache-2.0 dual, committed 2026-05-22,
    4d fresh). Files cited: `src/tls.rs:551`.
- **Source-must-read compliance** (CLAUDE.md #12): each behavior claim in
  the EVIDENCE section below traces to specific file:line in one of the
  five sources above. Full per-claim citation table maintained in the
  finding doc `docs/process/release-readiness/W11-F-F1g-h2-stack-divergence-finding.md`
  Part C (which has both `<owner>/<repo>@<sha>:<file>:<line>` form AND
  clickable GitHub blob URLs). The prompt summary below uses short-form
  references (repo+SHA + general behavior) for brevity; the finding doc
  is the canonical evidence chain.
- **No copying**: zero verbatim source code in this artifact. Zero verbatim
  multi-line comments. The single cliproxyapi source comment that was
  initially quoted in the round-1 finding doc was paraphrased in round-2
  per codex per-commit review.
- **Output use**: synthesizing W11-F F-1 dormant-infrastructure policy
  decision only (recorded at `W11-F-F1-status.md` §11 "Dormancy gates").
  No source-derived code shipped.

## Prompt sent to codex (via stdin, `codex exec --sandbox workspace-write`)

```
You are a senior architect reviewing a critical scope decision for the
HUAKAI AI gateway's W11-F fingerprint mimicry epic. Owner needs an
independent second opinion. Your job: read the evidence below, draft your
recommendation among options A/B/C (or propose D if you see something
better), under 800 words. No code changes.

CONTEXT (HUAKAI W11-F):
HUAKAI is an MIT-licensed AI gateway that proxies user requests to upstream
LLM vendors (Anthropic, OpenAI, Gemini, Kiro). The W11-F epic adds TLS +
HTTP/2 fingerprint mimicry on HUAKAI's outbound to api.anthropic.com etc.,
so HUAKAI doesn't get detected as a non-SDK request.

Owner's per-vendor principle (newly codified in AGENTS.md): for each
vendor, HUAKAI mimics what THAT vendor's official first-party CLI /
desktop client actually sends on the wire. Inferring from reference
projects is forbidden; only real captures count.

EVIDENCE:

1. SDK source reads (clean-room L0):
   anthropics/anthropic-sdk-typescript@32ce8c0 uses Node global fetch /
   undici with default config; zero allowH2/Dispatcher in src/.
   anthropics/anthropic-sdk-python@5db69c6 uses httpx.Client with default
   config; zero http2/HTTP2/allow_h2. Both SDK defaults = h1.1.

2. Cross-library h2 divergence (committed at c69a034): undici v7 with
   allowH2=True sends 2 SETTINGS params (INITIAL_WINDOW_SIZE=262144);
   httpx 0.28.1 with http2=True sends 6 (INITIAL_WINDOW_SIZE=65535).
   Pseudo-header order also differs (alphabetical vs HTTP semantic).
   No h2 baseline generalizes.

3. Reference projects (under ~/refs/):
   - router-for-me/CLIProxyAPI@21fad9d (MIT, today): for api.anthropic.com
     uses utls Chrome + http2.Transport, source comment frames the path as
     mimicking CC's TLS behavior. For Antigravity forces HTTP/1.1 with
     comment about mimicking Node.js https defaults.
   - Xerxes-2/clewdr@5762680 (AGPL): uses wreq Emulation::Chrome145.
   - 0x676e67/rquest@e8781fb (MIT/Apache): default alpn [HTTP2, HTTP1].
   - 2 of 2 maintained AI gateways pick h2 + Chrome-like for Anthropic,
     citations thin (no captures in their repos).

4. DIRECT EVIDENCE (this commit, e619b87): real Claude Code CLI v2.1.112
   driven as subprocess against local h2-only ALPN server (via
   ANTHROPIC_BASE_URL + NODE_EXTRA_CA_CERTS env). All 5 connections
   negotiated alpn_negotiated=null. Same server, undici and httpx probes
   negotiate h2 successfully. CONCLUSION: CC CLI does NOT advertise h2.
   cliproxyapi's source-comment intent is contradicted by direct capture.
   JSONL at tools/fingerprint-collector/captures/h2-server-1779775310.jsonl.

5. Per-profile audit:
   - anthropic-claude-code.json: alpn=["http/1.1"], h1.1 wire (just
     confirmed by item 4)
   - codex-cli.json: alpn=[] (no ALPN), h2 or h1.1 per reqwest default
   - gemini-advanced.json: alpn=["h2","http/1.1"] advertised, Google
     picks h1.1
   - kiro-cli.json: alpn=[] (no ALPN), h1.1
   NO profile actually uses h2 on wire.

W11-F F-1 EPIC STATE:
- F-1.a/b/c/d/f/g landed; F-1.e (HTTP/2 fork outbound client) not started.
- F-1 Released criterion §5 #4 (>= 1 profile h2 fixture non-vacuous):
  N/A now.

OPTIONS:
A. Accept dormant — keep F-1.b through F-1.g as dormant infrastructure;
   F-1.e indefinitely deferred; Released criterion #4 re-scoped to
   "profile alpn matches its real capture".
B. Revert — rip out F-1.b through F-1.g (7 commits), shrink W11-F to F-2
   only; lose reviewed h2 infrastructure.
C. Plan trio re-evaluate W11-F entire scope.

CONSTRAINTS: CLAUDE.md #10 material decision; CLAUDE.md #11 clean-room L0;
CLAUDE.md #14 mutation discipline; Owner Feature Preservation Rule (prefer
minimal scope BUT don't delete features without safe equivalent).

YOUR JOB:
1. Pick A, B, C, or propose D
2. Justify with reference to specific evidence + constraints
3. Note risks + what you'd want to see before declaring W11-F Released
4. Under 800 words
```

---

## Codex reply (verbatim from `codex exec` output)

> **Recommendation: A, with explicit dormancy gates.**
>
> Keep F-1.b through F-1.g as dormant infrastructure, defer F-1.e, and
> re-scope the Released criterion from "at least one non-vacuous h2
> fixture" to "each profile's ALPN and wire protocol match its own
> first-party capture."
>
> The decisive evidence is #4: real Claude Code CLI v2.1.112, driven
> through the local h2-only ALPN server, did not negotiate h2 across 5
> connections, while the same server accepted h2 from undici and httpx
> probes. That directly falsifies the "Anthropic official client is
> h2-like" assumption for this profile. Evidence #1 also supports caution:
> official Anthropic SDK defaults are h1.1. Evidence #2 proves there is
> no safe generic h2 fingerprint to use anyway. Evidence #3 becomes
> non-decisive because reference-project intent/comments are contradicted
> by direct first-party capture, and AGENTS.md now forbids deriving
> vendor profiles from reference projects.
>
> Do not choose B. Reverting seven reviewed commits creates churn and
> destroys potentially useful infrastructure for a future vendor/profile
> that actually captures h2. It also conflicts with Feature Preservation:
> the safer move is to preserve capability behind evidence gates, not
> delete it because current profiles do not need it.
>
> Do not choose C unless there is broader uncertainty beyond F-1.e. The
> scope decision is narrow enough: W11-F should mimic first-party
> captures, and current captures say no active h2 outbound path is needed.
>
> Risks to record:
>
> - Dormant code can rot or be accidentally enabled without a valid
>   profile.
> - Tests may become vacuous if they assert h2 infrastructure while no
>   real profile uses h2.
> - Future maintainers may misread reference-project behavior as
>   sufficient evidence.
> - "Released" could be overstated if profile captures lack
>   reproducibility metadata.
>
> Before declaring W11-F Released, I would require:
>
> 1. Every vendor profile has `_field_sources` pointing to real
>    first-party capture artifacts, CLI/app version, timestamp, and
>    capture method.
> 2. Tests assert exact ALPN behavior per profile, including `http/1.1`,
>    no-ALPN, or advertised-but-not-selected cases.
> 3. h2 infrastructure remains unreachable unless a profile has direct
>    first-party h2 capture.
> 4. F-1.e is moved to Mandatory Roadmap or Feature Flag with an
>    activation rule: direct capture first, implementation second.
> 5. Release notes explicitly state that no current profile uses h2 on
>    wire, so the old F-1 criterion is intentionally N/A rather than
>    silently skipped.
>
> Owner 中文总结：建议选 A，但加明确证据门禁；保留已做的 h2 基础设施为休眠
> 能力，不继续做 F-1.e，也不把没有真实一方客户端捕获的 h2 当作发布要求。
> 没有功能缩水，因为能力被保留为未来有证据时启用；clean-room 风险更低，
> 因为不再跟随参考项目推断；主要安全/发布风险是休眠代码误启用和测试空转，
> 需要用 profile capture、ALPN 断言和 Feature Flag/Mandatory Roadmap
> 约束。

---

## Synthesis (Claude + Codex)

Both views align on **option A**. Codex adds 5 dormancy gates that Claude
had not articulated; Owner approved all 5 + the underlying A choice.

| Dimension | Claude (chat) | Codex (verbatim above) | Owner decision |
|---|---|---|---|
| Overall option | A | A + 5 dormancy gates | **A + 5 gates** |
| F-1.e | indefinite defer | Mandatory Roadmap / Feature Flag, capture-first activation rule | Mandatory Roadmap + Feature Flag |
| Already-committed h2 infra | dormant | dormant + hard-unreachable gate | dormant + hard-unreachable gate |
| Released §5 #4 | re-scope to "profile alpn matches own capture" | replace with conjunction of 5 gates | replace with 5 gates |
| Explicit risks | (not in Claude's view) | 4 risks listed (rot, vacuous tests, mis-reading refs, overstating Released) | adopted |
| Release notes language | (not in Claude's view) | explicit "no current profile uses h2, criterion intentionally N/A" | adopted |

Implementation lives in:
- `AGENTS.md` §"Dormant h2 outbound infrastructure gate" — Gate 3 + Gate 4
  enforcement, codex per-commit review rule.
- `W11-F-F1-status.md` §11 — full 5-gate text + acceptance status + 4
  follow-up slices (§11-Gate1-audit, §11-Gate2-slice, §11-Gate4-flag,
  §11-Gate5-release-template).
