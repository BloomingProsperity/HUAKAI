# 依赖与许可证政策

本政策适用于 Go、Rust、前端、构建工具和容器运行依赖。

1. 新增依赖前必须核实许可证、维护状态、最近发布、已知漏洞和传递依赖。
2. Runtime 路径只允许与项目 MIT 发布边界兼容、来源清晰且仍在维护的依赖。
3. GPL、LGPL、AGPL、未知许可证和无许可证代码不得进入 runtime、vendoring
   或发布产物；许可证风险只能改变实现方式，不能静默删除产品能力。
4. PR 必须列出新增或升级的直接依赖、版本、用途、许可证、传递依赖变化和
   验证命令。
5. Go 依赖变更必须提交一致的 `go.mod`、`go.sum`，运行 `go mod tidy`、
   `go test`、`go vet` 和 `govulncheck`。
6. Rust 依赖变更必须提交一致的 `Cargo.toml`、`Cargo.lock`，并在对应
   workspace 运行 `cargo deny check`、`cargo clippy` 和 `cargo test`。
7. 测试或本地工具依赖也要注明非 runtime 范围，不能借工具链把受限代码带入
   构建产物。
8. 不能通过 rename、fork、关闭默认检查或修改基线来绕过许可证与漏洞门。
9. 因网络、索引或工具故障无法完成检查时，PR 不得声称已验证；必须记录失败
   输出和补跑条件。
10. 读取非 MIT 参考项目源码时仍要执行 clean-room 分车道；“只加依赖、不复制
    代码”不能替代许可证核实。
