# 2026-07-12 Antigravity OAuth 客户端环境变量覆盖（Codex 独立计划）

> 综合状态：Codex 独立草案完成后，已与
> `2026-07-12-antigravity-oauth-client-to-env-claude.md` 对照。本文件作为
> “synthesized after diff” 的执行计划；Owner 本次逐项指令是最终边界。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “给 Antigravity 内置 OAuth `client_id`/`client_secret` 增加环境变量 override，同时保留内置默认值。” |
| Scope | 范围内：`internal/provider/antigravity/bootstrap.go`、对应单元测试、`internal/credentialworker/mode_refresh_test.go` 的调用迁移、`.env.prod.example` 可选配置说明。范围外：token 端点、scopes、redirect URI、payload 覆盖规则、其他 provider、数据库与部署逻辑。 |
| Success criteria | 非空环境变量优先；未设置或仅空白时使用既有内置值；固定公开 profile 的其余字段不变；所有旧导出常量值引用迁移为函数调用；指定构建、vet、定向测试通过；两项变异分别被目标测试捕获。 |
| Time estimate | 墙钟约 15—25 分钟；单 agent 实施与验证。 |
| Blast radius | Antigravity 默认 OAuth 配置、刷新适配器构造及依赖这两个导出标识符的测试；错误实现可能导致刷新校验拒绝合法配置，或让环境变量覆盖失效。 |
| Failure modes | 环境变量空白误覆盖默认值：统一 `TrimSpace` 后判断；配置创建与校验读取时环境发生变化导致不匹配：测试内保持环境稳定并只按既定语义实现；遗漏旧常量引用导致编译失败：全仓精确扫描；变异后未还原：每次变异后立即应用反向补丁并复跑目标测试。 |
| Decision points | 用户已明确决定保留内置默认，并仅开放 client ID/secret 的环境变量覆盖；不需要中途扩大设计。若发现生产代码存在用户未说明的旧 API 外部依赖，只做函数调用迁移，不提供兼容变量。 |

## 并行计划对照结论

- 一致项：两份计划都要求保留内置值、非空 env 优先、固定 token/scopes/redirect profile、不接受 payload 改写，并以 build、vet、定向测试和两项变异证验收。
- 冲突项：无。
- 互补项：Codex 草案补充空白 env 的回退语义、全仓引用扫描和变异还原防护；Claude 草案补充保留公开 native-app wire 值会继续触发 secret scanning 的运营提示。
- 综合决定：严格按 Owner 当前指令实施，不改变既有固定公开 profile，不新增兼容变量，不扩展到其他 provider。

## 预执行检查清单

1. 精确扫描两个旧导出常量的全部引用，确认工作区既有改动并避免覆盖。
2. 核对 `.env.prod.example` 中 OAuth/vendor 配置区，选择相邻位置加入注释配置。
3. 修改生产代码与测试，执行 `gofmt`。
4. 再次扫描，确保旧值式引用清零，固定 token/scopes/redirect/source 逻辑未变。
5. 设置 `GOFLAGS=-buildvcs=false`，依次运行全仓 build、vet 和两个定向包测试。
6. 逐一执行环境变量分支删除、内置 ID 置空的变异；保存失败输出后立即还原，并复跑正式测试确认工作树是正确版本。
7. 只使用只读 Git 命令核对差异；禁止 `git add`、`git commit` 及其他 Git 写操作。

## 具体执行顺序

1. 在 `bootstrap.go` 增加环境变量名常量、内置非导出常量和两个访问函数，并让默认配置、校验与 adapter 全部调用访问函数。
2. 将 Antigravity 测试和 credential worker 测试的旧常量值引用改成函数调用，新增环境变量覆盖判别测试。
3. 在生产环境示例的 OAuth 配置附近加入两项注释掉的可选 override，不写真实 secret。
4. 完成格式、静态检查、测试、变异证明及最终差异审阅。
