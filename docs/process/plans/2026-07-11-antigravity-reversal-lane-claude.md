# Antigravity 反转车道补全 — 计划(Claude)— 2026-07-11

## 背景与决策
Owner 已提供真 consumer 号(~/.gemini/antigravity-cli/antigravity-oauth-token)并拍板:
**内置真 Antigravity CLI 的 client_id/secret + 激活车道,保持 env-gate 默认关(部署方 opt-in)**。

调研坐实:Antigravity 复用 gemini-cli 同一后端 `cloudcode-pa.googleapis.com/v1internal`(非
api.antigravity.ai)。HUAKAI 已有半成型车道 + 正确的 `CodeAssistAdapter`(已打 cloudcode-pa)
+ `geminicodeassist` 响应 unwrap,antigravity 只差 UA/ideType/GOOGLE_ONE_AI credits + project_id 获取。

## Scope(全部在 default-off env-gate 后,不翻生产默认)
- G1 出站:废弃 antigravity_session.go 的错端点(api.antigravity.ai),让 antigravity 复用/内嵌
  CodeAssistAdapter 到 cloudcode-pa v1internal + antigravity UA/X-Goog-Api-Client + GOOGLE_ONE_AI credits。
- G2 响应映射:protocol_selector antigravity_session → geminicodeassist.Adapter(现错映射到 openai)。
- G3 project_id:建 loadCodeAssist→onboardUser 获取流(ideType=ANTIGRAVITY),consumer 号首次入池必需。
- G4 激活:内置真 client_id/secret(公开 wire 值,两镜逐字一致);重激活 mode_refresh 的 fail-closed
  暂停(geminiAntigravityPausedAdapter);env-gate 默认关不变。
- G5 导入:cli_import 解析嵌套 {token:{access_token,expiry,refresh_token}} + expiry→expires_at。

## Clean-room 铁律
client_id/secret/scopes/端点/UA/ideType/X-Goog-Api-Client 是**事实 wire 值**(两镜相同=公开常量),
可用值;但**禁逐字搬** CLIProxyAPI/sub2api 的函数名/struct/注释/结构,一律 paraphrase,复用 HUAKAI
现有 code_assist.go 模式。注释中文、不写借鉴项目名/任务号。

## 成功标准
- 机械正确性单元可测(mock cloudcode-pa 响应):端点对、envelope 对、响应 unwrap 对、project_id
  获取流对、import 解析对,每项判别测试变异证。
- 车道默认关(env 未设时惰性,不影响热路径);激活(env 开)时能构造出正确 adapter。
- 真账号 E2E 实测(我亲跑):用 Owner 号打 cloudcode-pa,验 loadCodeAssist/fetchAvailableModels/
  generateContent 真通、账目落账——留 Claude 验收,不在本 codex 切片内。

## Owner-gated 残留(本切片不做,后续 surface)
- 默认 env-gate 翻 on(生产默认行为翻转)。
- claude-sonnet-4 等经 antigravity 出(G7 协议翻译)。
- GOOGLE_ONE_AI credits 计费/额度语义 + 403 封号态建模(触 quota/money)。

## Blast radius
新上游车道,默认关;误改可能影响 gemini code_assist 车道(共用 adapter)——须确保 antigravity
改动不回归现有 gemini code_assist(单独判别测试)。
