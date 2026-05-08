# 2026-05-07 Bedrock A2+A3 Codex Plan

| Owner directive | "实现 HUAKAI Bedrock plan A2+A3 合并 atomic：AWS EventStream binary decoder + BedrockEventStreamScanner。Clean-room 强制（CLAUDE.md #11）。" |
| Scope | In: write fresh implementation and tests under `/tmp/parallel-a2a3-codex/backend/internal/provider/bedrock/eventstream/` and `/tmp/parallel-a2a3-codex/backend/internal/gateway/`. Out: no repo production mutation, no AWS SDK source, no new dependency, no schema/auth/billing/quota/deploy changes. |
| Success criteria | Decoder validates prelude/message CRC32-IEEE, length limits, string headers, truncation and unsupported header errors. Scanner implements `StreamScanner`, converts Bedrock chunk envelopes to `SSEEvent`, emits exception/error as terminal errors, skips unknown events, and tests pass. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation lane. |
| Blast radius | Output is isolated in `/tmp/parallel-a2a3-codex`; repository risk is limited to this plan artifact. |
| Failure modes | CRC boundary mistakes: covered by fixed hex fixture and mismatch tests. Encoder/decoder mutual masking: covered by hard-coded fixture. Clean-room leakage: use only public AWS wire format and Bedrock streaming docs, no SDK-derived source. Interface mismatch: read HUAKAI local gateway interface only. |
| Decision points | Owner confirmation required only if asked to write into repo production files, add dependencies, alter registry wiring, or touch high-risk files. |
| Pre-execution checklist | 1. Read CLAUDE.md #11 and Rules clean-room summary. 2. Record public AWS source URLs. 3. Use `io.ReadFull`, not `bufio.Scanner`, for binary decoder. 4. Keep files under 500 LoC. 5. Run targeted Go tests from isolated output copy. |

Concrete execution order:

1. Create `/tmp/parallel-a2a3-codex/backend` with a minimal copy of `go.mod` and required HUAKAI gateway interface files for compilation.
2. Implement `eventstream.Decoder` from the public AWS/Smithy Event Stream wire format.
3. Implement decoder tests with an independent test encoder plus a fixed hex fixture.
4. Implement `BedrockEventStreamScanner` against HUAKAI `StreamScanner`.
5. Implement scanner tests using the A2 test-style encoder shape in the gateway package.
6. Run `go test ./internal/provider/bedrock/eventstream ./internal/gateway` from `/tmp/parallel-a2a3-codex/backend`.
7. Report file list, key signatures, clean-room source URLs, and test simulation strategy.
