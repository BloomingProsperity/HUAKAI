# 2026-07-05 models protocol_family CHECK 与混元 E2E 修复

| Owner directive | "修 models protocol_family CHECK 缺 19 vendor family(S1 上线阻塞)+ 打通混元 e2e" |
| Scope | 后端本仓库内迁移、相关 Go 侧 protocol family 白名单、迁移/白名单判别测试，以及 `cmd/gateway/upstream_e2e_test.go` 的混元 seed no_capacity 修复；不提交 commit、不运行真实上游请求、不写入真实密钥。 |
| Success criteria | `models_protocol_family_check` 覆盖 registrydefault 已注册 adapter family；down 在存在新增 family 数据时 fail-fast；本地测试能证明旧 CHECK 拒绝、新 CHECK 接受 `hunyuan_chat`；Go 侧若存在白名单则与 registrydefault 单一真相源对齐；混元 E2E seed 走到可选号，不再因 seed 漏环导致 `no_capacity`。 |
| Time estimate | 约 2-4 小时，取决于现有迁移测试基础、e2e seed 依赖和全量 `go vet` / `go build` 时长。 |
| Blast radius | 中高：新增数据库迁移会改变 `models.protocol_family` 允许集合；Go 侧白名单若修改会影响管理端建模/同步校验；E2E 文件仅在 `e2e_upstream` build tag 下编译运行。 |
| Failure modes | 迁移允许未注册 placeholder family：通过逐个核实 `MustRegister` 和 env-gated 注册状态避免；down 缩回破坏已有数据：加 fail-fast guard；Go 白名单多处漂移：尽量抽 registrydefault 导出集合复用；E2E no_capacity 修复误判：对比 smoke seed、读取 resolver/selector SQL WHERE 后再改。 |
| Decision points | 不修改 `LICENSE`、真实密钥、生产部署脚本；不做 git commit。若必须改 auth core、billing ledger、quota enforcement 或 destructive migration，停止请求 Owner。当前任务已明确授权新增谨慎迁移。 |
| Pre-execution checklist | 1. 亲读 `registrydefault/default.go` 并核实每个 `Protocol*` 常量是否实际 `MustRegister`。2. 亲读 `0008` 与 `0011` 当前 CHECK。3. grep 全仓 protocol_family 白名单。4. 对比 `smoke_test.go` 与 `upstream_e2e_test.go` seed 链。5. 写迁移、测试与最小 seed 修复。6. 跑 gofmt、目标包测试、`go vet -tags e2e_upstream ./cmd/gateway/`、`go build ./...`，真实上游请求留给注入 key 的运行者。 |

## 具体执行顺序

1. 枚举 registrydefault 已注册 family，区分默认注册与 env-gated 注册；只把确认有 adapter 注册路径的 family 纳入 CHECK，并在报告逐个列出。
2. 新增 `0172` up/down：up drop/add 扩集合；down 先检查新增 family 存量行，存在则 RAISE，再缩回 0011 后的原集合。
3. 搜索管理端与同步 writer 的白名单，优先把 Go 侧允许集收敛到 `registrydefault` 导出函数；必要时加判别测试。
4. 查找迁移测试惯例，新增 `hunyuan_chat` apply/未 apply 判别测试；每个测试说明如果把集合变异回旧值会红。
5. 定位混元 E2E `no_capacity`：对照 smoke seed 与 selector/resolver 查询，补齐缺失的 alias、binding、model/provider 协议或 account 启用条件。
6. 执行格式化和本地验证；不运行会触达真实混元上游的测试主体，只保证编译与可由运行者注入 key 执行。
