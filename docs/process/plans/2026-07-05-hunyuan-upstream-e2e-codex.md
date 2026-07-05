# 2026-07-05 混元真实上游 E2E 测试

| Owner directive | “真实上游端到端测试(混元真转发+计费+配额+并发)” |
| Scope | 仅新增 `cmd/gateway/upstream_e2e_test.go`，实现 `e2e_upstream` build tag 下的真实混元 Chat Completions E2E；不修改 `internal/` 生产代码、不提交、不硬编码 API key。 |
| Success criteria | 测试编译通过；单请求断言 HTTP 200、真实上游内容、usage 非零、claim committed、actual_cost > 0、quota reserved 归零且 settled > 0、usage_records 非零；并发子测试断言超出并发 cap 后部分 429 或排队成功、所有请求结束后账号 in_flight_count 归零、成功请求均落账。 |
| Time estimate | 约 60-90 分钟；主要时间用于亲读黄金模板、确认表结构、写测试、跑 gofmt/vet/build。 |
| Blast radius | 新增 build-tagged E2E 测试文件；默认 `go test` 不会运行真实上游；需要运行者显式提供 `HUAKAI_E2E_DATABASE_URL` 与 `HUAKAI_E2E_HUNYUAN_KEY`。 |
| Failure modes | 表结构或迁移列名误判导致编译或运行失败：通过读取迁移与现有测试校准；真实上游响应口径与网关 usage 口径有差异：断言 token 非零并对 usage 做非零/合理一致检查；并发请求若全部排队成功：允许“429 或排队后成功”，但仍要求槽位释放与成功落账。 |
| Decision points | 若需要改 `internal/` 生产逻辑、迁移、鉴权/计费/配额核心或新增运行时依赖，停止并请求 Owner；本轮预期不触发。 |
| Pre-execution checklist | 1. 完整亲读 `cmd/gateway/smoke_test.go`。2. 亲读混元 protocol/vendor 常量与 credential handler。3. 查明配额、usage、claim、并发槽相关表列与现有测试。4. 新增测试文件并保持注释中文。5. 跑 `gofmt -l`、`go vet -tags e2e_upstream ./cmd/gateway/`、`go build ./...`。 |

## 具体执行顺序

1. 以 `smoke_test.go` 为黄金模板复制 seed 链、构建网关、子进程启动、KEK env、端口等待、清理顺序与并发槽断言方式。
2. 将 provider/account/credential 改为混元真实端点、`hunyuan_chat` protocol family、`hunyuan` vendor、`api_key` auth_mode，并从 `HUAKAI_E2E_HUNYUAN_KEY` 读取明文 key。
3. 将 model registry 绑定改为 `hunyuan-lite`，请求体改为非流式 OpenAI Chat Completions，`max_tokens=16`。
4. 额外 seed 用户 cost 配额策略，单请求后校验 claim、usage_records 与 quota_windows。
5. 增加并发子测试，发起 `cap_concurrency+2` 个请求，统计 200/429/其它错误，结束后检查账号槽位释放和成功请求落账。
6. 执行格式化、vet、build；真实上游请求不在本机主动运行，报告运行命令与证据。
