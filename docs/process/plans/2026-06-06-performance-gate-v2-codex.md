# Performance Gate v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a strong-discriminating mixed-load performance gate and informational hot-path benchmarks for report 141/147.

**Architecture:** Keep the blocking gate behavior-based: correctness plus `sum(request latency) / wall clock` parallelism, with p95 as a loose CI backstop only. Reuse HUAKAI's existing full-chain Chat Completions mock harness and add benchmarks in existing/non-frozen package surfaces without reading any reference-project source.

**Tech Stack:** Go tests/benchmarks, shell script, Makefile target, GitHub Actions.

---

| Owner directive | "性能 gate v2 —— 强判别混合负载(report 141/147)。测试力度要大, 必须真能抓串行化退化!" |
| Scope | In: mixed-load gatewayhttp test, full-chain benchmark, hot-path benchmarks, perf-gate script, Makefile target, CI step. Out: production behavior changes, commits, `/home/ubuntu/refs`, DB schema, auth/billing/quota core changes. |
| Success criteria | `TestChatCompletionsMixedLoadP95` drives 32 x 200 full-chain requests, asserts zero bad responses, exact settle count, parallelism >= 8.0, p95 < 200ms, and goroutine count <= baseline + 20 after settle. Benchmarks compile and can be run informationally. CI broad race skips the latency gate by env and a dedicated perf step runs it blocking. |
| Time estimate | 2-3 hours wall clock in one Codex session, dominated by package test/vet/build verification. |
| Blast radius | Test-only changes in `gatewayhttp`, benchmark-only changes in hot-path packages, script/Makefile/CI wiring. No production runtime dependency or schema mutation. |
| Failure modes | Sandbox cannot listen: existing `newGatewayHTTPTestServer` should skip listener-dependent tests. Perf threshold too tight: use parallelism as core discriminator and p95 200ms as loose guard; report measured factor. Benchmark requiring DB unavailable: billing benchmark should skip unless a DSN is present. CI race suite running latency gate: set skip env on broad `go test -race` and run dedicated perf step separately. |
| Decision points | No high-risk file is planned. `internal/proto` is frozen, so the proto benchmark must modify existing `field_matrix_test.go` instead of adding a new file even though other hot-path packages can add benchmark files. Owner/PM handles commit. |
| Pre-execution checklist | 1. Confirm worktree state. 2. Avoid `/home/ubuntu/refs`. 3. Modify no frozen-package new files. 4. Write tests/benchmarks before any production code. 5. Run gofmt. 6. Run build/vet/tests/perf script where sandbox permits. |

## File Plan

- Modify `backend/internal/gatewayhttp/dispatch_smoke_test.go`: add reusable full-chain fixture helper, `TestChatCompletionsMixedLoadP95`, and `BenchmarkChatCompletionsFullChain`.
- Create `backend/internal/pricingeval/resolver_benchmark_test.go`: benchmark valid tiered `pricingeval.Resolve`.
- Create `backend/internal/billing/settler_benchmark_test.go`: benchmark DB-backed `DefaultSettler.Settle` when `HUAKAI_DATABASE_URL` or `HUAKAI_TEST_DATABASE_URL` is set; skip otherwise.
- Create `backend/internal/pool/router/default_selector_benchmark_test.go`: benchmark account selection hot path with in-memory sources.
- Modify `backend/internal/proto/field_matrix_test.go`: add field-matrix lookup benchmark in an existing frozen-package file.
- Create `backend/scripts/perf-gate.sh`: blocking mixed-load test plus informational benchmarks.
- Modify `backend/Makefile`: add `perf` target.
- Modify `.github/workflows/backend-ci.yml`: broad race suite gets latency-gate skip env; dedicated perf step runs script.

## Execution Order

- [ ] Read the exact helper functions and package APIs needed for fixtures and benchmarks.
- [ ] Refactor `dispatch_smoke_test.go` test setup into a local helper that returns handler, mock server cleanup, and settler counters without changing production code.
- [ ] Add `TestChatCompletionsMixedLoadP95` with K=32, M=200, latency slice sorting, p50/p95/p99 logging, parallelism assertion, p95 backstop, settle count assertion, bad response assertion, and goroutine leak check.
- [ ] Add the two mutation self-check comments inside the mixed-load test: global handler mutex should trip parallelism; leaked per-request goroutine should trip goroutine bound.
- [ ] Add `BenchmarkChatCompletionsFullChain` using the same mock chain.
- [ ] Add hot-path benchmarks for `pricingeval.Resolve`, billing settlement, pool selection, and proto field matrix lookup.
- [ ] Add `backend/scripts/perf-gate.sh` with `set -euo pipefail`; run the mixed-load test blocking and benchmarks informationally.
- [ ] Add `make perf` target that invokes the script.
- [ ] Add CI perf step and skip env on broad race step.
- [ ] Run `gofmt`.
- [ ] Run `/usr/local/go/bin/go build ./...`, `/usr/local/go/bin/go vet ./...`, targeted `go test` package set, `make perf` or the script, and selected benchmark command with `GOCACHE=/tmp/go-build`.
- [ ] Report exact measured p50/p95/p99/parallelism when available, and record any sandbox listener skips honestly.

## Clean-Room Notes

- This is an implementer-lane task. Only HUAKAI source, HUAKAI docs, and Owner-provided requirements are in scope.
- `/home/ubuntu/refs` is explicitly out of scope and must not be read.
- No reference project behavior claims are needed for this task.
