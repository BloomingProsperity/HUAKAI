# 2026-06-24 后端质量刷新 round133：cache_routing nil slice 检查清理

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-6：持续削减 staticcheck baseline 中可直接修复的测试噪声，避免真实问题被 baseline 淹没。 |
| Scope | 仅修复 `backend/internal/cache_routing/auto_inject_test.go` 中 `out == nil || len(out) == 0` 的冗余 nil slice 检查，并删除 `backend/scripts/staticcheck-baseline.txt` 对应 S1009。 |
| Success criteria | 测试仍然拒绝 nil/空返回体；断言改为 `len(out) == 0`；staticcheck baseline 不再包含 `internal/cache_routing/auto_inject_test.go` 的 S1009。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操 1 个小闭环。 |
| Blast radius | 单个 cache_routing 测试文件和 staticcheck baseline。失败时可能误削弱对空返回体的断言。 |
| Failure modes | 误删非空断言：保留 `len(out) == 0`；baseline 删除过宽：只删精确 S1009 条目；Go 工具链不可用：记录并运行文本级检查。 |
| Decision points | 无需 Owner 中途确认；本轮不触碰生产 cache routing 逻辑、billing、quota、auth、schema 或部署脚本。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 `auto_inject_test.go`；3. 已确认 `len(nil)==0` 能覆盖 nil slice；4. 已确认 baseline 精确命中 S1009。 |

## 执行顺序

1. 将冗余 `out == nil || len(out) == 0` 简化为 `len(out) == 0`。
2. 删除 `staticcheck-baseline.txt` 中对应 S1009。
3. 运行 scoped whitespace、clean-room 词、S1009 残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
