# 2026-05-27 ChatGPT R2 admin flow_id fix

| Owner directive | "CHG R2 verify 抓出 S1-B (admin OAuth flow_id 丢失)" |
| Scope | 修 `openai/chatgpt_oauth` admin-mode authorize URL，使 `redirect_uri` query preserve `flow_id`；补判别测试；让 ChatGPT admin callback integration 走 provider redirect 形态；检查 Gemini 同类风险并记录路标。 |
| Out of scope | 不改 state 编码方案；不修 Gemini 实现；不改 auth/billing/quota/schema/deploy；不 commit。 |
| Success criteria | `TestChatGPTAuthorizeURLPreservesFlowIDInAdminMode` 在删除 flow_id preserve 时变红；`TestChatGPTOAuthAdminCallbackEndToEnd` 不再手动传 flow id，而是从 provider redirect URL query 取到；指定 Go 测试通过或如实记录失败。 |
| Time estimate | 45-75 分钟。 |
| Blast radius | `backend/internal/credentialacq` OAuth start/callback；`StartInput` 若需要预置 flow id，属于内部非 schema contract 扩展。 |
| Failure modes | 只改 authorize URL 但 token exchange 使用旧 redirect URI；allowlist 被动态 query 绕过；测试继续手动注入 flow id；Gemini 同 bug 被误报已修。 |
| Mitigation | admin redirect helper 只允许 `flow_id` query；stored PKCE payload 与 authorize `redirect_uri` 保持一致；测试从 authorize URL 模拟 provider callback；Gemini 只登记 `R-GEM-FLOW-ID-001`。 |
| Decision points | 无高风险 Owner 阻断项；Gemini 修复另开切片。 |
| Pre-execution checklist | 1. 读 ChatGPT/Gemini OAuth builder 与 admin callback handler；2. 先写 ChatGPT 红测；3. 最小实现；4. 更新 integration test；5. 记录 Gemini risk；6. 跑验收命令。 |
