# 2026-05-13 OpenAI Chat Client Split
| Owner directive | "HUAKAI 后端：拆 openai_chat_client.go 单文件成 5 个文件（参考 anthropic_messages 已拆完的模式）。" |
| Scope | In: split `backend/internal/proto/openai_chat_client.go` into five same-package production files matching the requested names and boundaries. Out: tests, behavior changes, new packages, new dependencies. |
| Success criteria | Original production file removed; five new files exist; each file is <= 300 LoC; `go build ./internal/proto/...`, `go test ./internal/proto/... -count=1 -run "TestOpenAIChat"`, and `go vet ./internal/proto/...` pass from `backend/`; `/tmp/codex-split-openai-chat-final.txt` records LoC and verification evidence. |
| Time estimate | Wall clock 20-40 minutes; agent time one implementation pass plus formatting and checks. |
| Blast radius | Low-to-medium: package-level symbol moves can break compile if imports are wrong, but no logic should change. |
| Failure modes | Missing import after split, duplicated import, dropped helper, stale original file causing duplicate declarations, file > 300 LoC. Mitigation: line-number based extraction, `gofmt`, build/test/vet, LoC check. |
| Decision points | None expected. High-risk areas such as schema, auth, billing, quota, deployment, secrets, and `LICENSE` are out of scope. |
| Pre-execution checklist | Stub file written to `/tmp/codex-split-openai-chat.txt`; read `anthropic_messages_{types,request,parse,response,stream}.go`; read current `openai_chat_client.go`; preserve `openai_chat_client_test.go`; update progress file after each split file. |
| Concrete execution order | Create five split files using the existing code only; append per-file progress markers; delete original file; run `gofmt`; run required build/test/vet commands from `backend/`; write final evidence file. |
