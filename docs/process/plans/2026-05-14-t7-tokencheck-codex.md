# 2026-05-14 T7 tokencheck

| Owner directive | "HUAKAI 信任链 T7 — token count cross-check + cache hit verify 防虚报。" |
| Scope | 新建 `backend/internal/tokencheck/` 独立 Go 包与单元测试；不修改 proto / gateway / handler / schema / LICENSE；不引入依赖。 |
| Success criteria | `go test ./internal/tokencheck/...` 通过；`go vet ./internal/tokencheck/...` 通过；每个源码文件 LoC <= 250、每个测试文件 LoC <= 200；`/tmp/codex-t7-tokencheck-final.txt` 写入 LoC、测试数、回归证据。 |
| Time estimate | 约 30-45 分钟 wall clock；Codex 单轮实现 + 测试。 |
| Blast radius | 新包当前未接入主链路，主要风险是类型接口设计不贴合现有 `proto` 结构或未来接入时 warning 不可读。 |
| Failure modes | 估算公式过于精确导致误判：用 5%/20% 可配置阈值、Unknown verdict 处理零值；cache detail JSON 形态不一致：解析多种常见字段名并在缺证据时返回 ok/无 warning；`ProtocolLossEntry` silent drop：warning 填 `Severity` + `Reason` + `Code`。 |
| Decision points | 无需 Owner 中途确认：本任务只新增独立包，且 Owner 明确要求不要问。未来接入 gateway/settler 属于后续高风险链路改动，应单独确认。 |
| Pre-execution checklist | 1. 写 `/tmp/codex-t7-tokencheck.txt` stub；2. 读取现有 `proto` 类型；3. 新增 4 个实现文件；4. 新增 3 个测试文件；5. 运行 gofmt/go test/go vet；6. 写 `/tmp/codex-t7-tokencheck-final.txt`。 |

Concrete execution order:

1. 定义共享 Verdict / Discrepancy / CacheVerifyResult / thresholds。
2. 实现 `HeuristicEstimator` 与 `NoopEstimator`。
3. 实现 reported-vs-estimated cross-check。
4. 实现 HopAttestation.Detail cache 字段解析与 Usage 交叉验证。
5. 覆盖 estimator / crosscheck / cache verify 单测。
6. 统计 LoC 与测试数量，写入 `/tmp` final 证据。
