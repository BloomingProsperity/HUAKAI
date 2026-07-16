# 2026-07-16 账号接入与运营联动基础修复（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “那你全部修复啊。你看下c这些文件是不是有人在动”；此前已要求所有修复作为 PR 提交，未经 Owner 同意不得并入主线。 |
| Scope | 在独立分支 `fix/account-intake-ops-20260716-codex` 完成第一批后端基础修复：统一账号接入 dry-run 合同、账号运营聚合合同、现有 bulk-by-tag 逐账号原子执行与完整结果，并闭环 review 发现的批内去重顺序和生产 selector 软冷却豁免接线。同步判别测试、OpenAPI 和最少必要文档。 |
| Out of scope | 本批不修改 selector/PASR、Gemini 出站、官方定价、R4 harness、前端页面、数据库 schema、资金账、配额、鉴权核心、真实凭据或生产部署；不直接启用 Claude Cookie、Setup Token、Agent Identity、CRS 或秘密迁移包。上述能力保留为后续独立 PR，不算取消。 |
| Success criteria | 账号接入核心可在零写库、零网络请求条件下得到稳定的 create/update/skip/conflict/fail 预检结果，且结果不包含秘密；在租户 grant 未落地前不挂 HTTP 入口。运维可从一个账号合同看到真实阻断来源、状态来源、恢复时间和允许动作；批量操作中单账号 update 与 audit 同成同败，一个账号失败不阻断后续账号；目标测试、相关包测试、代码预算、`git diff --check` 和只读 Codex review 通过；提交 Draft PR，不合并。 |
| Time estimate | 本批约 5-8 小时；后续高风险能力按独立 PR 推进。 |
| Blast radius | 影响管理端账号接入与账号运营 API、对应 store 边界和测试；不改变数据面转发、selector、计费、鉴权或数据库结构。 |
| Failure modes | 预检误把多份凭据绑定到同一账号；响应泄漏 token/cookie/private key；运营聚合自创状态而与 selector 真相漂移；bulk 只做到部分 update 却没有审计；为了整批原子持有长事务；与其他工作树正在修改的 wiring/OpenAPI 文件冲突。 |
| Mitigations | 预检保持纯函数和窄 inventory 接口；秘密仅产不可逆摘要；所有状态标注权威来源及 selector 是否消费；单账号事务、整批继续执行；判别测试覆盖中间失败与后续继续；避免修改正在被 selector、Gemini、定价、R4 和前端审计工作树占用的文件，必要合同先放独立增量文档。 |
| Decision points | Owner 已确认：租户 grant 未落地前，账号接入 dry-run 只保留纯核心和测试，不开放 HTTP；不能让部署者代任意租户处理，也不能默认授权全部租户管理员。后续 Claude Cookie 换码、Setup Token 正式启用、Agent Identity 私钥、CRS 出站、秘密迁移包、三身份 grant 持久化、`last_request_observed_at` schema 及跨协议 selector 接线，必须分别提交“现状、参考行为、HUAKAI 方案、优缺点、迁移与回滚”给 Owner 确认。 |
| Parallel-plan status | Owner 明确要求 Codex 独立工作并与其他目标分开。本计划继承已有 Codex 独立方案，不读取或修改其他 agent 的计划与未提交工作。 |

## 当前冲突矩阵

| 正在活动的工作树 | 已观察到的活动范围 | 本批处理 |
| --- | --- | --- |
| `HUAKAI-wt-selector-chain` | `cmd/gateway/wiring.go`、selector、PASR、rate precheck、queue wait | 不修改这些文件；运营聚合只读取现有权威合同 |
| `HUAKAI-wt-gemstream` / `HUAKAI-wt-r4integ` | Gemini passthrough、R4 harness、部分集成文件 | 不修改 Gemini 和 R4 文件 |
| `HUAKAI-wt-pricing` | 官方模型定价 migration 与 pricing 代码 | 不修改 schema 与定价 |
| `HUAKAI-frontend-truth-audit-20260716-codex` | OpenAPI、openapicheck、前端真相审计 | 该工作已停止；本批仅同步自身新增 operations 路径与 bulk 响应，不改前端页面或其它合同 |
| `HUAKAI-wt-loader` | 真实账号加载器 | 不读取或修改其未提交内容 |

## Pre-execution checklist

1. 以 `origin/integration/r4-test@6083b22d` 创建独立干净工作树。
2. 确认本工作树无其他目标的未提交改动。
3. 完整读取账号 bulk handler/store、凭据接入 parser/finalizer、账号详情、channel health、credential、auth/model cooldown 的生产源码与测试。
4. 在编辑实现前列出准确文件范围，避开冲突矩阵中的活动文件。
5. 所有 Go 注释与测试注释使用中文，生产代码注释不出现借鉴项目名。
6. 不使用 `/tmp`；测试临时目录统一使用 `/home/ubuntu/.codex-tmp/account-intake-ops`。
7. 不新增 runtime dependency，不修改 schema、鉴权、资金或真实秘密。
8. 运行目标单测、相关包测试、代码预算和 `git diff --check`。
9. 只暂存本批差异，运行只读 Codex review；S0/S1 修复后最多再审一轮。
10. 中文提交并推送 Draft PR；未经 Owner 同意不合并。

## 执行中边界修正

`cmd/gateway` 的 OpenAPI 一致性测试发现 operations 路由已注册但合同缺失，形成 `impl_only=1`。因此本批必须同步 OpenAPI 路径、响应 schema、bulk 新增字段和 1000 条上限错误，并增加 method/schema 判别测试；否则实现不能通过启动接线门，不能把该缺口推迟为后续集成。

首轮只读 review 另发现两处产品正确性问题：失败凭据提前占用去重键，以及 `disable_cooling` 运营语义与生产候选查询不一致。前者调整为仅可执行项登记去重；后者只在真实生产 `pool_group` 查询中豁免 `throttled/cooldown`，不豁免 `revoked`，并同步 operations 信号。旧 channel 查询没有携带该开关到后续 gate，本批不制造假接线。

## 执行顺序

1. 源码核实并确定现有接口可复用边界。
2. 实现统一账号接入 dry-run 领域合同与 parser 适配。
3. 实现账号运营聚合领域合同及只读管理端入口。
4. 修复 bulk-by-tag 的逐账号原子性和完整逐项结果。
5. 补正常、失败、恢复、秘密不回显及租户边界判别测试。
6. 运行检查、只读 review、提交并创建 Draft PR。

## 账号身份冲突成熟项目对照增补

| 项目 | 内容 |
| --- | --- |
| Owner directive | “如果库里已经有两条同一上游 ID，它还会随便取第一条更新。我要把这两种都收紧为显式冲突，要求人工消歧，避免自动写错账号。参考成熟项目！” |
| Lane | `specifier`，只读 `sub2api`、`new-api`、`CLIProxyAPI` 当前默认分支源码；本会话不承担同一对照产物的 reviewer lane。 |
| Scope | 核实账号导入/创建时使用什么身份键，重复身份是拒绝、覆盖、更新、合并还是允许并存，批量导入怎样报告单项冲突，以及上游是否依赖数据库唯一约束。 |
| Success criteria | 每个成熟项目的结论都带当前可达 commit SHA 与源码行号；明确区分观察、推断和 open question；HUAKAI 只采用独立设计的行为合同和判别测试。 |
| Blast radius | 对照本身只读；若证据表明当前 `identity_ambiguous` 或邮箱冲突策略不合适，仅调整 intake 纯计划和测试，不开放 HTTP、不写库。 |
| Failure modes | 把成熟项目的内部命名、schema 或算法顺序带回本仓库；只看搜索结果不读上下文；把“没找到”误写成“明确不支持”；把单账号更新策略误套到批量导入。 |
| Mitigations | 三镜像均更新 HEAD；逐段阅读入口、服务层、持久化调用和测试；输出只写行为；未观察到的能力列入 open question。 |

### Clean-room lane guard

- PRIOR LANES ON THIS ARTIFACT: none
- REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api
- 禁止复制函数名、结构字段名、注释、schema、测试和逐行算法。
- 证据仅以 `<repo>@<sha>:<file>:<line>` 锚点记录，正文使用 HUAKAI 自身词汇。

## 后续批次，不静默缩水

1. Claude Setup Token 一等接入与统一 gate。
2. Claude Cookie 一次性换码 Safe Equivalent。
3. Codex 账号级批量执行与 Agent Identity Experimental。
4. CRS 受控连接器与安全账号迁移包。
5. 主动探测、`last_request_observed_at`、跨协议统一链路。
6. 三身份单层租户 grant 持久化与授权操作面。
