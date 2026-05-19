# 2026-05-14 T8 Redact Audience
| Owner directive | "HUAKAI trust-chain T8 — audience-based redaction levels... 新建 internal/redact/audience.go + audience_test.go... 不动现有 internal/redact/allowlist.go 和 redactor.go... 不要问 Owner。" |
| Scope | In: 在现有 Go module 的 `backend/internal/redact` 包新增 audience 分级字段过滤和测试。Out: 不读取 Oktsec 源码，不改现有 `allowlist.go` / `redactor.go`，不改 ledger / auth / billing / quota / DB。 |
| Success criteria | 4 个 audience 枚举可用；各级字段集合严格受 T0 allowlist 约束；internal 不按 audience 过滤但仍拒绝 T0 forbidden/unknown 字段；12+ 单测覆盖 allow/reject/hierarchy/forbidden/empty/nil；现有 redact 测试继续通过；go test/vet 干净。 |
| Time estimate | 20-35 分钟 wall clock；Codex 单轮完成。 |
| Blast radius | 仅新增 redact 包文件；风险集中在字段集漏配、hierarchy 断裂、internal 误放 forbidden 字段。 |
| Failure modes | 误把不存在于 T0 allowlist 的字段放入 audience；通过 `IsSafeField` 二次过滤缓解。字段集合被调用方修改污染；返回拷贝缓解。测试只测拒绝不测允许；用表驱动显式断言缓解。 |
| Decision points | 无需 Owner 中途确认；若必须改高风险文件或 T0 allowlist 才能满足需求则停止，但本任务可用现有字段完成。 |
| Pre-execution checklist | 1. 初始化 `/tmp/codex-t8-redact-audience.txt`。2. 读取现有 redact 包和测试风格。3. 新增 audience 实现。4. 新增 12+ 单测。5. 运行 gofmt/go test/go vet。6. 写 `/tmp/codex-t8-redact-audience-final.txt`。 |
| Concrete execution order | 先实现字段集合与拷贝/过滤函数；再写测试覆盖四级 audience、层级、禁字段、nil/empty；最后运行回归并记录证据。 |
