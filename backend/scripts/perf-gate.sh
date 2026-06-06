#!/usr/bin/env bash
set -euo pipefail

GO_BIN="${GO:-go}"
export HUAKAI_SKIP_PERF_LATENCY_GATE=0

echo "[perf-gate] blocking mixed-load latency/concurrency gate"
"${GO_BIN}" test ./internal/gatewayhttp \
  -run '^TestChatCompletionsMixedLoadP95$' \
  -count=1 \
  -timeout 2m \
  -v

echo "[perf-gate] informational hot-path benchmarks"
"${GO_BIN}" test \
  ./internal/gatewayhttp \
  ./internal/pricingeval \
  ./internal/billing \
  ./internal/pool/router \
  ./internal/proto \
  -run '^$' \
  -bench 'Benchmark(ChatCompletionsFullChain|ResolveTieredPricing|DefaultSettlerSettle|DefaultSelectorSelect|FieldMatrixLookup)$' \
  -benchmem \
  -count=1 \
  -timeout 10m
