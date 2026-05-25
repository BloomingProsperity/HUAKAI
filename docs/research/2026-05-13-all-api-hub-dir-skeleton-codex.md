# 2026-05-13 all-api-hub 目录骨架深挖（Codex lane）

| 字段 | 值 |
|---|---|
| Ref | all-api-hub |
| 本地路径 | `~/refs/all-api-hub/` |
| Evidence SHA | `893e832d0f92` |
| Last commit date | 2026-05-09 |
| Lane | codex / specifier |
| 范围 | T1 顶层目录骨架，不做跨 ref 对比，不读其他 ref |
| Clean-room | 只记录行为、边界、风险与 HUAKAI 升级方向；不搬运源码、命名体系、算法顺序或目录结构 |

## 0. 读法与总体判断

- 读法：先用 `find ~/refs/all-api-hub -maxdepth 2 -type d` 与 `ls -la <dir>` 建骨架，再只对入口、服务、测试、文档、CI 等少数文件做 `nl -ba | sed -n '1,120p'`。
- 目录观察：顶层包含 agent 提示、CI、文档、文档助手、E2E、OpenSpec、开发插件、发布脚本、主扩展代码和测试目录。
- 项目类型：这是 WXT + React + TypeScript 浏览器扩展，manifest 入口、权限、public 目录和构建输出由 WXT 配置承载（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:11）。
- 产品面：根 README 描述其核心是多站点 AI 中转站账号、余额、模型、密钥、签到、网页 API 测试、渠道同步与备份管理（qixing-jk/all-api-hub@893e832d0f92:README.md:10）。
- 运行入口：后台入口注册 runtime 消息、临时窗口、cookie 拦截和菜单，并在安装、更新、启动时恢复服务（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/index.ts:52）。
- UI 入口：options 页用 hash 菜单、侧栏、搜索和懒加载 pane 组织管理页面（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/options/App.tsx:39）。
- Popup/sidepanel：弹窗把账号、书签、独立凭证分成 tab，并复用账号管理 provider（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/popup/App.tsx:22）。
- 服务面：长期后台服务包括账号刷新、签到、用量历史、模型同步、通知、站点公告、发布更新、WebDAV 等（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/servicesInit.ts:51）。
- 风险：文档页标明 AGPL-3.0；HUAKAI 只能把它当行为证据，不可复制实现与目录结构（qixing-jk/all-api-hub@893e832d0f92:docs/docs/README.md:48）。

## 1. 根目录配置与项目入口（非目录，但决定整体骨架）

1. 用途
- 根目录把扩展构建、类型检查、测试、E2E、i18n、发布包和浏览器差异配置集中起来。
- 它不是业务目录，但决定所有一级目录如何接入构建、测试、发布和 manifest。

2. 关键文件
- `package.json`: 168 LoC；定义 WXT dev/build、三浏览器构建、zip、compile、lint、knip、Vitest、Playwright、Husky 入口（qixing-jk/all-api-hub@893e832d0f92:package.json:12）。
- `wxt.config.ts`: 153 LoC；声明 `src` / `src/public`、manifest 权限、optional permissions、browser-specific settings、commands 和 Vite dev 插件（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:12）。
- `vitest.config.ts`: 102 LoC；把测试拆成 DOM 与 Node 两类项目，并设覆盖率门槛（qixing-jk/all-api-hub@893e832d0f92:vitest.config.ts:25）。
- `playwright.config.ts`: 38 LoC；E2E 指向 `e2e`，仅跑 Chromium，CI 下有 retry、report、trace/video 策略（qixing-jk/all-api-hub@893e832d0f92:playwright.config.ts:9）。
- `tsconfig.json`: 33 LoC；严格 TS、WXT alias、DOM lib、no emit，并继承 WXT 生成配置（qixing-jk/all-api-hub@893e832d0f92:tsconfig.json:2）。

3. 入口 / 调用关系
- `package.json` 的 build/test 脚本调用 WXT、Vitest、Playwright、lint 与 i18n 工具链（qixing-jk/all-api-hub@893e832d0f92:package.json:13）。
- WXT manifest 把浏览器权限、host permissions、侧边栏/浏览器 action command 注入扩展包（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:25）。
- CI 和 Husky 复用这些脚本，所以根目录配置是本项目所有目录的执行网关。

4. 核心 logic / 算法
- 构建算法是“同一源码，多浏览器目标”：Chrome/Edge 走 MV3 语义，Firefox/Safari 分支在 manifest 与脚本中分叉。
- 质量算法是“本地门禁 + CI 分层”：staged 校验、push 校验、unit coverage、E2E smoke 分别守不同失败面。
- 权限算法是“默认基础权限 + 浏览器差异 optional permissions”，把敏感能力推迟到用户确认。

5. 暴露功能
- 用户看见的是一个可安装的跨浏览器扩展。
- 贡献者看见的是一组统一命令：开发、构建、测试、打包、检查 i18n、E2E。
- Operator 看见的是通过 release workflow 产出 Chrome/Firefox/Safari 包的流水线。

6. HUAKAI 升级点
- 架构升级：HUAKAI 可借鉴“root orchestration manifest”，把 backend、admin ops、docs、acceptance tests 的入口放进统一 task registry，而不是散落 README。
- 安全升级：把高权限能力做成 optional capability gate，并在 UI/运行时留“权限状态快照”。
- 生态升级：保留多目标构建矩阵，但 HUAKAI 应输出 gateway/admin/account-hub 三类 artifact，而非复制扩展目录形状。

## 2. `.claude/`

1. 用途
- 该目录承载 Claude slash command 与 OpenSpec workflow skill，面向 agent 驱动的规格化开发。
- 它不是产品 runtime，但体现 ref 项目的“先 spec 后实施”协作路径。

2. 关键文件
- `.claude/commands/opsx/apply.md`: 命令入口；与同名 skill 对应。
- `.claude/commands/opsx/explore.md`: 探索入口；用于变更上下文发现。
- `.claude/commands/opsx/verify.md`: 验证入口；用于变更完成前检查。
- `.claude/skills/openspec-apply-change/SKILL.md`: 156 LoC；说明选择 change、读取状态、读取上下文、逐任务实施（qixing-jk/all-api-hub@893e832d0f92:.claude/skills/openspec-apply-change/SKILL.md:16）。
- `.claude/skills/openspec-sync-specs/SKILL.md`: OpenSpec 同步技能；从目录名看负责 spec 同步。

3. 入口 / 调用关系
- Slash command 提示会引导 agent 调 OpenSpec CLI，再读取 proposal/spec/design/tasks 等上下文（qixing-jk/all-api-hub@893e832d0f92:.claude/skills/openspec-apply-change/SKILL.md:27）。
- 技能要求实现时按 pending task 循环，并把任务完成状态写回 tasks 文件（qixing-jk/all-api-hub@893e832d0f92:.claude/skills/openspec-apply-change/SKILL.md:67）。
- 这条链路间接影响 `openspec/`、`src/`、`tests/`，但自身不直接进入扩展包。

4. 核心 logic / 算法
- Agent workflow 算法是“选择变更 -> 读 schema/status -> 读指令 -> 读上下文 -> 小步实施 -> 回写进度”。
- 它把 agent 行为约束成可复核流程，降低“跳过规格直接改代码”的风险。
- 暂停条件包括任务不清、设计问题、错误或用户中断，体现人机交接边界。

5. 暴露功能
- 维护者可以通过命令让 agent 开始、继续、验证或归档一个 OpenSpec change。
- 项目获得一种轻量 PM/agent 协作层。
- 新贡献者可以从命令名理解变更生命周期。

6. HUAKAI 升级点
- 架构升级：HUAKAI 的 PM-Orchestrator 可保留“change 状态机”，但把风险门禁扩展到 billing/quota/auth 等高风险域。
- 生态升级：把 agent prompt 与 review template 分层，避免产品实现目录被 agent 工作流污染。
- 安全升级：agent workflow 应强制列出数据、权限、迁移、money-path 风险，而不只读 OpenSpec task。

## 3. `.codex/`

1. 用途
- 该目录承载 Codex prompt 版本的 OpenSpec 操作命令，与 `.claude/` 的 workflow 形成平行 agent 入口。
- 它说明 ref 项目有多 agent toolchain 适配，而不是只绑定单一助手。

2. 关键文件
- `.codex/prompts/opsx-apply.md`: 150 LoC；Codex 版实施 prompt，步骤和 Claude skill 基本同构（qixing-jk/all-api-hub@893e832d0f92:.codex/prompts/opsx-apply.md:10）。
- `.codex/prompts/opsx-new.md`: 新建变更 prompt；从命名看负责创建 change。
- `.codex/prompts/opsx-continue.md`: 继续变更 prompt；从命名看负责恢复上下文。
- `.codex/prompts/opsx-sync.md`: 同步 prompt；用于 specs 同步。
- `.codex/prompts/opsx-verify.md`: 验证 prompt；用于变更检查。

3. 入口 / 调用关系
- Codex prompt 先选择 change，再读取 OpenSpec status 和 apply instructions（qixing-jk/all-api-hub@893e832d0f92:.codex/prompts/opsx-apply.md:21）。
- Prompt 明确读取 `contextFiles`，然后展示 progress 与 remaining tasks（qixing-jk/all-api-hub@893e832d0f92:.codex/prompts/opsx-apply.md:46）。
- 它与 `openspec/` 目录强耦合，但不应该直接定义产品行为。

4. 核心 logic / 算法
- 平行 agent 算法：把同一 OpenSpec 生命周期翻译成不同助手可执行的提示。
- 进度算法：任务从 unchecked 到 checked，需要持续汇报整体完成度。
- 阻塞算法：当设计或任务不清时暂停，而不是静默猜测。

5. 暴露功能
- Codex 可以按统一规格变更流程工作。
- Owner 可以让不同 agent 对同一 change 执行相似流程，减少口径漂移。
- 维护者可以把 prompt 当成操作文档。

6. HUAKAI 升级点
- 架构升级：HUAKAI 可把 Codex prompt 抽成 repo 内“agent operating contract”，并与 review/plan/cross-discuss 规则联动。
- 安全升级：在 Codex prompt 模板前置 clean-room lane guard，尤其当 change 涉及 non-MIT ref evidence。
- 生态升级：同一 work unit 应有 `plan-codex`、`plan-claude`、`synthesis` 三段 artifact。

## 4. `.github/`

1. 用途
- 该目录管理 issue templates、composite action 和多条 CI/CD workflow。
- 它覆盖测试、E2E、PR build、release build、docs deploy、translation、nightly 与 store publish。

2. 关键文件
- `.github/workflows/test.yml`: 120+ LoC；按路径触发质量 job 和 unit tests（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/test.yml:3）。
- `.github/workflows/e2e-smoke.yml`: 97 LoC；构建扩展后跑 Playwright smoke，并上传失败产物（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/e2e-smoke.yml:40）。
- `.github/workflows/build-and-publish.yml`: 385 LoC；支持 release/manual，含 dry-run、store review/publish 参数（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/build-and-publish.yml:3）。
- `.github/actions/package-extension/action.yml`: 102 LoC；复合 action 封装 setup/install/package/collect artifacts（qixing-jk/all-api-hub@893e832d0f92:.github/actions/package-extension/action.yml:35）。
- `.github/workflows/deploy-docs.yml`: docs 发布 workflow；从目录名看对接 `docs/`。

3. 入口 / 调用关系
- push/PR 根据 touched paths 触发质量和单测，直接覆盖 `src/`、`tests/`、config 与 workflow 本身（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/test.yml:4）。
- E2E workflow 在构建测试包后安装 Chromium 并执行 Playwright（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/e2e-smoke.yml:67）。
- Release workflow 先确定 tag/branch/commit，再 checkout 具体 ref 并构建（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/build-and-publish.yml:58）。

4. 核心 logic / 算法
- CI 算法是“路径过滤 + 并发取消 + 分层 job”。
- Release 算法是“来源 ref 与发布 tag 分离”，避免 branch/commit 构建被误当成稳定 release。
- Packaging 算法要求 Chrome、Firefox、source 三类 zip 各唯一，否则失败。

5. 暴露功能
- 贡献者提交代码后自动获得 type/lint/knip/i18n/unit coverage 反馈。
- PR 或 push 可跑扩展 E2E smoke。
- 维护者可以 dry-run 构建发布包，也可以选择是否提交 Chrome/Edge/Firefox 审核。

6. HUAKAI 升级点
- 架构升级：HUAKAI 应把 gateway/admin/account-hub 的 CI 拆成 contract/unit/e2e/release 四层，而不是一条大流水线。
- 安全升级：release workflow 对生产发布、store publish、secret use 应加环境审批和 artifact attestation。
- 生态升级：加入 “spec coverage -> acceptance tests -> release gate” job，把文档规格和测试覆盖串起来。

## 5. `.husky/`

1. 用途
- 该目录提供本地提交/推送前门禁，防止明显坏改进入远端。
- 它与 `package.json` 的 `validate:staged`、`validate:push` 脚本直接相连。

2. 关键文件
- `.husky/pre-commit`: 8 LoC；运行 staged validation，失败时中止（qixing-jk/all-api-hub@893e832d0f92:.husky/pre-commit:1）。
- `.husky/pre-push`: 8 LoC；运行 push validation，失败时中止（qixing-jk/all-api-hub@893e832d0f92:.husky/pre-push:1）。
- `.husky/README.md`: 约 2.2 KB；说明 hook 使用。

3. 入口 / 调用关系
- Git commit 触发 staged validation；该脚本在 `package.json` 中包含 lint-staged 与 staged i18n check（qixing-jk/all-api-hub@893e832d0f92:package.json:32）。
- Git push 触发 compile 和 knip（qixing-jk/all-api-hub@893e832d0f92:package.json:33）。
- Hook 不直接读业务数据，只调用 repo 标准命令。

4. 核心 logic / 算法
- 本地 gate 算法是“越早越轻”：commit 做格式/局部检查，push 做类型与死代码检查。
- i18n staged check 只在 relevant staged path 命中时执行，降低无关提交成本（qixing-jk/all-api-hub@893e832d0f92:scripts/run-i18n-check-if-staged.mjs:37）。

5. 暴露功能
- 贡献者本地能更早发现格式、lint、i18n、类型和未用代码问题。
- 维护者减少 CI 资源浪费。

6. HUAKAI 升级点
- 架构升级：HUAKAI 可把本地 gate 改成 `contracts affected`、`acceptance affected`、`docs link check` 三类。
- 安全升级：pre-push 可扫描 secret placeholder、migration destructive markers、billing ledger mutation tests。
- 生态升级：Codex review 结果可变成 commit message trailer 或 pre-commit artifact，但不要硬依赖联网。

## 6. `assets/`

1. 用途
- 顶层 `assets/` 只放一个图标文件，偏发布/展示素材。
- 与 `src/assets/` 不同，顶层素材更像仓库级展示资产。

2. 关键文件
- `assets/icon.png`: 二进制图片；仓库级图标。
- `src/assets/icon.png`: README 使用的产品 logo 源（qixing-jk/all-api-hub@893e832d0f92:README.md:6）。
- `src/public/_locales/en/messages.json`: 20 LoC；manifest 字符串暴露扩展名称、描述、命令和 context menu 文案（qixing-jk/all-api-hub@893e832d0f92:src/public/_locales/en/messages.json:1）。

3. 入口 / 调用关系
- README 直接引用 `src/assets/icon.png` 展示 logo（qixing-jk/all-api-hub@893e832d0f92:README.md:6）。
- WXT 配置启用 auto-icons 和 `src/public`，实际扩展 icon/locales 更靠 `src/public` 与 WXT 产物（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:17）。
- 顶层 `assets/` 未在已读入口中观察到 runtime import。

4. 核心 logic / 算法
- 素材目录无算法，主要承担品牌识别。
- 真正的 manifest 本地化文案由 `src/public/_locales` 管理，和构建工具绑定。

5. 暴露功能
- 用户在 README、商店、扩展 manifest 和 docs 中看到统一品牌。
- Operator 发布时可复用图标资产。

6. HUAKAI 升级点
- 架构升级：HUAKAI 应区分 product brand assets、admin UI assets、docs assets、release assets。
- 安全升级：发布图标、截图、docs 图应纳入 release asset checksum，避免供应链替换。
- 生态升级：为各 edition 生成可追溯品牌包，而不是散放二进制素材。

## 7. `docs/`

1. 用途
- 该目录是 VuePress 文档站，提供用户上手、账号/凭证、统计、自动化、生态、站长工具、安全与同步文档。
- 它面向最终用户与 operator，和产品功能一一对应。

2. 关键文件
- `docs/package.json`: 27 LoC；VuePress dev/build/check-links/check 脚本（qixing-jk/all-api-hub@893e832d0f92:docs/package.json:11）。
- `docs/docs/README.md`: 97 LoC；文档首页，列出用户角色与功能入口（qixing-jk/all-api-hub@893e832d0f92:docs/docs/README.md:57）。
- `docs/docs/.vuepress/config.js`: 316 LoC；多语言、navbar/sidebar、sitemap、theme 配置（qixing-jk/all-api-hub@893e832d0f92:docs/docs/.vuepress/config.js:18）。
- `docs/docs/account-management.md`: 用户账号管理文档。
- `docs/docs/webdav-sync.md`: WebDAV 同步文档。

3. 入口 / 调用关系
- Docs package 调 VuePress dev/build，并有 link check（qixing-jk/all-api-hub@893e832d0f92:docs/package.json:12）。
- VuePress config 按语言注册首页、快速上手、账号与凭证、统计、自动化、生态、站长工具、安全等 sidebar（qixing-jk/all-api-hub@893e832d0f92:docs/docs/.vuepress/config.js:49）。
- README/doc homepage 把普通用户、进阶玩家、站点管理员拆成不同路径（qixing-jk/all-api-hub@893e832d0f92:docs/docs/README.md:57）。

4. 核心 logic / 算法
- 文档信息架构按用户角色和任务域分层。
- 多语言算法由 VuePress locales + 目录化 markdown 实现。
- Docs 与产品功能同步程度高，目录可反推功能面。

5. 暴露功能
- 用户可查看安装、权限、账号、密钥、模型列表、签到、WebDAV、支持站点、站长管理等文档。
- Operator 有公开文档站与 sitemap。
- 贡献者可用 docs check 验证文档构建与链接。

6. HUAKAI 升级点
- 架构升级：HUAKAI docs 应按 Owner/operator/developer/auditor 四类角色组织。
- 生态升级：为 Admin Ops 增加 runbook、incident drill、billing dispute、quota exhaustion、provider outage 文档。
- 安全升级：所有涉及 key、cookie、backup 的文档必须标明本地/服务端存储边界和审计行为。

## 8. `docs_assistant/`

1. 用途
- 该目录是文档自动化助手，用 Python 更新贡献者/赞助商、发布日志、翻译等内容。
- 它不是扩展 runtime，而是 docs lifecycle automation。

2. 关键文件
- `docs_assistant/main.py`: 101 LoC；长轮询式更新贡献者与发布日志（qixing-jk/all-api-hub@893e832d0f92:docs_assistant/main.py:18）。
- `docs_assistant/translate.py`: 756 LoC；README 描述其调用 OpenAI-compatible 接口做翻译并保留 Markdown 结构（qixing-jk/all-api-hub@893e832d0f92:docs_assistant/README.md:51）。
- `docs_assistant/changelog.py`: 约 8.5 KB；发布日志更新。
- `docs_assistant/contributors.py`: 约 17.6 KB；贡献者/赞助商更新。
- `docs_assistant/Dockerfile`: 212 bytes；把助手容器化。

3. 入口 / 调用关系
- README 要求配置 OpenAI-compatible base URL、key、model 和 retry 参数（qixing-jk/all-api-hub@893e832d0f92:docs_assistant/README.md:17）。
- 主循环按不同 interval 触发贡献者和 release 文档更新（qixing-jk/all-api-hub@893e832d0f92:docs_assistant/main.py:31）。
- 翻译工具读取中文源文件，输出 en/ja 目录（qixing-jk/all-api-hub@893e832d0f92:docs_assistant/README.md:53）。

4. 核心 logic / 算法
- 文档更新算法是“独立周期 + 失败重试 + 最小/最大 sleep clamp”。
- 翻译算法是“源文档 -> 模型翻译 -> 保留 Markdown -> 写多语言目录”。
- 它把 LLM 用作文档运营工具，而不是产品 gateway runtime。

5. 暴露功能
- 维护者能自动同步 contributors、sponsors、release notes。
- 文档可半自动生成英文/日文版本。
- Dockerfile 提供部署为常驻 updater 的可能。

6. HUAKAI 升级点
- 架构升级：HUAKAI 可建立 docs ops worker，但要接入审计队列和人工 review，不让 LLM 自动改权威合规文本。
- 安全升级：LLM 翻译 worker 不应读取 secrets、billing ledger、auth docs 的敏感源；所有输出要 diff review。
- 生态升级：把 release notes、scenario docs、acceptance test docs 生成拆成独立 pipelines。

## 9. `e2e/`

1. 用途
- 该目录是 Playwright 扩展端到端测试，覆盖账号、密钥、书签、自动签到、popup 凭证、lazy loading、更新日志等用户流。
- 它验证浏览器扩展真实入口，而不只验证 service/unit。

2. 关键文件
- `e2e/accountManagementCommonFlows.spec.ts`: 688 LoC；seed storage、打开 options、验证账号列表/刷新/排序等通用流（qixing-jk/all-api-hub@893e832d0f92:e2e/accountManagementCommonFlows.spec.ts:41）。
- `e2e/keyManagementCommonFlow.spec.ts`: 约 8 KB；账号 token 管理流，含 site route stub（qixing-jk/all-api-hub@893e832d0f92:e2e/keyManagementCommonFlow.spec.ts:83）。
- `e2e/popupApiCredentialProfiles.spec.ts`: 约 7.9 KB；popup 创建与验证独立凭证（qixing-jk/all-api-hub@893e832d0f92:e2e/popupApiCredentialProfiles.spec.ts:36）。
- `e2e/lazyEntryLoading.spec.ts`: 211 LoC；验证 popup/options 懒加载 chunk 行为（qixing-jk/all-api-hub@893e832d0f92:e2e/lazyEntryLoading.spec.ts:27）。
- `e2e/utils/commonUserFlows.ts`: helper；统一 locale、seed storage、stub model metadata，并对 console/page error 设 guard（qixing-jk/all-api-hub@893e832d0f92:e2e/utils/commonUserFlows.ts:64）。

3. 入口 / 调用关系
- Playwright config 指定 testDir 为 `e2e`，CI 下单 worker、失败截图、trace 和 video（qixing-jk/all-api-hub@893e832d0f92:playwright.config.ts:9）。
- E2E workflow 构建测试扩展后执行 `pnpm e2e`（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/e2e-smoke.yml:67）。
- e2e utils 通过 service worker 读写扩展 storage，模拟真实安装状态（qixing-jk/all-api-hub@893e832d0f92:e2e/accountManagementCommonFlows.spec.ts:44）。

4. 核心 logic / 算法
- E2E 算法是“构建扩展 -> seed 本地状态/网络 stub -> 打开 chrome-extension 页面 -> 断言用户可见 UI 与 storage 后果”。
- 它把 runtime message、service worker、popup/options 页面放在同一测试流里。
- Lazy loading 测试用资源快照差异判断非默认 pane 是否延迟加载。

5. 暴露功能
- 维护者能发现 popup/options 跨页面用户流回归。
- CI 能捕获扩展构建、service worker、资源加载、UI selector 和 storage 兼容性问题。

6. HUAKAI 升级点
- 架构升级：HUAKAI 需要 Admin Ops E2E 覆盖登录、账号池、provider health、quota exhaustion、billing dispute、audit logs。
- 算法升级：引入 fault injection，模拟 provider timeout、partial write、quota race、billing replay。
- 生态升级：测试报告应输出 scenario ID 与 release gate ID，而不只是 Playwright artifact。

## 10. `openspec/`

1. 用途
- 该目录是规格驱动开发仓，包含 project context、rules、active changes 与 archived specs。
- 它把产品行为拆成可追踪 requirement/scenario。

2. 关键文件
- `openspec/config.yaml`: 67 LoC；定义项目上下文、技术栈、约定、存储、测试、权限、安全与 artifact rules（qixing-jk/all-api-hub@893e832d0f92:openspec/config.yaml:11）。
- `openspec/specs/auto-checkin/spec.md`: 447 LoC；定义每日签到调度、catch-up、retry、provider outcome 等要求（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/auto-checkin/spec.md:6）。
- `openspec/specs/api-credential-profiles/spec.md`: 约 100+ LoC；定义独立凭证 CRUD、验证、安全处理、备份与导出（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/api-credential-profiles/spec.md:3）。
- `openspec/specs/managed-site-channel-probe-filters/spec.md`: 约 100+ LoC；定义渠道模型过滤的 probe-backed rule 行为（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/managed-site-channel-probe-filters/spec.md:7）。
- `openspec/specs/webdav-selective-sync-data/spec.md`: 约 100+ LoC；定义 WebDAV 选择性同步域、空选择阻断、读改写保留远端域（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/webdav-selective-sync-data/spec.md:8）。

3. 入口 / 调用关系
- `.claude/` 与 `.codex/` 的 opsx prompts 通过 OpenSpec CLI 读取此目录状态。
- `openspec/config.yaml` 指定业务逻辑应放 `src/services/`，UI 关注渲染，测试使用 Vitest/MSW（qixing-jk/all-api-hub@893e832d0f92:openspec/config.yaml:36）。
- Specs 中的 scenario 应映射到 `tests/` 或 `e2e/`。

4. 核心 logic / 算法
- 规格算法是“capability -> requirement -> scenario -> implementation/test”。
- 安全约束把 token/key/backup 视作 secret，不可日志泄露（qixing-jk/all-api-hub@893e832d0f92:openspec/config.yaml:40）。
- WebDAV spec 明确读远端、合并本地选中域、再整包替换的行为，避免未选域被删除（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/webdav-selective-sync-data/spec.md:71）。

5. 暴露功能
- 维护者可以按能力查行为契约。
- Agent 可以基于 specs/changes 做实施。
- Reviewer 可以对照 scenario 检查覆盖缺口。

6. HUAKAI 升级点
- 架构升级：HUAKAI 的 specs 应强制每个 parity feature 映射到 acceptance tests 与 release gate。
- 安全升级：auth/billing/quota schema 的 spec 必须带 abuse/recovery/operator scenario，不只 happy path。
- 生态升级：OpenSpec 与 `docs/03_FEATURE_PARITY_MATRIX.md`、risk register、scenario ledger 应互相链接。

## 11. `plugins/`

1. 用途
- 该目录只有一个 Vite/WXT 开发插件，用于开发期自动连接 React DevTools。
- 它服务本地开发体验，不是用户可见产品功能。

2. 关键文件
- `plugins/react-devtools-auto.ts`: 275 LoC；启动/检测 standalone devtools、缓存 backend、注入脚本（qixing-jk/all-api-hub@893e832d0f92:plugins/react-devtools-auto.ts:19）。
- `wxt.config.ts`: 153 LoC；在 Vite plugins 中引用该开发插件（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:77）。

3. 入口 / 调用关系
- WXT Vite 配置加载 dev plugin（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:80）。
- 插件根据环境变量决定是否启用、是否自动启动、端口和缓存时长（qixing-jk/all-api-hub@893e832d0f92:plugins/react-devtools-auto.ts:28）。
- 它会访问本机端口并把 devtools backend 写入 public 目录附近。

4. 核心 logic / 算法
- Dev helper 算法是“检查端口 -> 启动或复用 devtools -> 验证返回内容 -> 缓存 backend -> dev 构建注入”。
- 这类逻辑应只在开发模式启用，避免污染 release 产物。

5. 暴露功能
- 前端开发者调试 React tree 更方便。
- 非开发用户理论上不可见。

6. HUAKAI 升级点
- 架构升级：HUAKAI 前端可有 dev-only observability 插件，但必须构建时剔除。
- 安全升级：任何 dev server injection 必须限定 localhost、dev mode 和明确 opt-in。
- 生态升级：把 admin UI 的 profiling、route timing、mock provider panel 做成 dev plugin。

## 12. `resources/`

1. 用途
- 该目录目前是社区/文档资源，主要放微信群图片。
- 它和 README、docs homepage 的社区入口绑定。

2. 关键文件
- `resources/wechat_group.png`: 二进制图片；README 社区 badge 指向该资源（qixing-jk/all-api-hub@893e832d0f92:README.md:37）。
- `docs/docs/README.md`: 文档首页嵌入该图片作为中文社区入口（qixing-jk/all-api-hub@893e832d0f92:docs/docs/README.md:93）。

3. 入口 / 调用关系
- README 将 WeChat 群入口链接到 `resources/wechat_group.png`（qixing-jk/all-api-hub@893e832d0f92:README.md:37）。
- VuePress 文档首页直接展示同一图片（qixing-jk/all-api-hub@893e832d0f92:docs/docs/README.md:93）。

4. 核心 logic / 算法
- 无业务算法。
- 它是社区运营素材，需要随时间维护有效性。

5. 暴露功能
- 用户可从 README/docs 加入社区。
- Operator 可替换社区入口图片。

6. HUAKAI 升级点
- 生态升级：HUAKAI 应把社区/支持渠道资源放在 docs assets，并记录 owner、过期日期、替换流程。
- 安全升级：二维码和外链应做 review，防止供应链或社群劫持。
- 架构升级：Admin Ops 文档中社区入口应与 support SLA、issue triage、incident channel 分离。

## 13. `scripts/`

1. 用途
- 该目录提供本地/CI 辅助脚本：Android dev、i18n 检查、Safari release asset、release notes、资产上传、诊断报告。
- 它连接 package scripts、Husky 和 GitHub Actions。

2. 关键文件
- `scripts/run-i18n-check-if-staged.mjs`: 93 LoC；读取 staged files，仅当相关路径变化时跑 i18n extract check（qixing-jk/all-api-hub@893e832d0f92:scripts/run-i18n-check-if-staged.mjs:12）。
- `scripts/i18n-prune-report.mjs`: 426 LoC；扫描 locale JSON、调用 i18n 工具并生成报告（qixing-jk/all-api-hub@893e832d0f92:scripts/i18n-prune-report.mjs:98）。
- `scripts/prepare-safari-release-assets.sh`: 67 LoC；检查 Safari 构建产物，生成 Xcode bundle 和 release zip（qixing-jk/all-api-hub@893e832d0f92:scripts/prepare-safari-release-assets.sh:15）。
- `scripts/diagnostics/e2e-diagnostics.mjs`: 94 LoC；诊断入口。
- `scripts/diagnostics/lazy-loading-report-utils.mjs`: 1258 LoC；懒加载报告 helper。

3. 入口 / 调用关系
- `package.json` 的 i18n staged script 调 `scripts/run-i18n-check-if-staged.mjs`（qixing-jk/all-api-hub@893e832d0f92:package.json:34）。
- Safari release script 被 release workflow 或人工发布流程调用。
- Diagnostics 脚本服务 E2E/lazy loading/memory 报告。

4. 核心 logic / 算法
- i18n staged 算法：获取 staged 文件 -> path match -> 若相关则跑 extract check，否则跳过。
- Safari release 算法：定位 safari zip 与 extension dir -> 调系统 converter -> 复制/压缩 release assets。
- Diagnostics 算法：收集对照报告，用于性能和懒加载回归分析。

5. 暴露功能
- 贡献者获得更快的 i18n gate。
- Operator 有 Safari 发布辅助。
- Maintainer 有 E2E/性能诊断工具。

6. HUAKAI 升级点
- 架构升级：HUAKAI scripts 应按 `dev/ci/release/diagnostics/migrations` 分组，降低脚本目录膨胀。
- 安全升级：release scripts 必须有 dry-run、artifact checksum、secret absence scan。
- 生态升级：诊断脚本应输出 machine-readable JSON，供 release-readiness gate 汇总。

## 14. `src/`

1. 用途
- `src/` 是主产品代码，包含扩展入口、UI 组件、feature 页面、service 层、类型、工具、样式、本地化和 public manifest locales。
- 这是 ref 的业务核心。

2. 关键文件
- `src/entrypoints/background/index.ts`: 202 LoC；后台生命周期、安装/更新/启动、菜单、消息、临时上下文与 cookie 拦截入口（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/index.ts:52）。
- `src/entrypoints/content/index.ts`: 142 LoC；网页注入侧功能开关、content message handler、兑换助手与 API check 控制器（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/content/index.ts:60）。
- `src/entrypoints/options/App.tsx`: 134 LoC；options 管理页面壳、搜索、侧栏、懒加载 pane（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/options/App.tsx:39）。
- `src/entrypoints/popup/App.tsx`: 103 LoC；popup/sidepanel 复用壳、tabs、stats 和 action（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/popup/App.tsx:22）。
- `src/services/accounts/accountStorage.ts`: 1995 LoC；账号持久化、迁移、刷新、汇总、锁定写入（qixing-jk/all-api-hub@893e832d0f92:src/services/accounts/accountStorage.ts:93）。
- `src/services/webdav/webdavAutoSyncService.ts`: 1554 LoC；WebDAV 自动同步、选择性域、冲突/状态、通知（qixing-jk/all-api-hub@893e832d0f92:src/services/webdav/webdavAutoSyncService.ts:98）。

3. 入口 / 调用关系
- WXT 指向 `src` 和 `src/public`，并把 manifest 权限、public locales、commands 绑定到扩展（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:12）。
- 全局 AppLayout 注入 device、preferences、theme、release status、dialog、toast、update 与签到 pretrigger providers（qixing-jk/all-api-hub@893e832d0f92:src/components/AppLayout.tsx:29）。
- 后台初始化多个 scheduler/service，并要求 alarm listeners 在异步等待前注册，保证 MV3 worker 被唤醒时不漏事件（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/servicesInit.ts:51）。
- Runtime message router 按 action/prefix 转发给账号、签到、历史、模型、通知、WebDAV、验证等子服务（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/runtimeMessages.ts:54）。

4. 核心 logic / 算法
- 账号资产算法：storage service 在跨 popup/options/background 场景下使用写锁，避免 read-modify-write race（qixing-jk/all-api-hub@893e832d0f92:src/services/core/storageWriteLock.ts:1）。
- 站点适配算法：API service 按站点类型选择 override module，再回退 common implementation（qixing-jk/all-api-hub@893e832d0f92:src/services/apiService/index.ts:60）。
- 请求保护算法：每站点 FIFO/token-bucket 限流，控制并发、每分钟请求和 burst（qixing-jk/all-api-hub@893e832d0f92:src/services/apiService/common/siteRequestLimiter.ts:76）。
- 自动化算法：后台用 alarms 恢复签到、用量历史、模型同步、站点公告、WebDAV 和更新检查。
- 站长工具算法：managed-site service 定义搜索、创建、更新、删除渠道、拉模型、构造 payload、匹配渠道等契约（qixing-jk/all-api-hub@893e832d0f92:src/services/managedSites/managedSiteService.ts:50）。
- 模型同步算法：按管理站配置、限流、allowed models、channel config、global filters 生成同步服务（qixing-jk/all-api-hub@893e832d0f92:src/services/models/modelSync/scheduler.ts:76）。
- API 验证算法：先跑模型探测，再用解析出的 model 运行其他 probe；无模型时返回 fail/unsupported summary（qixing-jk/all-api-hub@893e832d0f92:src/services/verification/aiApiVerification/suiteRunner.ts:25）。
- WebDAV 加密算法：以 PBKDF2/AES-GCM envelope 识别加密备份，同时保持未加密备份兼容（qixing-jk/all-api-hub@893e832d0f92:src/services/webdav/webdavBackupEncryption.ts:1）。

5. 暴露功能
- 用户：账号资产看板、余额/用量、自动识别、自动刷新、自动签到、书签、密钥、独立凭证、模型价格、网页 API 检测、导入导出、WebDAV、分享快照。
- Operator/站长：管理站渠道、模型同步、模型重定向、渠道 key 解析/验证、批量 probe。
- 贡献者：清晰的 entrypoints/features/services/types/tests 分层。

6. HUAKAI 升级点
- 架构升级：HUAKAI 应保留“feature UI -> service contract -> background/job worker”的边界，但后台任务要服务端化，配合 Postgres、队列、审计和租户隔离。
- 算法升级：账号/渠道选择应从本地 heuristics 升级为 PASR 类评分，纳入健康、成本、quota、latency、tenant policy 和 fallback budget。
- 安全升级：本地扩展的 key storage 行为不能照搬到 HUAKAI 服务端；服务端必须加 KMS envelope、audit log、scoped token、operator separation。
- 生态升级：网页/API quick check 可转化为 Admin Ops “provider capability probe”，结果沉淀到 provider health 与 routing policy。

## 15. `tests/`

1. 用途
- `tests/` 是 Vitest 单元/组件/服务测试，覆盖 components、contexts、entrypoints、features、hooks、services、types、utils 和 MSW。
- 它比 `e2e/` 更靠近函数、hooks、service contracts。

2. 关键文件
- `tests/setup.ts`: 54 LoC；配置 Testing Library、jsdom polyfill、cleanup、data-testid attribute（qixing-jk/all-api-hub@893e832d0f92:tests/setup.ts:1）。
- `tests/setup.shared.ts`: 3079 bytes；共享测试初始化。
- `tests/services/accountOperations.test.ts`: 440 LoC；验证账号操作、手动余额、tag clearing 等逻辑（qixing-jk/all-api-hub@893e832d0f92:tests/services/accountOperations.test.ts:47）。
- `tests/services/webdavAutoSyncService.test.ts`: WebDAV 自动同步服务测试。
- `tests/components/VerifyApiDialog.test.tsx`: API 验证 UI 测试。

3. 入口 / 调用关系
- `vitest.config.ts` 将 DOM 与 Node 测试分项目运行，DOM setup 用 `tests/setup.ts`，Node setup 用 `tests/setup.node.ts`（qixing-jk/all-api-hub@893e832d0f92:vitest.config.ts:36）。
- CI unit job 运行 coverage 并上传报告（qixing-jk/all-api-hub@893e832d0f92:.github/workflows/test.yml:100）。
- MSW、fake browser 和 test-utils 支撑 service 与 UI 的隔离测试。

4. 核心 logic / 算法
- 测试分层算法：DOM 组件/entrypoint 走 jsdom，纯 service/util 走 node。
- Mock 算法：通过 hoisted mocks 替换网络/service，断言 service 行为与 storage 更新。
- Coverage 算法：源代码覆盖率门槛由 Vitest config 管理（qixing-jk/all-api-hub@893e832d0f92:vitest.config.ts:58）。

5. 暴露功能
- 维护者能快速回归 service 与 UI 组件。
- CI 能阻止核心逻辑、翻译、类型、未用代码问题进入 main。
- Reviewer 可通过测试名反推能力覆盖点。

6. HUAKAI 升级点
- 架构升级：HUAKAI tests 应用 contract tests 覆盖 API gateway protocol、account hub state transitions、admin ops UI。
- 算法升级：为 routing、quota、billing、provider failover 添加 property/parallel/concurrency tests。
- 生态升级：每个 acceptance scenario 应有 AT ID，测试报告需能反向写入 release gate。

## 16. 跨目录 workflow trace

### 16.1 扩展启动与后台任务恢复

| 步 | 目录/文件 | 观察 |
|---|---|---|
| 1 | `wxt.config.ts` | WXT 把 `src`、`src/public`、manifest 权限和 commands 接入构建（qixing-jk/all-api-hub@893e832d0f92:wxt.config.ts:12） |
| 2 | `src/entrypoints/background/index.ts` | 后台入口注册 runtime message、temp context、cookie interceptor、context menu（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/index.ts:52） |
| 3 | `src/entrypoints/background/servicesInit.ts` | 启动时先注册 alarm scheduler，再初始化 i18n、模型元数据、账号刷新与网页助手（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/servicesInit.ts:51） |
| 4 | `src/services/models/modelSync/scheduler.ts` | 模型同步 scheduler 从偏好和渠道配置构建服务，并处理限流与过滤（qixing-jk/all-api-hub@893e832d0f92:src/services/models/modelSync/scheduler.ts:76） |
| 5 | `src/services/webdav/webdavAutoSyncService.ts` | WebDAV 后台服务使用 alarms 与 in-flight guard 管同步（qixing-jk/all-api-hub@893e832d0f92:src/services/webdav/webdavAutoSyncService.ts:98） |
| HUAKAI 升级 | n/a | 改成 server-side worker + queue + lease + audit；浏览器 alarm 的概念可对应租户级 scheduled job。 |

### 16.2 用户从 popup/options 管账号与凭证

| 步 | 目录/文件 | 观察 |
|---|---|---|
| 1 | `src/components/AppLayout.tsx` | 全局 providers 包装页面，注入偏好、主题、更新、dialog 和 toast（qixing-jk/all-api-hub@893e832d0f92:src/components/AppLayout.tsx:29） |
| 2 | `src/entrypoints/popup/App.tsx` | Popup 在账号、书签、独立凭证之间切换，并预加载目标 view（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/popup/App.tsx:56） |
| 3 | `src/features/AccountManagement/AccountManagement.tsx` | 账号页提供刷新、禁用账号刷新、外部签到入口和去重对话（qixing-jk/all-api-hub@893e832d0f92:src/features/AccountManagement/AccountManagement.tsx:24） |
| 4 | `src/services/accounts/accountStorage.ts` | 账号 storage 在写入前加跨上下文锁（qixing-jk/all-api-hub@893e832d0f92:src/services/accounts/accountStorage.ts:93） |
| 5 | `e2e/accountManagementCommonFlows.spec.ts` | E2E 直接 seed storage 并通过扩展页面验证用户流（qixing-jk/all-api-hub@893e832d0f92:e2e/accountManagementCommonFlows.spec.ts:67） |
| HUAKAI 升级 | n/a | 把本地 storage write lock 升级为 Postgres transaction + optimistic concurrency + audit event。 |

### 16.3 网页 API 检测与 capability probe

| 步 | 目录/文件 | 观察 |
|---|---|---|
| 1 | `src/entrypoints/content/index.ts` | Content script 按偏好启停兑换助手与网页 API 检测控制器（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/content/index.ts:83） |
| 2 | `src/entrypoints/background/contextMenus.ts` | 后台按偏好创建右键菜单，并把触发转发给 content script（qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/background/contextMenus.ts:70） |
| 3 | `src/services/verification/webAiApiCheck/background.ts` | 后台处理 base URL 规范化、模型拉取、probe 执行和保存 profile（qixing-jk/all-api-hub@893e832d0f92:src/services/verification/webAiApiCheck/background.ts:44） |
| 4 | `src/services/verification/aiApiVerification/suiteRunner.ts` | Probe suite 先找可用模型，再运行其他验证项（qixing-jk/all-api-hub@893e832d0f92:src/services/verification/aiApiVerification/suiteRunner.ts:31） |
| 5 | `openspec/specs/web-ai-api-check/spec.md` | Spec 要求 context-menu 手动触发、可编辑参数、抓模型、测试、secret redaction（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/web-ai-api-check/spec.md:6） |
| HUAKAI 升级 | n/a | 转化为 provider onboarding probe：探测结果进入 provider registry、routing eligibility、health timeline。 |

### 16.4 WebDAV 备份、选择性同步与加密

| 步 | 目录/文件 | 观察 |
|---|---|---|
| 1 | `src/features/ImportExport/ImportExport.tsx` | 导入/导出页面把手动备份、手动导入、WebDAV 设置和自动同步设置放在同页（qixing-jk/all-api-hub@893e832d0f92:src/features/ImportExport/ImportExport.tsx:73） |
| 2 | `openspec/specs/webdav-selective-sync-data/spec.md` | Spec 要求账号、书签、独立凭证、偏好四个同步域可选且空选择阻断（qixing-jk/all-api-hub@893e832d0f92:openspec/specs/webdav-selective-sync-data/spec.md:8） |
| 3 | `src/services/webdav/webdavAutoSyncService.ts` | 自动同步服务合并账号、凭证、偏好、tags、channel configs 等域（qixing-jk/all-api-hub@893e832d0f92:src/services/webdav/webdavAutoSyncService.ts:1） |
| 4 | `src/services/webdav/webdavBackupEncryption.ts` | 加密 envelope 用 PBKDF2/AES-GCM 并兼容非加密旧内容（qixing-jk/all-api-hub@893e832d0f92:src/services/webdav/webdavBackupEncryption.ts:1） |
| HUAKAI 升级 | n/a | 替换为 server-side tenant export/import，支持 per-domain restore、dry-run diff、signed backup、KMS key rotation。 |

## 17. HUAKAI 整体升级 punch list

| ref 项 | HUAKAI 现状 | HUAKAI 升级建议 | 升级维度 | 优先级 |
|---|---|---|---|---|
| WXT 多入口扩展壳 | HUAKAI 是服务端 gateway + admin ops，不应复制扩展壳 | 把“entrypoint + provider + service”拆成 API gateway ingress、admin UI、background worker 三层 contract | 架构升级 | P0 |
| 本地账号 storage 写锁 | HUAKAI 需要多租户并发与审计 | 用 Postgres transaction、advisory/row locks、version field、audit event 替代本地锁 | 架构/安全 | P0 |
| 每站点 request limiter | HUAKAI routing 需要 provider/account 级限流 | 建立 tenant/provider/account/model 多维 token bucket，并和 quota/billing 状态联动 | 算法升级 | P0 |
| 后台 alarm scheduler | HUAKAI 不能依赖浏览器 alarm | 建 tenant-aware job scheduler，支持 lease、retry、dead-letter、operator replay | 架构升级 | P0 |
| API capability probe suite | HUAKAI provider onboarding 需要真实探测 | 把模型、文本、工具、结构化输出、web-search probe 变为 provider health 的标准检查 | 生态/算法 | P0 |
| WebDAV 选择性同步 | HUAKAI 需要数据导入导出与恢复 | 做 per-domain export/import、dry-run diff、conflict report、operator approval | 生态升级 | P1 |
| WebDAV 加密 envelope | HUAKAI 有服务端密钥责任 | 用 KMS envelope encryption、tenant key separation、rotation metadata、restore audit | 安全升级 | P0 |
| 独立 URL+Key 凭证 | HUAKAI 需要 provider credential vault | 做 scoped provider credentials、secret redaction、usage attribution、rotation workflow | 安全/架构 | P0 |
| 管理站渠道同步 | HUAKAI routing 需要 account/channel inventory | 建 provider account hub，支持 channel import、capability sync、model metadata reconciliation | 架构/生态 | P0 |
| Probe-backed model filters | HUAKAI routing 需动态能力过滤 | 让模型 eligibility 由 live probe + historical SLA + policy rule 决定 | 算法升级 | P1 |
| 模型重定向 | HUAKAI 不能只做静态 mapping | 引入 policy-based model alias、fallback chain、cost cap、tenant allow/deny | 算法升级 | P0 |
| Popup/options 信息架构 | HUAKAI Admin Ops 需要高密度运维面板 | 将“账号/密钥/模型/同步/验证”转为 ops nav：providers、accounts、keys、routing、quota、billing、logs | 生态升级 | P1 |
| Lazy loading E2E | HUAKAI admin UI 也会变重 | 做 route-level chunk regression、critical path TTI budget、visual smoke | 生态升级 | P2 |
| OpenSpec-driven changes | HUAKAI 已有 spec/parity/risk 体系 | 强制每个 spec requirement 绑定 AT ID、risk ID、release gate status | 架构升级 | P0 |
| Agent ops prompts | HUAKAI 需要 cross-agent governance | 建 plan-codex/plan-claude/synthesis/review 四件套，不让 prompt 直接替代批准 | 生态升级 | P0 |
| Docs assistant LLM 翻译 | HUAKAI 合规文档不可自动信任 | 文档自动化只生成 draft，必须人工/agent review 后入库 | 安全/生态 | P1 |
| Release dry-run | HUAKAI 生产发布高风险 | 所有 release 都要 artifact attestation、SBOM、config diff、rollback plan | 安全升级 | P0 |
| E2E user-flow fixtures | HUAKAI 需要真实 ops scenario | 扩展到 provider outage、quota depletion、billing replay、audit search、abuse response | 生态升级 | P0 |

## 18. 证据覆盖与 open questions

- Observed regions: 60+ selected files / directory listings, all from `~/refs/all-api-hub/` except HUAKAI brief and local skill instruction.
- Inferences: HUAKAI 升级点均为 HUAKAI-fit reasoning，不声称 ref 已实现这些升级。
- Open questions: 3。
- Open question 1：未逐行精读 `src/services/accounts/accountStorage.ts` 1995 LoC，账号迁移/刷新细节需要 T2/T3 精读。
- Open question 2：未逐行精读 `src/services/webdav/webdavAutoSyncService.ts` 1554 LoC，冲突处理和选择性同步实现需 T2/T3 验证。
- Open question 3：未逐行精读各 provider adapter，具体站点行为不能在本文件中当作最终 parity 结论。

---
Agent: codex
Ref: all-api-hub
SHA: 893e832d0f92
Pushed: 2026-05-09
Mining started: 2026-05-13T08:59:46Z
Mining done: 2026-05-13T09:19:57Z
Output LoC: 595
Source files read (per CLAUDE.md #11 closing): README.md; package.json; wxt.config.ts; vitest.config.ts; playwright.config.ts; tsconfig.json; .claude/skills/openspec-apply-change/SKILL.md; .codex/prompts/opsx-apply.md; .github/workflows/build-and-publish.yml; .github/actions/package-extension/action.yml; .github/workflows/test.yml; .github/workflows/e2e-smoke.yml; .husky/pre-commit; .husky/pre-push; docs/package.json; docs/docs/README.md; docs/docs/.vuepress/config.js; docs_assistant/README.md; docs_assistant/main.py; e2e/accountManagementCommonFlows.spec.ts; e2e/keyManagementCommonFlow.spec.ts; e2e/popupApiCredentialProfiles.spec.ts; e2e/autoCheckinQuickRun.spec.ts; e2e/bookmarkManagementCommonFlows.spec.ts; e2e/lazyEntryLoading.spec.ts; e2e/utils/commonUserFlows.ts; openspec/config.yaml; openspec/specs/auto-checkin/spec.md; openspec/specs/api-credential-profiles/spec.md; openspec/specs/managed-site-channel-probe-filters/spec.md; openspec/specs/webdav-selective-sync-data/spec.md; openspec/specs/web-ai-api-check/spec.md; plugins/react-devtools-auto.ts; scripts/i18n-prune-report.mjs; scripts/run-i18n-check-if-staged.mjs; scripts/prepare-safari-release-assets.sh; src/entrypoints/background/index.ts; src/entrypoints/background/runtimeMessages.ts; src/entrypoints/background/servicesInit.ts; src/entrypoints/background/contextMenus.ts; src/entrypoints/background/cookieInterceptor.ts; src/entrypoints/background/tempWindowPool.ts; src/entrypoints/content/index.ts; src/entrypoints/options/App.tsx; src/entrypoints/popup/App.tsx; src/components/AppLayout.tsx; src/features/AccountManagement/AccountManagement.tsx; src/features/KeyManagement/KeyManagement.tsx; src/features/ModelList/ModelList.tsx; src/features/ManagedSiteChannels/ManagedSiteChannels.tsx; src/features/ImportExport/ImportExport.tsx; src/features/UsageAnalytics/UsageAnalytics.tsx; src/services/accounts/accountStorage.ts; src/services/accounts/accountOperations.ts; src/services/accounts/autoRefreshService.ts; src/services/apiService/index.ts; src/services/apiService/common/siteRequestLimiter.ts; src/services/core/storageWriteLock.ts; src/services/managedSites/managedSiteService.ts; src/services/models/modelSync/scheduler.ts; src/services/models/modelSync/modelSyncService.ts; src/services/models/modelRedirect/ModelRedirectService.ts; src/services/checkin/autoCheckin/scheduler.ts; src/services/history/usageHistory/scheduler.ts; src/services/siteDetection/autoDetectService.ts; src/services/verification/aiApiVerification/suiteRunner.ts; src/services/verification/webAiApiCheck/background.ts; src/services/webdav/webdavAutoSyncService.ts; src/services/webdav/webdavBackupEncryption.ts; src/locales/README.md; src/utils/i18n/resources.ts; src/public/_locales/en/messages.json; src/constants/siteType.ts; src/constants/optionsMenuIds.ts; tests/setup.ts; tests/services/accountOperations.test.ts.

中文总结：本次为 all-api-hub 的 T1 目录骨架 clean-room 拆解，只基于本地目录、少量入口/服务/测试/文档源码片段形成行为级观察；真实观察覆盖 WXT 扩展入口、OpenSpec、CI/E2E、账号/模型/WebDAV/API 验证等目录，合理推断集中在 HUAKAI 的服务端化、审计化、多租户化升级方向；open question 共 3 个，主要是大体量账号/WebDAV/provider adapter 仍需 T2/T3 精读。本文件没有复制 ref 源码、实现顺序或可移植目录结构，也没有把 AGPL 代码带入 HUAKAI。
