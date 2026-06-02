# 2026-06-02 model-auto-sync Codex plan

| Owner directive | "HUAKAI 动态模型目录自动同步(对照 CLIProxyAPI,clean-room 引证不抄)。IMPLEMENTER。非冻结包。中文注释。自主→push origin HEAD:work/model-auto-sync。不碰 landing。" |
| Scope | In: vendor model-list fetching for anthropic/openai/gemini, startup + interval sync wiring, runtime registry refresh via existing model registry tables, admin trigger endpoint, discriminating tests, build/test/review/push. Out: pricing table edits, alias rewrite, pool binding creation, auth/billing/quota core changes, landing branch. |
| Success criteria | New upstream model appears in registry after sync; deleted upstream model is marked unavailable, not hard-deleted; failed sync leaves prior registry rows unchanged; admin endpoint can trigger sync with tenant scope validation; startup scheduler is configurable and disabled by default unless env enables it; `go build ./...` and `go test ./...` pass or failures are reported honestly. |
| Time estimate | 2-4 hours wall clock for source read, TDD, implementation, verification, self-review, and push. |
| Blast radius | `backend/internal/registry`, a new non-frozen sync package, `backend/internal/adminhttp`, gateway wiring/config, OpenAPI route consistency if required. Avoid `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`, `LICENSE`, payment/auth/quota/billing ledger core. |
| Failure modes | Vendor API shape mismatch -> tolerant parsers + fixtures; failed sync pollutes registry -> two-phase plan/apply with write only after successful fetch; deletion removes operator aliases/prices/bindings -> disable only rows sourced by auto-sync and never touch operator/tenant aliases/bindings; scheduler leaks goroutines -> explicit Stop in lifecycle; clean-room leakage -> record behavior-only citations and avoid upstream identifiers/structure in code. |
| Decision points | Schema: expected no new schema because 0008 model registry tables already hold models/aliases/status/source/snapshot. If implementation needs a new table, mark RESULT as Owner schema confirmation required before merge. Dependency: no new runtime dependency planned; if needed, stop before adding it. |
| Pre-execution checklist | 1. Read project rules and relevant backend files. 2. Read CLIProxyAPI source regions under clean-room lane guard only for behavior evidence. 3. Write failing discriminating tests. 4. Implement local APIs using HUAKAI-owned names and structures. 5. Wire config, scheduler, and admin endpoint. 6. Run targeted tests, then `go test ./...` and `go build ./...`. 7. Stage intended files only and run `codex exec review --uncommitted --full-auto --sandbox read-only` for <=2 rounds. 8. Commit and push `origin HEAD:work/model-auto-sync`. |

## Clean-room lane guard record

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## Concrete execution order

1. Inspect `~/refs/CLIProxyAPI` only for the requested model update and service behavior, then record short behavior evidence with commit SHA and file:line anchors.
2. Add tests before implementation:
   - catalog fetch normalizes a new vendor model into a sync plan and registry write makes it visible;
   - a missing previously auto-synced model becomes disabled instead of deleted;
   - fetch failure returns an error and leaves the registry unchanged.
3. Add a new non-frozen package for vendor fetch/scheduler orchestration and keep registry DB mutation logic in `backend/internal/registry`.
4. Add admin HTTP trigger in `backend/internal/adminhttp` and route wiring in `backend/cmd/gateway`.
5. Add config env parsing for enable/interval/vendor URLs. Defaults: disabled startup scheduler, conservative interval, official endpoint defaults when no override is set.
6. Run verification, self-review, commit, and push the requested branch.

## Clean-room behavior evidence from CLIProxyAPI

Local mirror note: `~/refs/CLIProxyAPI` is not a git checkout; `.huakai-head-sha`
records `21fad9dbb447a2ab70d51d0ac3e3d032525a6054`.

- Observed: the reference loads a bundled model catalog as startup fallback, then attempts a remote refresh at startup and on a fixed interval; failed remote refresh leaves current data in place. Evidence: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/registry/model_updater.go:67`, `:74`, `:83`, `:115`, `:118`, `:119`, `:120`.
- Observed: the remote refresh path validates fetched catalog content before replacing the in-process catalog, and compares old/new provider sections to decide which provider families changed. Evidence: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/registry/model_updater.go:124`, `:127`, `:141`, `:177`, `:182`, `:192`, `:227`.
- Observed: when the service learns that provider catalog content changed, it walks active credentials for those providers and re-applies model registration so runtime routing/scheduling sees the new model set. Evidence: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/service.go:852`, `:856`, `:866`, `:872`, `:880`, `:1046`, `:1232`, `:1238`, `:1245`, `:1260`, `:1276`.
- Observed: the runtime registry increments/removes model availability for a client registration and clears cached available-model snapshots when registrations change. HUAKAI will not copy this memory structure; the safe equivalent is a PostgreSQL catalog update plus registry snapshot bump. Evidence: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/registry/model_registry.go:230`, `:258`, `:271`, `:304`, `:347`, `:366`, `:439`, `:491`, `:520`, `:575`.
- Observed: model catalog sections include provider families relevant to this task and model metadata such as ids, owner/type, display name, token limits, and supported generation methods. Evidence: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/registry/model_definitions.go:16`, `:32`, `:37`, `:214`; `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/registry/models/models.json:2`, `:181`, `:1318`.

Source files read: `LICENSE`, `.huakai-head-sha`, `internal/registry/model_updater.go`, `sdk/cliproxy/service.go`, `sdk/cliproxy/model_registry.go`, `internal/registry/model_registry.go`, `internal/registry/model_definitions.go`, `internal/registry/models/models.json`, `sdk/cliproxy/auth/conductor.go`
Lane: specifier
Agent: GPT-5 Codex in ChatGPT session
UTC timestamp: 2026-06-02T08:56:38Z
