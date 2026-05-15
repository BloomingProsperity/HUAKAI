# 依赖许可证政策

1. 任何 PR 引入新的 runtime dependency 前，必须先在对应 Rust workspace 运行 `cargo deny check`。
2. Rust core gateway 使用的 deny 配置见 `exploratory/rust-core-gateway/merged/deny.toml`。
3. PR description 必须列出新增 direct runtime dependency 的名称、版本、来源和 license。
4. PR description 也必须说明是否新增 transitive dependency，以及是否已经被 cargo-deny 覆盖。
5. GPL、LGPL、AGPL、unknown license、unlicensed dependency 一律拒绝进入 runtime 路径。
6. `wreq-util` 和 `rquest-util` 已被显式 ban；不得通过 rename、fork 或 feature 组合绕过。
7. 如果产品能力需要类似 preset 的效果，必须使用 HUAKAI 自有模板或 permissive 依赖实现。
8. 新依赖如果只用于测试或本地工具，也要在 PR description 明确标注非 runtime 范围。
9. cargo-deny 因网络或索引问题无法完成时，PR 不能直接合并；需要记录失败输出和补跑条件。
10. 该政策对应风险登记 `docs/10_RISK_REGISTER.md` 中的 `R-LIC-003`。
11. license 风险只能改变实现方法，不能删除或缩水既定功能。
