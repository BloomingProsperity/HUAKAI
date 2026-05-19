# 2026-05-13 openai responses client split

| Owner directive | "HUAKAI 后端：拆 openai_responses_client.go 单文件成 5 个文件（参考 anthropic_messages 已拆完的模式）。" |
| Scope | In: mechanically split `backend/internal/proto/openai_responses_client.go` into five same-package files matching the existing `anthropic_messages` layout; write required `/tmp` progress and final evidence files. Out: changing logic, changing tests, touching auth/billing/quota/schema/LICENSE, or modifying `openai_responses_client_test.go`. |
| Success criteria | Original file removed; each new file is <= 300 LoC; requested symbols land in requested files; `go build ./internal/proto/...`, `go test ./internal/proto/... -count=1 -run "TestOpenAIResponses"`, and `go vet ./internal/proto/...` pass from `backend/`; `/tmp/codex-split-openai-responses-final.txt` records LoC and test evidence. |
| Time estimate | 20-40 minutes wall clock; one Codex work unit. |
| Blast radius | Limited to `backend/internal/proto/openai_responses_*` compile surface; failures would be import/symbol placement mistakes caught by build/test/vet. |
| Failure modes | Missing import after split, duplicate declarations, accidental logic edit, LoC over target, existing unrelated dirty worktree confusing diff. Mitigation: inspect current symbol boundaries, move declarations without edits, run focused build/test/vet, review diff against intent. |
| Decision points | No Owner sign-off expected unless the task unexpectedly requires high-risk files or logic changes. |
| Pre-execution checklist | 1. Confirm actual module path. 2. Read anthropic split pattern. 3. Read full openai responses client. 4. Split by requested symbol groups. 5. Delete original file. 6. Append per-file `/tmp` progress markers. 7. Run requested checks. 8. Write final `/tmp` evidence. |
| Concrete execution order | Create types file, request file, parse file, response file, stream file; run `gofmt`; run `go build`, focused `go test`, `go vet`; write final evidence. |
