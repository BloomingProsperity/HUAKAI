# 2026-05-21 juice-web-crawl-codex
| Owner directive | "深度调研任务 —— 全网抓取「LLM 模型降算力 / 静默替换 / 模型验真」这个方向的一切。" |
| Scope | In: web search, public pages, GitHub metadata pages/API where readable, academic pages, community discussions, commercial observability/audit services, Chinese and English sources. Out: git operations, cloning, source-code copying, implementation changes. |
| Success criteria | Produce `docs/process/research/2026-05-21-juice-web-crawl-codex.md` in Chinese with categorized methods, per-item URL/method/maturity/bypassability, and summary answers/top 5. |
| Time estimate | 30-45 minutes wall clock; one Codex research pass. |
| Blast radius | Low: documentation-only research artifact under `docs/process/research/`; no production code, schema, secrets, or git state touched. |
| Failure modes | Search engines miss niche posts; pages block access; GitHub stars are dynamic; social posts may be hard to verify. Mitigation: cite only readable URLs, mark unreadable/uncertain fields explicitly, and avoid invented counts or claims. |
| Decision points | None expected unless Owner asks for deeper source-code mining of a non-MIT project, which would require clean-room lane handling. |
| Pre-execution checklist | Confirm no git operations; create research directory; gather broad English/Chinese queries; verify real URLs; distinguish projects/services/papers/discussions; write report with source links and caveats. |
| Concrete execution order | 1. Search GitHub/project candidates. 2. Search academic/black-box verification/fingerprinting/monitoring literature. 3. Search Chinese community/tooling discussions. 4. Search English community and commercial observability. 5. Synthesize method taxonomy and HUAKAI top 5. |
