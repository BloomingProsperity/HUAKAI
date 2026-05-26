# Deferred Review Finding: Hermes bridge SSE scanner buffer

Status: Deferred S2

Source: Slice 2.2.b Gateway Round 2 review S2 #4.

Rationale: `backend/internal/hermeschat/bridge.go` uses `bufio.Scanner` with an explicit 1 MiB token limit for runner SSE lines. Current Hermes runner SSE chunks are expected to be small token/done/conversation events, so this is not a release-blocking correctness or security issue for Slice 2.2.b. The remaining risk is a future oversized SSE data line returning `bufio.Scanner: token too long` and terminating the stream.

Follow-up: In a later Hermes bridge hardening slice, replace `bufio.Scanner` with `bufio.Reader` or an explicit `io.Reader` line reader that enforces a documented maximum event size and returns a controlled gateway error for oversized events.

Owner decision: No immediate confirmation needed unless Hermes runner starts emitting single SSE lines over 1 MiB before that follow-up slice.
