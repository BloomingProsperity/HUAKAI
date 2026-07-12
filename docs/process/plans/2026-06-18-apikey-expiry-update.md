# Plan — 用户自助 API-key expiry 更新写路径 (inert-gap 切片)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner 全权自主实现+合并)
- 基线: origin/feat/frontend-portal @ 1324caf3
- 分支: feat/frontend-apikey-expiry-update

## 背景 + 真 inert gap 核实 (禁止凭记忆)

`PATCH /v1/api-keys/{id}` 用户自助改自己 key 的 name/status,但**改不了 expires_at** —— key 寿命创建后
永不能延长/调整,只能 revoke + 重建(丢失 key 明文)。补 expires_at 进 PATCH。三要素齐全确认是真 inert gap
(非死开关):
- **存储✓**: api_keys.expires_at 列已存在,创建时可设(userkey.go INSERT @userkey.go Issue 路径), 未来校验
  ErrInvalidExpiry @userkey.go:227。List/Get 都读出 KeyDescriptor.ExpiresAt(userkey.go:113, 365-368)。
- **消费者✓ (真行为效果)**: 鉴权时拒过期 key —— api_key_resolver.go:155 `if row.ExpiresAt.Valid &&
  !row.ExpiresAt.Time.After(now)` → 映射 HTTP 401。改 expires_at 直接改 key 的有效期。
- **缺口**: PatchRequest(userkey.go:627-634)仅 Name/Status *string;Patch fn(userkey.go:649-736)动态 UPDATE
  只拼 name/status;handler patchRequest(handlers.go:228-231)只 decode name/status 且 DisallowUnknownFields ——
  现在发 expires_at 反被 400 拒。零写路径。

非 money(key 生命周期, userkey 包只 import internal/db, 零 billing/quota/credit 引用 —— grep 证实),非 auth-core
(只动 key 元数据写,不碰 resolver/login/2FA),非避让(userkey 域与 proxies 分支 pool/credential/provider 不相交)。
migration-free(列已存在)。

## #16 三镜像研究 (clean-room specifier lane, 已完成)

### 首引 recency/liveness 核验 (#12, 核验于 2026-06-18 UTC)
- **Wei-Shaw/sub2api @ e34ad2b**: archived=false, disabled=false, pushed_at=2026-06-18T06:37Z (90d 内活跃);
  HEAD 提交 2026-06-10 "chore: sync VERSION to 0.1.136 [skip ci]"。生产码(backend/internal/...)非仅 tests。
- **QuantumNous/new-api @ 1ac0f58**: archived=false, disabled=false, pushed_at=2026-06-18T09:15Z;
  HEAD 提交 2026-06-13 "feat(audit): add authentication method tracking in audit logs"。生产码(model/controller)。
- **router-for-me/CLIProxyAPI @ 2a050dc**: archived=false, disabled=false, pushed_at=2026-06-18T08:05Z;
  HEAD 提交 2026-06-14 "feat: enhance fault tolerance for kv-based caching and introduce additional tests"。生产码(internal/)。

「已建 API-key/token 的 expiry 更新 + clear-vs-unchanged 三态」:
- **sub2api@e34ad2b (默认 tiebreaker, 最成熟)**: api_keys 有可空 wall-clock timestamp 列,NULL=永不过期(无 sentinel)
  (ent/schema/api_key.go:73-76)。**有更新路径**(PUT /keys/:id 用户自助, service api_key_service.go:589)。
  **三态机制(金标准)**: 线上 DTO 是可空字符串指针 —— absent/null=不改、空串""=清成永不过期、非空 RFC3339=设新值
  (handler api_key_handler.go:225-238);service 层归一成 (timestamp 指针 + 独立内部 clear 布尔 json:"-")
  (api_key_service.go:181-182),apply 时 clear 优先→置 NULL,else if timestamp 非 nil→覆盖,else 不动
  (api_key_service.go:589-601)。**不重载单一 nullable 字段**——因 JSON null 已被"不改"占用,故用空串作"清"的带内信号。
  线格式:更新用绝对 RFC3339 串(空串=清);创建另用一个相对天数字段(不对称)(handler/api_key_handler.go:39)。scope=用户自助,归属强制。
  过去时刻:**允许**(无未来校验,被两 agent 标为 UX gap)。
- **new-api@1ac0f58**: token 的 deadline 列是 int64 epoch 秒,默认值用 -1 作"永不过期" sentinel(model/token.go:22, 199)。
  **有更新路径**(PUT /token/, controller/token.go:294)但是**全对象 PUT**(GORM Select 强制写 deadline 列, model/token.go:286)。
  **无真三态**:set=正 epoch、clear=送 -1 sentinel、"不改"非一等公民(客户端须回送当前值;漏送→0=epoch-zero=过去→
  **静默废 key**, 危险脚枪)。scope=用户自助。过去时刻:允许。
- **CLIProxyAPI@2a050dc**: **no-equivalent(已证)** —— client key 是扁平 opaque 静态配置串,集合成员校验,零生命周期字段
  (internal/access/config_access/provider.go:41-45,88-101);唯一 "expiry" 是上游 OAuth token 刷新(厂商授予,不可编辑)。

### 取舍 (sub2api 默认 tiebreaker + HUAKAI 升级 delta)
两真镜像在三态上**分叉**:sub2api 干净三态(可空字符串指针 + 内部 clear 布尔)vs new-api 重载单 sentinel + 全对象 PUT
(带"漏送=静默废 key"脚枪)。**默认 sub2api**(且明显更安全)。

| 维度 | sub2api 镜像 | new-api 镜像 | HUAKAI delta | dimension |
|---|---|---|---|---|
| 三态编码 | 可空字符串指针 absent/null=不改、""=清、RFC3339=设 | 全对象 PUT + -1 sentinel | 复刻 sub2api 干净三态, 贴 HUAKAI 既有 PATCH-指针惯例(Name/Status *string) | 架构(契约面) |
| 过去时刻校验 | 允许(UX gap) | 允许(+漏送静默废 key) | **拒过去时刻**(复用自家 create 的 ErrInvalidExpiry@userkey.go:227), 关掉两镜像都有的静默废 key 脚枪 | 生态(UX/安全) |
| 永不过期编码 | NULL | -1 sentinel | NULL(HUAKAI 列已是 nullable timestamptz, 无 sentinel) | 架构(存储) |

- HUAKAI 三态落地: 线上 `patchRequest.ExpiresAt *string`(stdlib: 缺/null→nil 指针=不改;指向""=清;指向 RFC3339=设,
  解析失败→400 invalid_expires_at);service `PatchRequest` 加 `ExpiresAt *time.Time` + `ClearExpiry bool`(镜像
  sub2api 的 value+clear 分离);Patch apply: ClearExpiry→`expires_at=NULL`,else if ExpiresAt!=nil→未来校验后 `expires_at=$n`,
  else 不动。

## 实现范围 (success criteria)
- 后端 store userkey.go: PatchRequest +ExpiresAt *time.Time +ClearExpiry bool;PatchResult +ExpiresAt *time.Time;
  Patch fn —— no-op 短路扩成含 expiry 三态判断、未来校验(req.ExpiresAt!=nil && !After(now)→ErrInvalidExpiry)、动态 UPDATE
  加 expires_at 子句(clear→NULL / set→$n)、RETURNING 加 expires_at 扫描入 PatchResult。no-op fetch 路径回填 ExpiresAt。
- 后端 handler handlers.go: patchRequest +ExpiresAt *string;newPatchHandler 解析三态(""→clear / RFC3339→set /
  解析失败→400 invalid_expires_at);patchResponse +ExpiresAt *time.Time(json omitempty,nil=永不过期不输出)。
- 前端 apiKeys.ts: UpdateApiKeyRequest 类型 + updateApiKey(id, body) 走 userPatch。
- 前端 api-key-expiry-form.ts(新): 纯 builder buildApiKeyExpiryPatch(action, when?) 三态编码 + validate(RFC3339/未来)
  + 错误码映射 + test + package.json 脚本。
- OpenAPI: PATCH /v1/api-keys/{id} requestBody +expires_at(string, ""=clear, RFC3339=set, 省略=不改)+ response +expires_at。

强测试(变异验证): handler(set/clear/unchanged 三态解析 + 解析失败→400 + 不触达 service)+ store 集成(integration_pg:
set 未来→Get 反映 / clear ""→Get 为 nil / past→ErrInvalidExpiry / no-op 回填)+ 前端(builder 三态 + validate + 接线)。

## blast radius
- userkey.go(PatchRequest/PatchResult/Patch 3 处)+ handlers.go(patchRequest/handler/patchResponse 3 处)+ 前端 2 文件
  + openapi.yaml + 测试。**resolver/鉴权读侧不改**(已 live 消费 expires_at)。**schema 不改**(列已存在)。
  无新 endpoint(扩既有 PATCH)。无 money 无避让。

## 门禁
codex 401 → ultracode 对抗审查(#8 替代门禁)零 S0/S1 → 重跑干净基线 fail 0(含 cmd/gateway OpenAPI 一致性)→
squash 合并 → ff main。
