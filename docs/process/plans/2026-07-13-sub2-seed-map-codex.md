# 2026-07-13 sub2 测试数据映射 SQL（Codex 独立计划）

> 状态：独立草案，禁止据此执行。Codex 未读取任何同主题 Claude 计划；必须先与 Claude 独立草案交叉讨论、形成合并计划并由 Owner 批准。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “写一份 sub2api → HUAKAI 的数据映射 SQL 脚本，把 sub2 真实数据转成 HUAKAI schema，用于测试与给 UI 铺开提供真实行……你只写 SQL 文件，不执行。” |
| Scope | **范围内：**只读核查指定的 sub2 DDL、`huakai_seed` 的 `public`/`sub2` 元数据、HUAKAI API key 哈希写入实现；生成 `scripts/sub2-seed/map.sql`；覆盖 tenants、users、user_balances、api_keys、pool_groups、provider_accounts，并对 user_subscriptions 做 best-effort；加入幂等策略、序列修正和 parity 校验 SQL。**范围外：**执行 SQL、修改任何数据库、修改 Go、修改迁移、修改认证兼容逻辑、提交 commit、访问 `huakai`/`huakai_dev`/生产库、实现真实凭证加密或二期 provider 业务语义。 |
| Success criteria | 只新增目标 SQL 文件及本计划文件；SQL 以单事务组织且可重复运行；目标数据库保护明确；依赖顺序与 FK 可静态解释；users 保留源 ID 并修正序列；余额映射可校验预期总额 `200000164.99321872`；API key 哈希和前缀严格依据 HUAKAI 代码证据；provider/channel 缺种子时采用计划批准的安全策略；末尾含源/目标行数和余额 parity 查询；所有注释与 Owner 汇报为中文；没有执行 SQL。 |
| Time estimate | Owner 批准合并计划后，约 45–90 分钟墙钟时间；单个 Codex 会话约 60–120 分钟代理时间，取决于目标表约束复杂度和 provider/channel 种子状态。 |
| Blast radius | 计划阶段仅新增文档。执行阶段只新增一个 SQL 脚本，不执行时数据库爆炸半径为零；若 Owner 后续手工执行，脚本的清理策略可能删除 `huakai_seed.public` 对应表的既有测试行，因此必须有数据库身份门和显式测试库提示。错误的 FK 顺序、冲突键、序列名、哈希格式或占位 provider/channel 会导致导入失败或 UI 数据失真。 |
| Failure modes | 见下文“失败模式与缓解”。 |
| Decision points | 1. 是否明确批准为了数据迁移而在 SQL 中引用必要的 sub2 表/列名，作为 clean-room 的窄化操作例外；2. Claude/Codex 计划冲突的取舍；3. provider/channel 无种子时选择“创建最小占位行”还是“跳过 provider_accounts”；4. 是否允许脚本对目标表执行 `TRUNCATE`，还是采用按固定租户清理/UPSERT；5. 如 `pgcrypto` 不存在，采用确定性占位 hash 还是跳过 api_keys。 |
| Pre-execution checklist | 1. Claude 独立产出同主题计划；2. 对两份计划列出一致点、冲突和遗漏；3. Owner 批准合并计划及 clean-room 窄化例外；4. 确认当前连接目标只能是 `huakai_seed`；5. 只读检查目标 DDL、约束、序列、扩展和种子；6. 只读检查 HUAKAI API key 哈希实现；7. 只读检查指定 sub2 DDL；8. 写 SQL；9. 仅做文本/语法级静态复核，不连接执行。 |

## Clean-room lane guard

以下护栏约束批准后的源码读取与行为归纳。SQL 为完成 Owner 明确要求所必需的源字段引用，仍需 Owner 对上述窄化例外单独确认；不得借此复制上游结构到 HUAKAI 生产设计。

```text
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
```

## 独立技术方案

### 1. 只读发现阶段

1. 先从 HUAKAI 后端代码定位 API key 创建、更新和校验链，确认：规范化输入、摘要算法、编码格式、前缀长度以及数据库实际写入值。只接受代码中可追溯的真实证据，不以“大概率”为实现依据。
2. 使用只读 `psql` 元命令或 `information_schema` 查询 `huakai_seed`：确认目标列类型、默认值、CHECK、UNIQUE、FK、序列、`pgcrypto` 可用性及 providers/channels 种子。命令必须显式指定 `-d huakai_seed`；不得运行 DML/DDL。
3. 读取 Owner 指定的 sub2 DDL，只提取完成映射所必需的类型、空值性、约束和源列；不复制注释、函数、迁移顺序或无关结构。
4. 对实际源数据仅做必要的只读聚合/枚举检查：各表行数、role/status 值域、空值、ID 范围、余额合计、provider 类型/status 值域。若 Owner 不希望读取数据值，则退化为仅 DDL 映射，并在 SQL 中用 CASE/default 防御。

### 2. SQL 结构与数据库保护

1. 文件头用中文列出目标、建议执行命令、测试级捷径、风险和 parity 点，并写明绝不用于生产。
2. 单事务：`BEGIN` → 目标库身份断言 → 按 FK 依赖清理/UPSERT → 映射插入 → 序列修正 → 可执行 parity 查询 → `COMMIT`。
3. 数据库身份保护优先使用匿名块检查 `current_database() = 'huakai_seed'`，不写死任何其他数据库名；不使用 `\connect`。
4. 幂等策略优先“固定导入租户 + 稳定主键/唯一键 + `ON CONFLICT DO UPDATE`/按导入租户精确删除”，避免无条件清空 134 表环境中的其他测试数据。只有目标 FK 拓扑证明安全且 Owner 明确选择后才使用 `TRUNCATE`。
5. 清理顺序从叶表到父表，插入顺序相反；对超出本切片、但引用目标行的 FK 做静态检查，避免脚本重跑被外部测试行阻断。

### 3. 映射顺序

1. **tenants：**解析真实必填列。确保固定默认租户存在；若 ID=1 已被非导入租户占用，脚本不得静默覆盖，应失败并给出可诊断消息，或按合并计划采用稳定自然键取得 tenant ID。
2. **users：**保留源 ID；严格映射 role/status；display_name 为空时由 email 前缀生成；bcrypt 串原样进入目标 password_hash；password_version 使用目标默认或明确安全测试值；时间戳按目标类型传递。插入完成后用实际序列名执行安全 `setval`，正确处理空表和 `is_called`。
3. **user_balances：**按用户一一生成；数值显式转为目标精度；balance、held、version 按 directive 映射；parity 查询使用精确 numeric 比较，预期合计为 `200000164.99321872`。
4. **api_keys：**只有在确认 HUAKAI 实现和 `pgcrypto` 后才写真实 digest 表达式；前缀长度取代码证据；其他列按真实约束转换。若扩展不可用，不在脚本中自行 `CREATE EXTENSION`，因为这会扩大权限/环境影响；按 Owner 选择使用明确不可认证的确定性占位值或跳过，并在头注释与 parity 输出中标记。
5. **pool_groups：**只映射 UI 渲染和目标 NOT NULL 所需字段；其余使用 HUAKAI 默认。源定价/路由专属字段不猜测、不伪造。
6. **provider_accounts：**先验证 provider/channel 目标结构和种子。如果已有兼容种子，构造确定性映射；如果没有，默认建议跳过账号导入并输出原因，因为创建占位 provider/channel 可能要求猜测较多业务必填列。若 Owner 选择占位方案，只创建最小、明显标记为测试的行；不写 account_credentials，不伪造 KEK/密文。
7. **user_subscriptions：**先检查是否存在语义足够接近的目标表与必填关系；无法真实映射时以中文注释说明跳过，不影响前六项。

### 4. 静态验证（绝不执行 SQL）

1. 检查脚本只引用 `public` 和 `sub2`，且无 `huakai`/`huakai_dev`/生产连接文本。
2. 核对每个 INSERT 的列数、类型转换、默认值、冲突目标和 FK 顺序。
3. 核对所有测试级捷径都在文件头集中列出，并在相应语句附近再次标记。
4. 核对末尾 parity SQL 至少覆盖 tenants、users、user_balances、api_keys、pool_groups、provider_accounts、user_subscriptions 的源/目标行数或明确跳过状态，以及用户余额合计。
5. 不运行 `psql -f`、不执行事务、不 commit；最终报告列出预期行数，无法从既有事实确定的行数明确写“待只读查询确认”，不得猜测。

## 失败模式与缓解

- **误连非测试库：**脚本内用 `current_database()` 强制中止，建议执行命令显式 `-d huakai_seed`。
- **清理扩大影响：**优先按稳定导入租户/主键精确清理；是否 `TRUNCATE` 交 Owner 决策。
- **ID/唯一键冲突：**发现同 ID 非导入数据时中止，不静默覆盖；冲突策略逐表按真实约束制定。
- **FK 拓扑不完整：**从目录元数据读取入向/出向 FK，按依赖排序；外部引用存在时避免粗暴清理。
- **序列修正错误：**从 `pg_get_serial_sequence` 或真实默认表达式确认序列，不猜名称。
- **哈希方案误判：**必须以 HUAKAI 写入/校验代码双向证据确认；扩展缺失不自动安装。
- **bcrypt 无法登录：**原样保留只服务数据展示；脚本头明确“认证可能失败”，不修改认证代码。
- **provider 占位污染语义：**默认建议无种子则跳过；若批准占位，使用醒目标识且仅限测试库。
- **凭证不可用：**不伪造密文或 KEK；provider_accounts 仅用于展示，明确不能用于真实上游调用。
- **源/目标时间或 numeric 类型不兼容：**显式转换，异常值归入可诊断失败或保守默认，不静默截断。
- **clean-room 边界冲突：**只引用执行映射不可避免的源表/列名，不搬运完整上游 DDL、注释、函数或结构设计；必须先获 Owner 对该窄化例外的书面批准。

## 交叉讨论时必须比较的要点

1. Claude 与 Codex 对幂等策略（TRUNCATE、精确删除、UPSERT）的不同风险判断。
2. 数据库身份断言方式和是否允许脚本携带可执行 parity 查询。
3. provider/channel 无种子时跳过还是创建占位。
4. `pgcrypto` 缺失时 api_keys 的降级方案。
5. 源 ID 与目标现有测试数据冲突时的处理。
6. user_subscriptions 的“语义足够接近”判定标准。
7. clean-room 字段名窄化例外及其边界。

Source files read: none（本计划阶段未读取参考项目源码或源 DDL）
Lane: specifier
Agent: GPT-5 Codex /root
UTC timestamp: 2026-07-13T03:44:06Z
