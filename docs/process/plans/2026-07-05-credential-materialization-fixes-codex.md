# 2026-07-05 credential 物化三处修 Codex 计划

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| Owner directive | "credential 物化 3 处修(F-1 Bedrock region 白名单 + F-2 legacy 回填健康 subject + F-3 google_sa legacy 路径)"；"禁止 git commit/push"；"禁改 internal/gateway/、internal/gatewayhttp/、internal/channelhealth/、internal/credentialacq/、cmd/gateway/account_slot*"；"注释、报告中文"；"每处配判别测试+§14 变异证红(cp 备份)"；"先亲读三镜" |
| --- | --- |
| Scope | 只改 `internal/provider/bedrock/passthrough.go`、Bedrock/provider vault 测试、必要时 `internal/provider/postgres_vault.go`。不改 schema、不改 gateway/gatewayhttp/channelhealth/credentialacq、不 commit/push。 |
| Success criteria | F-1 非空合法 AWS region 通过，含 `.`、`@`、内网/路径形态等非法 region fail-closed；F-2 legacy fallback 产出的 `AccountInfo.AccountCredentialID` 与 `CredentialVersion` 非零，满足健康 subject；F-3 legacy `service_account` 不再产空 `Value` 的可转发凭据，而是明确 fail-closed。指定门禁全绿或如实记录阻塞。 |
| Time estimate | 约 1.5-2.5 小时墙钟；Codex 实作、变异、门禁各一轮。 |
| Blast radius | Bedrock aws_sigv4/upstream_passthrough 的 endpoint 构造；PostgresCredentialVault legacy fallback 的 AccountInfo；legacy service_account 解析。v2 credentialstore 正常路径不改变。 |
| Failure modes | AWS 合法 region 被误拒：用官方形态 `us-east-1`、`ap-northeast-1` 单测覆盖；legacy subject 与 v2 id 混淆：不读取 `account_credentials` 时只用稳定 legacy 账号行 id 和版本 1；service_account 误作可转发：改为 `ErrCredentialFormat` fail-closed 并测试错误。 |
| Decision points | 无需 Owner 中途确认：不触高风险文件、不改 schema、不新增依赖、不删除文件。若门禁暴露需要 schema/auth/gateway 改动，则停下报告。 |
| Pre-execution checklist | 1. 已读 `AGENTS.md`、`docs/RULES.md`、clean-room 技能。2. 已确认目标文件无 live lock 并声明 `.coordination` 锁。3. 已亲读三镜相关区域。4. 已确认本地 Vertex 白名单形态可作为本仓实现风格。5. 编辑前保留用户已有 dirty diff，不回滚。 |

## 三镜对照

- new-api：Bedrock 路径把凭据串拆出的 region 交给 AWS SDK 或直接参与 Bedrock host 拼接，但读取片段未看到字符白名单；同仓 Vertex 渠道要求部署地区配置存在且可解析，并要求含默认地区键，说明 region/location 是运营配置而非客户请求自由输入。引用：`new-api@8874d1929f97bb3f7fcae2af81c9e114535044f1:relay/channel/aws/relay-aws.go:64`、`new-api@8874d1929f97bb3f7fcae2af81c9e114535044f1:relay/channel/aws/adaptor.go:91`、`new-api@8874d1929f97bb3f7fcae2af81c9e114535044f1:controller/channel.go:487`。
- new-api：Vertex service-account 正常路径会读取服务账号 JSON，生成访问令牌后再转发；这支持 HUAKAI legacy `service_account` 不能产空主值继续走普通 OAuthAccessToken adapter。引用：`new-api@8874d1929f97bb3f7fcae2af81c9e114535044f1:relay/channel/vertex/service_account.go:40`、`new-api@8874d1929f97bb3f7fcae2af81c9e114535044f1:relay/channel/vertex/adaptor.go:128`。
- CLIProxyAPI：Vertex service-account 作为专门导入/存储形态，携带 project/location/email 与原始服务账号内容；运行时由专门 executor 用服务账号换 token 后请求 Vertex，而不是把空主值 credential 交给通用转发。引用：`CLIProxyAPI@9e9c244250795fe441b9e6c443d9abf07f6257f0:internal/cmd/vertex_import.go:45`、`CLIProxyAPI@9e9c244250795fe441b9e6c443d9abf07f6257f0:internal/auth/vertex/vertex_credentials.go:15`、`CLIProxyAPI@9e9c244250795fe441b9e6c443d9abf07f6257f0:internal/runtime/executor/gemini_vertex_executor.go:306`、`CLIProxyAPI@9e9c244250795fe441b9e6c443d9abf07f6257f0:internal/runtime/executor/gemini_vertex_executor.go:1098`。
- sub2api：Bedrock region 有默认值并参与模型区域前缀和 URL 构造，但读取片段未看到字符白名单；Vertex service-account 由专门 token provider 取得 access token，且 URL 构造使用账号上的 project/location。引用：`sub2api@87dfc66132c6af2ee464668fdd15209884b0d335:backend/internal/service/bedrock_request.go:17`、`sub2api@87dfc66132c6af2ee464668fdd15209884b0d335:backend/internal/service/bedrock_request.go:73`、`sub2api@87dfc66132c6af2ee464668fdd15209884b0d335:backend/internal/service/gemini_token_provider.go:52`、`sub2api@87dfc66132c6af2ee464668fdd15209884b0d335:backend/internal/service/vertex_service_account.go:60`。

## 执行顺序

1. F-1：在 Bedrock adapter 增加 `aws_region` 字符白名单/形态校验，拒绝点号、斜杠、冒号、`@`、空白、大写、首尾连字符等；合法示例保留。
2. F-1 测试：扩展 `internal/provider/bedrock/passthrough_test.go`，断言合法 region 成功且非法 region 返回错误；测试里同时证明若无校验，非法 region 会进入 host。
3. F-2：legacy fallback `AccountInfo` 补稳定 subject：`AccountCredentialID=provider_accounts.id`、`CredentialVersion=1`。依据：legacy 表无 `account_credentials.id/credential_version`，但有账号行主键和旧 `token_version`；为避免和 v2 版本语义混淆，本次不使用 token_version。
4. F-2 测试：更新/新增 legacy fallback 测试，要求 subject 非零，并用本地等价 `healthKeyOK` 断言；变异删字段会红。
5. F-3：将 legacy `service_account` 改为明确 `ErrCredentialFormat`，避免空 `Value` credential 静默进入转发；v2 `gemini/vertex_sa` handler 保持不变。
6. F-3 测试：新增 unit 测试断言 `mapServiceAccount` fail-closed；保留 v2 credentialstore 对 `vertex_sa` 正常物化测试。
7. 变异证红：用 `cp` 备份目标文件，分别删/绕过 F-1、F-2、F-3 关键守卫跑最小测试，确认红，再恢复备份。
8. 门禁：运行 `go build ./... && go vet ./...`，以及指定 `go test` 包。

Source files read: new-api relay/channel/aws/adaptor.go; new-api relay/channel/aws/relay-aws.go; new-api controller/channel.go; new-api relay/channel/vertex/adaptor.go; new-api relay/channel/vertex/service_account.go; CLIProxyAPI internal/cmd/vertex_import.go; CLIProxyAPI internal/auth/vertex/vertex_credentials.go; CLIProxyAPI internal/runtime/executor/gemini_vertex_executor.go; sub2api backend/internal/service/bedrock_request.go; sub2api backend/internal/service/gemini_token_provider.go; sub2api backend/internal/service/vertex_service_account.go
Lane: specifier
Agent: GPT-5 Codex session
UTC timestamp: 2026-07-05T14:35:00Z

---

## Claude PM 验收裁定(2026-07-05)

- **F-1 保留**:白名单+trim+fail-closed 亲核通过;变异抽查(删校验→TestRejectInvalidRegion 红)复验通过。
- **F-2 回退**:回填 provider_accounts.id 方案否决。亲核 0022 建表:account_credential_id 强外键 REFERENCES account_credentials(id) + uq_channel_health_credential_version(account_credential_id, credential_version) 全局唯一(不带 tenant)——回填值不存在则健康落库外键违约,恰好存在则串写其他凭据健康行(S3 盲区变 S2 污染)。禁 schema 下无合法回填值;正解(legacy 补建 v2 凭据行/放宽外键)触 schema=Owner-gated。codex 报告自flag的 FK 疑虑经核实成立。spec 中"回填稳定标识"兜底建议本身有缺陷(Claude 的 spec 责任,非 codex 执行偏差)。
- **F-3 保留**:亲核全仓生产代码零消费 Extra["auth_kind"]/google_sa,fail-closed 正确。
- dr001 跨租户隔离负向断言未被改弱(仅 legacy 断言适配,随 F-2 一并回退)。
- 三套门全绿:codebudget ok / quality-gate PASS(staticcheck 94, deadcode 879)/ 全量 go test 0 失败。
