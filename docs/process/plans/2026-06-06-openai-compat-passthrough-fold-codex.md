# 2026-06-06 OpenAI-Compatible Passthrough Adapter Fold

| Owner directive | "合并 8 个 OpenAI-兼容厂商适配器包 -> 1 个通用适配器(去碎片+去重, 行为零改变)" |
| Scope | In: local HUAKAI provider adapter consolidation for behavior-identical OpenAI-compatible passthrough packages, registry registration preservation, replacement tests, and verification. Out: `/home/ubuntu/refs`, frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`, commits, schema/auth/billing/quota/deploy changes. |
| Success criteria | Every folded protocol keeps the same protocol constant, platform string, endpoint, accepted credential types, Authorization behavior, JSON headers, endpoint override behavior, upstream passthrough endpoint selection, and error-prefix behavior. Registry default protocol -> platform -> endpoint set remains unchanged for folded packages. OpenRouter is retained because local source verification found optional `HTTP-Referer` and `X-Title` header behavior not present in DeepSeek. Required Go build/vet/tests pass or any failure is reported with exact output. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation session. |
| Blast radius | Provider adapter construction and default registry wiring for DeepSeek, Fireworks, Grok, Groq Cloud, Mistral, Perplexity, and Together. OpenRouter remains on its package-specific adapter. |
| Failure modes | Accidental behavior shrinkage: mitigated by per-package diff verification, table-driven adapter tests, and registry endpoint regression tests. Registry drift: mitigated by explicit protocol/platform/endpoint assertions. Clean-room risk: mitigated by reading only local HUAKAI code and not `/home/ubuntu/refs`. Structural risk: provider is not frozen; no new files are added under frozen packages. |
| Decision points | No Owner confirmation needed for low/medium-risk local provider refactor. Stop and report if any additional package has real logic differences, if a required verification command cannot run, or if implementation would require touching high-risk files. |
| Pre-execution checklist | 1. Confirm worktree status. 2. Verify each candidate package against `deepseek` with only package/platform/endpoint/error-prefix/comment differences ignored. 3. Record fold set and exceptions. 4. Add failing tests before production code. 5. Implement generic provider adapter. 6. Update `registrydefault` registrations. 7. Delete folded package directories only. 8. Run gofmt/build/vet/tests. |

## Fold Set

- Fold into `provider.OpenAICompatPassthroughAdapter`: `deepseek`, `fireworks`, `grok`, `groqcloud`, `mistral`, `perplexity`, `together`.
- Keep unchanged: `openrouter`, because it sets optional attribution headers from credential extras.

## Concrete Execution Order

1. Add `backend/internal/provider/openai_compat_passthrough_test.go` with table-driven coverage for the seven folded platforms:
   - `Platform()` returns the configured platform string.
   - API key requests use the exact legacy endpoint and `Bearer ` authorization.
   - missing credential value returns the legacy platform-prefixed error.
   - unsupported OAuth credential returns the legacy platform-prefixed error.
   - custom endpoint override and upstream passthrough authorization preserve legacy behavior.
2. Add or extend `backend/internal/provider/registrydefault/default_test.go` so the seven folded protocol registrations still resolve to the exact platform and endpoint pair.
3. Run the new tests before implementation and confirm they fail because the generic adapter/type or registry endpoint expectations are not yet implemented.
4. Add `backend/internal/provider/openai_compat_passthrough.go` implementing the shared adapter in package `provider`.
5. Update `backend/internal/provider/registrydefault/default.go`:
   - Remove imports for the seven folded packages.
   - Register the seven protocols with `&provider.OpenAICompatPassthroughAdapter{PlatformName: "...", Endpoint: "..."}`.
   - Leave `openrouter.PassthroughAdapter` registered unchanged.
6. Delete only the seven folded package directories:
   - `backend/internal/provider/deepseek`
   - `backend/internal/provider/fireworks`
   - `backend/internal/provider/grok`
   - `backend/internal/provider/groqcloud`
   - `backend/internal/provider/mistral`
   - `backend/internal/provider/perplexity`
   - `backend/internal/provider/together`
7. Run `gofmt -w` on touched Go files.
8. From `backend`, with `GOCACHE=/tmp/go-build`, run:
   - `/usr/local/go/bin/go build ./...`
   - `/usr/local/go/bin/go vet ./internal/provider/...`
   - `/usr/local/go/bin/go test ./internal/provider/... ./internal/gateway/... -count=1`

## Self-Review Notes

- The plan does not add files under frozen packages.
- The plan preserves OpenRouter rather than forcing it into the generic adapter.
- The plan includes tests before production code and direct registry endpoint assertions.
