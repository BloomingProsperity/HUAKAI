# 2026-06-24 backend quality renew round159 otelbridge counter suffix deprecation

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 处理 `backend/internal/otelbridge/provider.go` 中 `otelprom.WithoutCounterSuffixes()` 的 SA1019；使用本地已存在的 `github.com/prometheus/otlptranslator` translation strategy 替代 deprecated option，并清理 `backend/scripts/staticcheck-baseline.txt` 对应条目。 |
| Success criteria | `WithoutCounterSuffixes()` 不再使用；Prometheus exporter 显式配置 `UnderscoreEscapingWithoutSuffixes`，避免 exporter 再追加 `_total`；现有 bridge counter 名称和测试期望保持一致；baseline 不再包含该 SA1019。 |
| Time estimate | 约 15 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 中：影响 `/metrics` 指标命名策略；已核实当前 OTel bridge 未使用 `WithUnit`，bridge counter 名称本身已经带 `_total`，因此选择 no-suffix strategy 以保持当前表面。 |
| Failure modes | 若未来新增带 unit 的 OTel instrument，本策略也不会追加单位后缀；该行为需在新增 instrument 时显式评估。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改变 Prometheus 指标命名规范或 go.mod 依赖归类，另起计划；本轮只替换 deprecated option，`otlptranslator` 已在当前 go.mod 中存在。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取本地 `go.opentelemetry.io/otel/exporters/prometheus@v0.65.0/config.go`；3. 已读取本地 `github.com/prometheus/otlptranslator@v1.0.0/strategy.go`；4. 已核实本包 bridge 测试断言显式 `_total` 名称；5. 清理单条 baseline。 |

## 执行顺序

1. 在 `provider.go` 引入 `github.com/prometheus/otlptranslator`。
2. 将 `otelprom.WithoutCounterSuffixes()` 替换为 `otelprom.WithTranslationStrategy(otlptranslator.UnderscoreEscapingWithoutSuffixes)`。
3. 删除对应 staticcheck baseline 条目。
4. 用 `rg`、`git diff --check`、clean-room 词扫描核验，并尝试 `gofmt/go test`。
