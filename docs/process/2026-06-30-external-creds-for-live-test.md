# 全栈实测·需要 Owner 提供的真实凭证清单

用途:本地 docker 测试栈(huakai-fulltest)对「连外部」的功能做端到端实测时需要的真实凭证。
不提供也行——这些功能在自动实测里会标 **SKIP**,其余自有逻辑照常测。
安全:你给的任何真实凭证只在本地测试栈用,**绝不提交进 git、绝不外泄**,测完即可作废/轮换。

按价值排序:

## ① ⭐真上游账号 / API Key(最核心 —— 验证「中转转发」到底通不通)
这是验证产品核心的关键:加进账号池 → 建 HUAKAI key → 发真请求 → 拿上游**真回复** → 核对计费/用量扣减。
**任选其一**即可:
- **Claude API key**:`sk-ant-...`(Anthropic 控制台)
- **OpenAI API key**:`sk-...`
- **Gemini API key**:`AIza...`
- **Claude Code OAuth 凭证**:OAuth refresh token / 凭证 JSON(若你用的是订阅式 Claude Code 账号)

请告诉我:① 哪个 provider;② key 值(或 OAuth 凭证);③(可选)指定测哪个模型。

## ② 真 Telegram bot(验证真登录按钮)
- BotFather 给的 **bot token**:`123456789:AAEx...`
- **bot 用户名**:如 `YourLoginBot`
- 在 BotFather 里把该 bot 的 **Login Domain** 设为测试访问域名/IP(Telegram widget 强制要求)

## ③ 真 OAuth app(验证真社交登录:GitHub / Google / 其它)
- provider 名(github / google / …)
- **client_id**
- **client_secret**
- **redirect_uri**(须与 app 配置一致)
- scopes(如适用)

## ④ 真 SMTP(验证验证邮件/密码重置邮件真实投递)
- SMTP **host** + **port**
- **username** + **password**
- **发件地址**(from)
- TLS 模式(STARTTLS / SSL / 无)

## ⑤ 支付网关(可不提供)
HUAKAI 本来就是「手动充值优先」,真支付网关是可选增强;暂不需要。

---
当前状态:外部依赖项全部 SKIP,自动实测正在跑「其余所有链路」(注册/登录/Key/钱包/订单/订阅/资料/后台全部管理面)。
跑完给总表;之后你按本清单提供哪项,我就把对应那块从 SKIP 转成真实端到端实测。
