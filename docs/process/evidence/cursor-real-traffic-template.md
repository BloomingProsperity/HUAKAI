# Cursor IDE 真实流量证据采集模板 — C0 Slice

> **本模板由 Claude PM 写,Owner 本机抓真流量后填写并产出 sanitized summary**。
> 模板本身不含任何 secret material;Owner 填写的 evidence-actual 副本 **应放 gitignored 位置**,
> 仅提交 sanitized summary 进库(去 token/cookie/session 后)。
>
> 流程:
> 1. Owner 拷贝本模板到 `docs/process/evidence/cursor-real-traffic-evidence.md`
>    (此路径需加进 `.gitignore`,或 Owner 本机 work tree 之外)
> 2. Owner 抓真流量 + 填模板
> 3. Owner 产出 `docs/process/evidence/cursor-real-traffic-summary.md`
>    (本仓库可提交版本,**已 redact**,只有事实摘要)
> 4. Claude PM 用 summary 文件驱动 C2/C3/C4

## 1. OAuth 端点采集

抓 Cursor IDE 启动登录流程的浏览器流量。需要的字段:

| 字段 | 现状假设(synthesis) | Owner 抓到的真值 | sanitized 摘要(进仓库的部分) |
| --- | --- | --- | --- |
| `auth_url` | 待定,operator-config | [FILL] | OK 公开,可摘要 |
| `token_url` | 待定,operator-config | [FILL] | OK 公开,可摘要 |
| `client_id` | 待定,operator-config | [FILL] | OK 公开,可摘要 |
| `scope`(若有) | 待定 | [FILL] | OK 公开 |
| `redirect_uri` 模式 | `http://127.0.0.1:1455/auth/callback` | [FILL] | OK 公开 |
| OAuth flow 类型 | PKCE authorization_code | [FILL: PKCE / device-code / 其他?] | OK 公开 |
| PKCE method | S256 | [FILL] | OK 公开 |

## 2. session token 行为验证(C2 关键)

`refresher.go:255-256` 现在写:
```go
cred["access_token"] = accessToken
cred["session_token"] = accessToken
```
假设是 OAuth `access_token` 可直接当作 session token 用于 `api2.cursor.sh` 出站。
**需要 Owner 用真账号 verify 这个假设**:

| 测试 | 方法 | 结果 | 结论 |
| --- | --- | --- | --- |
| 1. 用 OAuth `access_token` 直接 POST `api2.cursor.sh/aiserver.v1.AiService/StreamChat` Authorization: Bearer | curl + 真 token | [FILL: 200 / 401 / 4xx] | [PASS/FAIL] |
| 2. 用 cookie `WorkosCursorSessionToken` 走同 endpoint | curl + 真 cookie | [FILL] | [PASS/FAIL] |
| 3. 同时带两者 | curl | [FILL] | [对比] |

**若 test 1 失败 / test 2 成功** → C2 必须修 `refresher.go:256`:
- 移除 `cred["session_token"] = accessToken`
- 用 OAuth flow 额外拿 cookie(可能需扩展 OAuth scope 或额外 endpoint),写入 `cred["cookie"]`
- cursor_session.go BuildRequest 在 cookie 模式下不要发 Authorization Bearer

## 3. OCAW 反封禁 header 真实样本(C3 关键)

Owner 本机用真 Cursor IDE 打开 → 网络面板 → 任一 StreamChat 请求 → 复制所有 request header。

### 3.1 `x-amzn-trace-id`
样本 3 个不同会话:
- 会话 1: [FILL,只填 prefix `Root=1-XXXXXXXX-` 即可,后面 24 hex 可 truncate 不写]
- 会话 2: [FILL]
- 会话 3: [FILL]

观察(prose 描述):
- 是否每个 request 一个新值?
- 是否客户端启动时固定?
- 包含本机 fingerprint 吗?

### 3.2 `x-cursor-checksum`
**这是反封禁核心,处理需特别小心**。
- 是否每个 request 都不同?(prose 描述)
- 是否绑定 request body? (test:同 body 不同 client → checksum 同/异)
- 是否绑定 endpoint path? (同 body 不同 path → 同/异)
- 长度 + 字符集 模式:[FILL: e.g. 64 hex / base64url 等]
- **不要把真实 checksum 字面值写进 summary 文件**;只描述 shape

### 3.3 `x-cursor-request-id`
- 格式:[FILL: UUID v4? 自定义?]
- 是否客户端单调?或随机?

### 3.4 `x-cursor-timezone`
- 格式:[FILL: IANA `Asia/Shanghai` ? UTC offset ?]
- 与本机 OS 时区关系

### 3.5 `x-cursor-client-version`
- 实际版本:[FILL: e.g. `0.43.6`]
- 是否带 build hash?

### 3.6 其他 header
列出 Owner 抓到的所有非标准 Cursor header(去 secret 后):
- [FILL]

## 4. Cookie 行为

如果 Cursor 用 cookie session:
- `WorkosCursorSessionToken` 是否必须?(prose 描述,不写值)
- 还有哪些 cookie? [FILL 名字列表]
- 过期时间 / refresh 机制

## 5. wire schema 探查(C4 关键)

抓 `api2.cursor.sh/aiserver.v1.AiService/StreamChat` 请求 body bytes。

### 5.1 上行(client → server)payload
- 编码类型:`application/connect+proto` 已知
- protobuf field numbers 观察(用 `protoc --decode_raw` 或 `xxd`):
  - 推测 model: field [FILL]
  - 推测 messages: field [FILL]
  - 推测 stream flag: field [FILL]
  - 推测 tools/functions: field [FILL]
  - 其他字段:[FILL]

### 5.2 下行(server → client)stream chunks
- 帧分隔:Connect-RPC 标准帧?自定义?
- 推测 content delta: field [FILL]
- 推测 finish_reason: field [FILL]
- 推测 usage: field [FILL]

### 5.3 wire schema 来源合法性

**严格要求**: 不读 Cursor 客户端 proprietary 代码;只看自己账号 + 自己 IDE 流量 wire bytes,这是黑盒行为采集,**不是反编译客户端**。如果要从 client 二进制提取 .proto descriptor,**Owner 必须法务前置确认**。

## 6. ToS / EULA 关键条款摘要

Owner 读 Cursor IDE EULA / ToS,摘出:
- 是否禁第三方代理出站?[FILL: 是 / 否 / 未明示]
- 是否禁 token refresh 自动化?[FILL]
- 是否禁多账号共享 IDE 配额?[FILL]
- 是否禁逆向客户端?[FILL]
- ToS URL + 抓取日期 + 关键条款引用(不引全文)

## 7. C0 验收闸

Owner 填完后 **summary 进仓库前** 必须确认:
- [ ] 无 `Bearer ` + 后跟非占位符 token
- [ ] 无 `WorkosCursorSessionToken=` + 后跟非占位符 cookie
- [ ] 无任何 32-char 以上 base64 / hex 看起来像 secret
- [ ] 无 `access_token`/`refresh_token`/`session_token` 字段的 value(只允许字段名 + shape 描述)
- [ ] ToS 条款引用 ≤ 100 字符(避免 fair-use 边界)

## 8. C0 后续切片决策依据

填完后,以下决策点的依据应清晰:

| 决策点 | 依据来自 | 状态 |
| --- | --- | --- |
| D-3 OCAW 策略 strict / dev | §3 checksum shape + ToS §6 | [PENDING Owner 填写] |
| D-4 wire schema 来源合法性 | §5.3 + §6 | [PENDING] |
| C2 refresher.go:256 修正方向 | §2 test 结果 | [PENDING] |
| C3 signer 接口字段集 | §3 各 header 真实形态 | [PENDING] |
| C4 是否能用 protobuf wire reverse 不读客户端代码 | §5 wire bytes + §6 ToS | [PENDING] |

---

**模板写者**: Claude PM (Opus 4.7)
**预计 Owner 填写工时**: 0.5–1 天(IDE 抓 + 法务读 + sanitize summary)
**模板版本**: 1.0 (2026-05-26)
