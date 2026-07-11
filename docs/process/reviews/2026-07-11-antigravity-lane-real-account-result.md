# Antigravity 反转车道 G1-G5 真账号实测结果 — 2026-07-11

Owner 硬规则「闭环之后要实测」。Antigravity 车道补全(G1 出站端点 / G2 响应映射 /
G3 project_id 获取流 / G4 内置公开 OAuth 凭据激活 / G5 CLI 嵌套 token 导入)落地后,
用 Owner 提供的真 consumer 号打真 cloudcode-pa,验证整条车道的 wire 假设是否属实。

## 结果:三关真账号 E2E 全通,所有 wire 假设坐实

用内置公开 Antigravity CLI OAuth 客户端 + Owner 的 refresh_token,对真
`cloudcode-pa.googleapis.com/v1internal` 顺序跑三关,全部 HTTP 200:

| 关卡 | 端点 | 结论 |
|---|---|---|
| 一 刷新 | oauth2.googleapis.com/token | 内置公开 client_id/secret 成功刷 Owner consumer 号,拿到新 access_token(expires_in=3599,scope 6 项) |
| 二 loadCodeAssist | v1internal:loadCodeAssist | ideType=ANTIGRAVITY 被接受;顶层返回 `cloudaicompanionProject`(已有 project,走直返分支);`paidTier.availableCredits[].creditType` 证实 GOOGLE_ONE_AI credit 体系 |
| 三 generateContent | v1internal:generateContent | 注入 `enabledCreditTypes:["GOOGLE_ONE_AI"]` 的 envelope 被接受;返回 `{"response":{…}}` 外壳;模型按提示真回文本;usageMetadata 真实(promptTokenCount=7/candidatesTokenCount=1) |

### 逐项车道假设 → 真账号验证

- **G1 端点/UA/credits**:出站 `cloudcode-pa v1internal:generateContent` + 静态
  UA `antigravity/hub/2.2.1 darwin/arm64` + `enabledCreditTypes:["GOOGLE_ONE_AI"]`
  全部被真上游接受并正常计费。
- **G2 响应映射**:真上游返回顶层 `{"response":…}` 外壳,正是 geminicodeassist
  unwrap 的前提;antigravity_session 复用该 unwrap 正确。
- **G3 project_id**:`projectIDFromNode` 首选字段 `cloudaicompanionProject` 与真响应
  字段名逐字一致(Owner 号已分配 project,走 loadCodeAssist 直返,未触发 onboardUser 分支)。
- **G4 内置公开凭据**:内置 client_id/secret 是有效的公开 wire 值,能真实完成
  refresh_token grant;车道默认 env-gate 仍关。
- **G5 导入解析**:Owner token 文件结构 `{token:{access_token,token_type,refresh_token,
  expiry},auth_method:consumer}` 与解析器扁平化逻辑逐字吻合。

## 亲检门禁(独立复跑,不采信 codex 报告)

- gofmt / git diff --check / go build ./... / go vet ./... 全 exit 0。
- go test -count=1 ./...(全量)+ codebudget 全绿。
- 五个判别测试变异全部真红:G2 改回 openai.Adapter 红、G1 删 GOOGLE_ONE_AI credits 红、
  G5 退回不扁平化红、**不回归证**(共用 CodeAssistAdapter 若被污染默认注入 credits →
  Gemini Code Assist 的「默认不含 credits」断言红)、G4 无条件注册 → 默认注册数 32→33 红。
- 变异后 cp 备份全量还原,干净基线 6 包重跑全绿,无变异残留。
- 共用车道不回归:CodeAssistAdapter 仅新增三个可选字段(UserAgent/APIClient/
  EnabledCreditTypes),`omitempty` + 空值回退保证默认 Gemini 请求形态 byte 不变。
- import 环解耦无行为回归:gemini/antigravity refresher 从 `credentialworker.Classify*`
  改引 `auth.Classify*`,而 credentialworker 的对应符号本就是 auth 的纯转发/别名。

## 已知边界(非阻塞,标注后续)

- **ProjectResolver 尚未挂到真实入池链路**:project_id 获取流已建 + 单测覆盖 + 真账号
  验证字段正确,但未接线到 credential 采集/物化流程(不擅自碰 auth core / Finalizer);
  首次入池把 project_id 持久化进 credential payload 的接线留后续切片。
- **X-Goog-Api-Client 值为温和占位**:`google-genai-sdk/1.0 gl-go/1.0` 是功能性头,
  真上游未拒;非精确客户端指纹(温和伪装姿态,不做设备指纹/关联规避)。
- **env-gate 默认关**:激活的是代码路径能力,生产默认行为未翻转(翻 on 属 Owner-gated)。
- **未触 money/quota/schema**:GOOGLE_ONE_AI credits 计费/额度语义 + 403 封号态建模
  留 Owner-gated 后续。

## 判断

Antigravity 反转车道 G1-G5 **已用 Owner 真账号端到端验证通过**:内置公开客户端能刷号、
cloudcode-pa 认 antigravity 身份、project_id 字段猜测经真响应逐字坐实、generateContent
真通且计费真实。这是「闭环之后实测」这一关对 Antigravity 的实质通过。车道默认关,部署方
opt-in 后即可用真 consumer 号反转出 Cloud Code 上游。
