# 2026-06-24 backend quality renew round158 userkey randomHex deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/userkey/userkey.go` 中已核实无引用的 `randomHex` helper，以及仅由该 helper 使用的 `crypto/rand`、`encoding/hex` import；清理 `backend/scripts/staticcheck-baseline.txt` 对应 U1000。 |
| Success criteria | `randomHex`、`crypto/rand`、`encoding/hex` 在 `userkey.go` 中无残留；baseline 不再包含该 U1000；不覆盖 `userkey.go` 中已有未提交改动。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低到中：文件属于用户 API key 管理域，但删除对象是未调用 helper，不改变签发、bcrypt、owner WHERE、Patch 或审计行为。 |
| Failure modes | 若 build tag 下有未搜索到调用会编译失败；当前 `rg` 在仓内只发现定义与 baseline。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改 API key 生成、hash、owner 校验或 auth 解析路径，停止并请求 Owner 确认；本轮只删死代码。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已检查当前 `userkey.go` dirty diff；3. 已用 `rg` 核实 helper 无调用；4. 不改已有 Patch 注释改动；5. 清理单条 baseline。 |

## 执行顺序

1. 删除 `randomHex` 函数。
2. 删除仅由它使用的 `crypto/rand`、`encoding/hex` import。
3. 删除对应 staticcheck baseline 条目。
4. 用 `rg`、`git diff --check`、clean-room 词扫描核验，并尝试 `gofmt/go test`。
