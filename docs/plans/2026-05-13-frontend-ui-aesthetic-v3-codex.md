# 2026-05-13 frontend-ui-aesthetic-v3-codex

| Field | Content |
|---|---|
| Owner directive | "禁止紫色，绿色！重派codex" / "不要问 Owner，按你判断走。" |
| Scope | In: pure design token research for HUAKAI frontend v3, including 4-5 token maps, status color separation, one recommendation, and globals.css snippet. Out: frontend implementation, market ref refresh, reference-source reading, sub2api decomposition reading. |
| Success criteria | `docs/research/2026-05-13-frontend-ui-aesthetic-v3-codex.md` exists in Chinese, avoids forbidden primary hues, avoids green success, explicitly separates primary/success/warning/danger, and includes a pasteable CSS variable block. |
| Time estimate | 45-75 minutes wall clock; single Codex authoring pass plus local verification. |
| Blast radius | Low: docs-only plus `/tmp/codex-ui-aesthetic-v3.txt` progress stub. No product code, schema, auth, billing, quota, secrets, or deployment changes. |
| Failure modes | Palette accidentally re-enters banned hue ranges; status colors collide with primary; HSL values drift from hex; v3 repeats v2 choices too closely. Mitigation: compute HSL from hex locally, avoid blue/purple/green primary ranges, and state collision handling per palette. |
| Decision points | None requiring Owner sign-off under the current directive; recommendation will be made explicitly with risks. |
| Pre-execution checklist | 1. Read v2 doc only for known failure context. 2. Do not fetch external refs. 3. Do not read sub2api decomposition. 4. Create `/tmp/codex-ui-aesthetic-v3.txt` stub. 5. Generate token maps and verify primary hue constraints. 6. Write research doc. |
| Concrete execution order | Draft five palettes; compute HSL values; write sections incrementally to `/tmp` scratch; create final research doc; verify banned hue/status requirements by scanning the output. |
