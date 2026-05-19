# 2026-05-14 Rust Redaction Wiring Codex Plan

| Owner directive | "给 Rust 数据面原型 (exploratory/rust-core-gateway/merged/) 做强制脱敏接线" |
| Scope | In: `exploratory/rust-core-gateway/merged/` 内 prost Debug 派生配置、secret-bearing 类型手写 Debug、redaction 注释与测试、cache no-op 清理、cargo build/test。Out: Go backend、数据库 schema、真实凭据、LICENSE、git commit。 |
| Success criteria | `RoutePlan` / `UpstreamAuthMaterial` / `AttemptReportRequest` / `PlannedAttempt` / attempt reporter 内含 `acquisition_token` 的自有结构 Debug 输出不包含原始 secret，包含 redaction 占位符，非 secret 字段仍可见；dead cache no-op 删除；`cargo build` 与 `cargo test` 在 merged workspace 通过；报告列出 grep 覆盖点。 |
| Time estimate | 约 45-75 分钟 wall clock；Codex 单 agent 实现、测试、复查。 |
| Blast radius | Rust 原型 crate 的 Debug trait、生成代码配置和局部单测；若 prost 配置不当，可能导致生成代码 trait 冲突或测试编译失败。 |
| Failure modes | prost 版本不支持目标 skip API：改用 `type_attribute` 等等价方式；手写 Debug 字段遗漏：用 grep 和新增断言覆盖；cache no-op 删除误伤测试：同步删除只验证 no-op 的旧测试；错误类型泄露：检查 Display/Debug 派生字段。 |
| Decision points | 高风险项无；若需要新增 runtime dependency、改 auth/quota/billing/db schema、或触碰真实 secret，则停止请求 Owner 确认。本任务不预计触发。 |
| Pre-execution checklist | 1. 阅读 build.rs、route_proto.rs、redaction.rs、account_planner.rs、attempt_reporter.rs、route_client.rs。2. 确认 prost 生成模块路径与 trait 冲突点。3. 确认所有 secret-bearing 类型清单。4. 先改 prost Debug 派生与手写 impl，再改自有结构 impl。5. 清理 cache no-op 与对应测试。6. grep tracing/format/Debug 泄露面。7. 跑 cargo build/test。 |
| Concrete execution order | 1. 定位生成代码 include 与 prost-build 版本能力。2. 在 redaction 相关模块集中放 prost Debug impl，类型自有模块放本地 Debug impl。3. 添加 Debug redaction 测试。4. 删除 account_planner/route_client no-op cache 调用、函数和纯 no-op 测试。5. cargo fmt/build/test。6. 自查 git diff 与 grep。 |
