# 2026-06-24 backend quality renew round123 HCSF clone

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；继续 `/home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md` 中 HCSF clone 热路径债。 |
| Scope | 仅处理 `backend/internal/gateway/upstream_dispatcher_hcsf.go` 的 `cloneHCSF` 防回退纪律。当前源码已由既有未提交改动把 JSON marshal/unmarshal clone 改为反射深拷贝，并有深拷贝行为测试；本轮只补静态 guard，防止后续退回 JSON clone。 |
| Success criteria | 1. `codebudget` 新增静态测试，明确禁止 `cloneHCSF` 区段内出现 `json.Marshal` / `json.Unmarshal`；2. guard 要求 `cloneHCSF` 继续走 `cloneReflectValue` 并清理 HCSF 非 wire 字段；3. 不修改 HCSF 出站语义、不修改协议映射、不扩大 `gateway` god 包实现。 |
| Time estimate | 约 10-20 分钟。 |
| Blast radius | 仅新增测试 guard 与计划文档；不触碰生产逻辑。 |
| Failure modes | 1. guard 过宽误伤同文件其他合法 JSON 处理；缓解：只截取 `func cloneHCSF` 到 `func clearHCSFNonWireFields` 的函数区段。2. Go 工具链缺失导致不能实际运行测试；缓解：跑文本检查并如实记录。 |
| Decision points | 是否进一步把反射 clone 改成手写 typed clone：本轮不做，因为 `proto.HCSF` 字段面较宽，手写 clone 是更大行为面，需单独计划与 Go 测试环境。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已核实 `cloneHCSF` 当前不是 JSON clone；3. 已确认 `gateway/upstream_dispatcher_hcsf.go` 和测试已有未提交改动，本轮不覆盖；4. 不读取/修改另一个目标的 `2026-06-23-backend-security-scan-codex.md`。 |

## 执行顺序

1. 新增 `backend/internal/codebudget/hcsf_clone_guard_test.go`，读取 `upstream_dispatcher_hcsf.go`。
2. 精准截取 `cloneHCSF` 函数体，禁止 JSON clone 回归。
3. 要求 `cloneReflectValue(reflect.ValueOf(*env))` 和 `clearHCSFNonWireFields(&out)` 两个关键语义点存在。
4. 运行文本检查、clean-room 禁词扫描、可用 Go 命令。
