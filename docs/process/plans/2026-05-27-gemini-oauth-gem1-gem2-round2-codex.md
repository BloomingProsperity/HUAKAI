# 2026-05-27 GEM-1+2 Round 2 S1 修复

| Owner directive | "GEM-1+2 Round 2 修 Codex review 抓出的 2 个 S1" |
| Scope | 只修 Gemini OAuth S1-A authorize URL 缺 refresh_token 参数、S1-B HTTPS admin redirect host 任意通过。 |
| Out of scope | 不改通用 `BuildAuthorizeURL`；不接新的 operator-config/ProfileBindings wiring；不改 auth/billing/quota/schema；不 commit/push。 |
| Success criteria | 新增判别测试覆盖 `access_type=offline`、`prompt=consent`、任意 HTTPS admin host 拒绝、allowlist 命中接受；现有 Gemini OAuth 测试保持通过。 |
| Time estimate | 30-45 分钟；单 Codex 小补丁。 |
| Blast radius | `backend/internal/credentialacq` Gemini OAuth 启动和 redirect 校验；既有 admin integration 测试 fixture 需要显式 allowlist。 |
| Failure modes | 误改通用 OAuth vendor；HTTPS admin 模式继续信任 request body；测试 fixture 非判别性；admin callback 临时关闭没有文档化。 |
| Mitigation | Gemini 专用 helper；默认空 allowlist 拒绝所有 HTTPS admin callback；红绿测试；只跑定向 Go 测试。 |
| Decision points | 后续 GEM-X 需要 Owner 批准 operator-config/ProfileBindings 静态注入 admin callback allowlist 后才能默认启用 HTTPS admin callback。 |
| Pre-execution checklist | 1. 读 Gemini OAuth 实现和测试；2. 读 stored PKCE start helper；3. 先写失败测试；4. 最小实现；5. 跑定向测试。 |
