# 2026-05-20 cleanup-batch3-codex

| Owner directive | "HUAKAI 代码清理 — 批次 3 (proto / provider / pool / obs / registry / transport / cmd 集群)。先读通用规则文件: /home/codex/.cache/cleanup-rules.txt —— 严格按其执行。" |
| Scope | In: comment-only cleanup in the requested Go files under backend/internal/{proto,provider,pool,obs,observability,registry,transport,cachemetrics,config,openapicheck,binding,credentialstore} and backend/cmd/gateway. Out: generated files, internal/db generated files, TODO(OCAW), non-comment code, dependencies, git operations. |
| Success criteria | All requested directories are searched with ripgrep; Codex review progress markers in comments are removed or normalized per cleanup rules; substantive comments remain; `go build ./...` passes from backend with the requested cache/tmp env. |
| Time estimate | 30-60 minutes wall clock; one Codex work unit. |
| Blast radius | Comment-only changes should not alter runtime behavior. The main risk is accidentally changing code or deleting useful rationale. |
| Failure modes | Over-matching real TODO/spec markers: mitigate by preserving OCAW, Phase, IDs, and rationale text. Touching generated files: mitigate by checking generated headers before edits. Missing non-`codex` markers such as `(N+4a)`: mitigate with separate ripgrep passes for `pass-` and `N+` forms. |
| Decision points | Stop for Owner confirmation only if cleanup requires high-risk files, generated code, or non-comment code changes; none are expected. |
| Pre-execution checklist | 1. Read cleanup rules. 2. Locate all in-scope `.go` and `*_test.go` marker comments using ripgrep. 3. Exclude generated files. 4. Apply comment-only edits. 5. Re-run marker searches. 6. Run requested `go build`. 7. Report changed `.go` files, marker counts, whole-line deletions, and build result. |
