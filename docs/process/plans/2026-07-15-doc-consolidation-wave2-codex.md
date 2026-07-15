# 2026-07-15 文档归并后续波（Codex 独立计划）

> 本计划由 Codex 独立起草。起草前未读取任何同主题 Claude 计划；不得把本文件冒充 Claude × Codex 综合稿。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “判文档是否过期，一律以代码为准、必须真读实现代码判定”；“该删就删，自主执行”；“发现文档与代码不一致/有问题的，单独标出来、分类归档”。 |
| Scope | 本波只处理 manifest 中 `frontend`、`observability-logging`、`deployment` 三个领域的 `HISTORICAL-DELETE`、`SUPERSEDED` 与 `NEEDS-CODE-VERIFY` 项；建立主索引骨架、三个领域 SSOT、删除日志和 DRIFT 表。只改文档并用 `git rm` 删除经实现核验的散文档；不改任何 `.go`、`.rs`、`.tsx` 实现，不 push。 |
| Out of scope | trust-chain 信任链族、`docs/research/**`、`docs/decompositions/**`、`docs/architecture/egress-tls-mimicry-SSOT.md`；本波不处理其他领域，不修改 `LICENSE`、数据库 schema、认证/计费/配额核心、部署脚本或生产配置。工作树中既有未跟踪文件 `docs/process/plans/2026-07-15-hermes-deployment-architecture-codex.md` 不纳入、不修改。 |
| Success criteria | 三个领域的每个候选文档均有逐份判定；凡涉及实现现状的判定均能回指亲读的实现 `file:line` 与必要调用链；过期/错误散文档分批 `git rm` 且删除日志完备；不一致项进入 DRIFT；形成三个领域 SSOT 并挂入 `PROJECT-SSOT-INDEX.md`；保护边界零改动；文档链接/表格与 git diff 校验通过；未提交变更经过 Codex review。 |
| Time estimate | 预计墙钟 4–7 小时；主要 agent 时间用于逐份读取约 90 份 manifest 条目、提取关键断言、阅读实现及追调用链。若候选实际跨域或断言过密，本波按完整证据优先缩到两个领域，不为凑数量降低真实性。 |
| Blast radius | 只影响文档导航与历史散文档的工作树可见性；误删会导致设计/运维上下文暂时从当前树消失，但 git history 可恢复。错误 SSOT 可能误导后续实现和运维，因此证据不足时必须保留并标 `待核`，不能猜。 |
| Failure modes | 见下表。 |
| Decision points | Owner 已明确授权凭代码证据自主删除，本波不逐份请示。遇到疑似 Owner-gated/缓做项，必须以风险登记或 DR 为准并保留；遇到“代码疑似缺陷”只登记 DRIFT，不改代码，留给 Claude/Owner 决定；若发现保护族交叉引用，只调整新索引指向，不删除保护文件。 |

## 失败模式与缓解

| 失败模式 | 缓解措施 |
| --- | --- |
| 把 grep 命中误当实现证据 | `rg` 只列清单和定位；最终判定前用带行号的整段读取打开实现，阅读条件分支、错误路径、调用方和装配点；删除日志不接受只有搜索结果的证据。 |
| 计划文档描述的是未来态，却被误判为过期 | 先检查 `docs/10_RISK_REGISTER.md`、相关 DR、综合计划及 Owner gate；明确决策的未来态标 `CURRENT（决策/路线图）`，SSOT 分开写“已实现”和“已决定未实现”。 |
| 只看前端组件而漏掉 API 接线或后端路由 | 从页面/路由入口追到 API client，再追 OpenAPI/后端 route 与 handler；反向抽查后端已挂管理端点是否有前端入口。 |
| 只看指标声明而漏掉实际采集/暴露 | 从计数/观测调用追到 collector/store，再追 `/metrics` 或管理 API 装配及前端消费；测试只能辅助，不能替代生产逻辑。 |
| 部署说明与模板、启动门或 embed 链不一致 | 联读 Compose/Dockerfile/启动入口、配置解析、迁移/首启逻辑、前端 embed 与健康检查调用链；不修改部署脚本。 |
| 删除后产生大量断链 | 删除前记录入链；删除后用仓库内链接扫描和 `rg` 反查路径，能安全改为 SSOT 的引用随本波修正，无法确认的登记 DRIFT。 |
| 误碰受保护或他人未提交文件 | 删除候选与保护路径做显式交集检查；每批 `git diff --name-status` 复核；不加入或修改既有 Hermes 未跟踪计划。 |
| 行号随本波编辑漂移 | 证据只引用实现代码，本波不改实现，因此行号稳定；SSOT 写明核验日期和 commit。 |

## 前置检查清单

1. 确认分支为 `feat/ui-density-overview`，记录 `HEAD`，检查工作树并隔离既有改动。
2. 确认活跃 goal 正文与 Owner 更新一致；读取 `docs/RULES.md`、项目简报、功能映射、风险登记与 agent 工作流。
3. 独立保存本 Codex 计划后，才允许查找同主题 Claude 草案；若存在则做差异表并等待/使用 Owner 批准的综合稿。若不存在，不伪造 Claude 意见。
4. 从 manifest 精确抽出三个领域的全部条目及初始状态，排除 `docs/research/**`、`docs/decompositions/**`、trust-chain 与已定稿出口 SSOT。
5. 建立逐文档核验台账：路径、关键断言、实现入口、调用链、代码证据、决策证据、最终处置。
6. 记录 `GOCACHE=/home/ubuntu/HUAKAI/.gocache` 与 `GOFLAGS=-buildvcs=false`；本波无代码改动，但需要运行 Go 侧只读测试或命令时沿用该环境。

## 具体执行顺序

### A. frontend

1. 读取前端构建入口、router、页面注册、权限/导航、API client 与状态管理生产代码。
2. 对 manifest 的每份前端文档提取可证伪关键断言；逐项追到 `.tsx/.ts` 生产逻辑，涉及服务能力时继续追 OpenAPI、Go route、handler 与装配。
3. 将“当前实现”“已决定但未实现”“仅历史执行记录”分开；旧 prompt/轮次计划若已被真实实现或明确后继完全吸收，删除并记证据。
4. 产出 `docs/architecture/frontend-SSOT.md`，包含真实页面树、权限边界、接线状态、构建/嵌入方式、已知漂移与代码证据。

### B. observability-logging

1. 读取日志初始化、请求日志/脱敏、指标采集、Prometheus 暴露、告警/审计查询及前端观察面生产调用链。
2. 逐份核验可观测文档，区分已接线指标、只定义未采集、只采集未暴露、已决定路线图。
3. 删除被当前实现/综合 SSOT 完全取代的过程文档；冲突进入 DRIFT。
4. 产出 `docs/architecture/observability-logging-SSOT.md`。

### C. deployment

1. 读取 Dockerfile、Compose、示例环境配置、Go 启动入口与配置解析、数据库迁移/首启、健康检查、前端 embed/静态资源服务生产链。
2. 逐份核验部署文档的命令、端口、服务拓扑、必需环境变量、启动顺序、健康/恢复断言。
3. 历史一次性执行计划与当前脚本/启动逻辑冲突或已完全完成者删除；运维风险或代码疑似缺陷进入 DRIFT。
4. 产出 `docs/architecture/deployment-SSOT.md`；不改任何部署脚本。

### D. 归并与校验

1. 建立/更新 `docs/architecture/DOC-CONSOLIDATION-DELETION-LOG.md`，每项写“文件 → 删除理由 → 亲读代码 `file:line` → 判定日期/基线 commit”。
2. 建立/更新 `docs/architecture/DOC-CODE-DRIFT.md`，严格使用 Owner 指定五列；“代码疑似缺陷”单独可筛选。
3. 建立 `docs/architecture/PROJECT-SSOT-INDEX.md` 骨架，挂三个领域 SSOT，列保护族与待处理领域，不把未核领域写成已完成。
4. 分领域执行 `git rm`，每批后复核保护边界与入链；必要的低风险文档引用改指向 SSOT。
5. 校验表格、相对链接、重复路径、删除日志覆盖率、DRIFT 证据格式和 `git diff --check`；检查工作树未误纳既有 Hermes 计划。
6. 暂存仅本波文件，运行仓库规定的 `codex exec review --uncommitted --full-auto --sandbox read-only`；归一化发现，修复 S0/S1，S2/S3 记录。
7. 若检查与 review 均通过，可按领域形成小型本地 commit（不 push）；若不 commit，则在报告中明确工作树状态。

## 预期交付

- `docs/architecture/PROJECT-SSOT-INDEX.md`
- `docs/architecture/frontend-SSOT.md`
- `docs/architecture/observability-logging-SSOT.md`
- `docs/architecture/deployment-SSOT.md`
- `docs/architecture/DOC-CONSOLIDATION-DELETION-LOG.md`
- `docs/architecture/DOC-CODE-DRIFT.md`
- 三个领域经实现证据支持的 `git rm` 批次
- 中文 Owner 报告：目标正文、领域/删除计数、删除摘要、DRIFT 数量与 TOP、SSOT/主索引状态、功能/clean-room/安全风险、需 Claude/Owner 跟进项
