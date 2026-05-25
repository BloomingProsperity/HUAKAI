# 代码清理计划 (Claude)

- 日期: 2026-05-20
- 触发: Owner directive —— 清除遗留的 / 残废的 / 错误的 / 不具备参考价值的注释和
  垃圾代码, 含不符合规则的命名。
- 授权: Owner "你定就好" (CLAUDE.md #10 单决策委派) + "禁止偷懒, 用 sonnet 并行,
  必须交叉验证 + 反复测试"。

## 调查 (5 路交叉验证)
3 路语义调查 (Explore: backend / frontend / rust) + 2 路 sonnet 机械 grep 扫描。
结论: 代码库本身很干净 —— 无 commented-out 代码成灾、无非英文标识符、无死导出
代码。最大的垃圾是 backend 生产源码里约 130 处 codex 评审进度标记
(codex pass-N / chunk-N / review vN / N+Na pass-N), 属"不具备参考价值"的过程考古。

## 范围 (本批会做)
- Stream 1: 删 backend 注释里的 codex 评审进度标记 (约 100 处, 约 40 文件),
  保留实质注释体。
- Stream 2: 同样清理 frontend (codex review 标记) + rust (burn-the-boats 日期标记)。
- Stream 3: 修正真正错误 / 过时 / 无价值注释 (config.rs 把在用字段标"已废弃";
  redaction.rs docstring 与返回类型矛盾; field_matrix.go 分隔线噪音;
  sse.ts 废话注释等)。
- Stream 4: 局部命名小修 (frontend acc -> account 等)。

## 明确不做 (有理由, 非偷懒)
- panic("TODO") storm_controller.go / ErrNotImplemented adapters: Phase-E 真实功能
  桩, 非垃圾; 改它属功能改动且涉 auth 核心 (高风险), 另立决策。
- OCAW provider TODO (约 28 处): 诚实的"待采集真实流量后补全"延迟标记, 有参考
  价值, 符合 HUAKAI 透明原则, 删了反而隐藏未完成工作, 保留。
- magic string 抽常量 (28 串): 属重构非垃圾清理, 触及大量文件, 列为可选后续。
- 全仓英文注释 -> 中文: 属风格迁移非垃圾清理; 规则只强制新注释中文; 列为可选后续。
- mock 数据时间戳 / rust test #[allow(dead_code)]: 测试基建, 保留。

## 执行 + 验证
codex 分批改 (注释 only, 零功能风险), Claude 验 diff 仅注释 + go build +
go test ./... + codex review, 一包一 commit。反复测试: 每批后跑测试, 收尾全量。
