# 2026-06-23 backend-quality-renew-round95-hcsf-clone-hotpath-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| --- | --- |
| Scope | 修 `backend/internal/gateway/upstream_dispatcher_hcsf.go` 的 `cloneHCSF`，去掉非流热路径每次 `json.Marshal/Unmarshal` 深拷贝；补 HCSF clone 隔离测试。 |
| Out of scope | 不改 DispatchHCSF 的路由/协议/计费语义，不改 provider adapter，不改 schema，不删除 Rust H2 文件，不新增 gateway 包文件。 |
| Success criteria | `cloneHCSF` 不再调用 JSON 编解码；clone 后的 slice/map/pointer/json.RawMessage 与原对象不共享底层可变数据；现有 HCSF dispatch 行为保持。 |
| Time estimate | 约 30-45 分钟。 |
| Blast radius | `DispatchHCSF` 非流式响应合成路径；若 clone 漏字段，可能丢请求侧上下文或 capability graph。用反射深拷贝覆盖新增字段，降低手写字段漂移风险。 |
| Failure modes | 反射 clone 误处理 nil slice/map：保留 nil；误处理 unexported 字段：HCSF/proto 结构为导出字段，测试覆盖关键可变字段；误改变 `json:"-"` passthrough 行为：本轮测试聚焦当前 HCSF dispatch 需要的字段隔离，不扩大为 passthrough 行为变更。 |
| Decision points | 如果 Owner 要把 clone helper 下沉到 `internal/proto` 作为公共 API，需要另开计划；本轮不新增包文件，避免扩大 god 包体量。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `cloneHCSF` 当前使用 JSON 深拷贝；4. 已核 HCSF 主要可变字段为 slice/map/pointer/json.RawMessage。 |

## 执行顺序

1. 在现有 HCSF dispatcher 文件内增加受限反射深拷贝 helper。
2. 将 `cloneHCSF` 改为调用该 helper。
3. 在 `upstream_dispatcher_hcsf_test.go` 增加 clone 隔离测试。
4. 运行可用静态检查；若 `go test`/`gofmt` 缺失，记录真实限制。
