# 2026-06-23 backend-quality-renew round64 signed-envelope

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 `backend/internal/usersession/rotation.go` 与 `backend/internal/twofa/challenge.go` 的 HMAC signed-envelope 编解码重复、错误语义、测试覆盖与后续抽公共工具的可行性。 |
| Out of scope | 不修改 session / 2FA 生产逻辑；不展开安全专项；不改 auth core、数据库 schema、billing/quota；不触碰其它目标。 |
| Success criteria | 输出中文 findings，逐条带绝对路径行号、具体函数/类型、触发条件和可执行修法。 |
| Time estimate | 约 20-30 分钟；一次 Codex renew 审查切面。 |
| Blast radius | 默认只读源码并写计划文件；若误改签名逻辑会影响 session 轮换和 2FA 登录，因此本轮不做生产代码改动。 |
| Failure modes | 只凭函数名判断重复；忽略两套 token 的字段/TTL 差异；把安全性质问题展开为 security 专项。缓解：逐文件读实现与测试，只报代码质量和漂移风险。 |
| Decision points | 若确认重复成立，后续由 Owner 决定是否抽 `internal/signedenvelope` 公共包，并以 type alias/adapter 兼容原调用方。 |
| Pre-execution checklist | 1. 读取 goal objective；2. 读取两个签名实现；3. 检索签名/验签调用方；4. 读取相关测试；5. 尝试运行相关测试并记录环境限制。 |
| Concrete execution order | 先读源码，再读测试与调用方，最后输出 findings，不写额外报告。 |
