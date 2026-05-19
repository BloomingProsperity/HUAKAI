# 2026-05-08 Upgrade #7 U7-E — 双 lane 综合

## 双 lane 输入

- claude lane: `docs/process/plans/2026-05-08-upgrade7-passthrough-claude.md` §"U7-E"段
- codex lane: `docs/process/plans/2026-05-08-upgrade7-u7e-codex.md` (codex bg lane via 修复
  模板首次成功输出 13KB plan.md)

## 一致点

1. ✅ 字段级 matrix 与 feature-level CapabilityMatrix 并存，不替换
2. ✅ Hardcoded Go map（不引 JSON spec / 外部加载）
3. ✅ 未登记字段返回 `FieldPreservedDefault`（PRESERVE-by-default 是核心契约）
4. ✅ 跨 (client, upstream) pair 不串线（同名字段不互借 verdict）
5. ✅ verdict 枚举: Preserved / Transformed / Dropped / PreservedDefault

## 差异 + synthesis 决策

| 项 | claude lane 初版 | codex lane | 综合采纳 | 理由 |
|---|---|---|---|---|
| Entry 类型 | 仅 `FieldVerdict` enum | `FieldMatrixEntry{Verdict, TransformKind, Reason}` 结构 | **codex** | Reason 字符串对运维可观测有价值；TransformKind lossy/lossless 区分对 compliance audit 必需 |
| TransformKind 区分 | 无 | `Lossless` / `Lossy` | **codex** | OpenAI finish_reason → CanonicalStopReason 多对一是真 lossy；Anthropic stop_reason 全映射是 lossless |
| API 形态 | 单 Lookup 返 verdict | Lookup 返 entry + IsExplicit helper | **混合** | 主 API `Lookup(...)→FieldMatrixEntry`（带 metadata）+ 短路 `LookupVerdict(...)→FieldVerdict`（callers 只要 verdict 时用）+ `HasEntry` 区分已登记 vs 默认 |
| key 形态 | 嵌套 `map[client]map[upstream]map[field]Entry` | 单 `FieldMatrixKey{Client,Upstream,FieldName}` flat key | **claude** | 嵌套 map 在 Go 同等访问效率，登记时按 (client,upstream) 分组更清晰可读；codex flat key 没有结构优势 |
| 每条登记必带 reason | 未要求 | 要求 | **codex** | 测试 `TestFieldMatrix_LookupKnownField` 强制每条 Reason 非空 |
| Transformed 必带 lossy/lossless | 未要求 | 要求 | **codex** | 测试 `TestFieldMatrix_TransformedEntriesAlwaysHaveTransformKind` 守约 |

## 实施产物

- `backend/internal/proto/field_matrix.go` (~180 LoC)
  - `FieldVerdict` 4 值枚举
  - `FieldTransformKind` 3 值枚举（None/Lossless/Lossy）
  - `FieldMatrixEntry{Verdict, TransformKind, Reason}` 结构
  - `FieldMatrix` 嵌套 map 类型
  - `Lookup` / `LookupVerdict` / `HasEntry` 三 API
  - `DefaultFieldMatrix()` 已知字段登记（OpenAI + Anthropic + Bedrock-on-Anthropic 三对）

- `backend/internal/proto/field_matrix_test.go` (~150 LoC, 9 test functions)
  - `LookupKnownField`: 12 已登记字段验证 verdict + transform kind + reason
  - `UnknownField_PreservedDefault`: 4 未登记字段返回默认
  - `LookupVerdict_ShortcutEquivalentToLookup`
  - `UnregisteredClientUpstreamPair`: cross-pair 边界
  - `HasEntry`: 区分已登记 vs 默认
  - `NilSafe`: 空 matrix
  - `RegisteredEntriesCoverAllClientsThatHaveAdapters`: 守界
  - `TransformedEntriesAlwaysHaveTransformKind`: codex 提出的 invariant

测试: `go test ./internal/proto/... -race -count=1` 全过；`go test ./...` 全过。

## 完成 #7 整体

| atomic | commit | 内容 |
|---|---|---|
| U7-A | a1543af | PassthroughEnvelope + helpers |
| U7-B | b3acbfa | HCSF 三类型加 Passthrough 字段 |
| U7-C | 7e17a97 | OpenAI adapter wire-up（12 family 受益） |
| U7-D | 2dd6fc5 | Anthropic adapter wire-up（Bedrock 自动受益） |
| U7-E | (待 commit) | FieldMatrix 字段级 verdict + 运维可观测 |

## 引用源

- HUAKAI 内部：proto package 已有 / 已合 atomic
- 外部思路启发：portkey extras 字段透传（不 verbatim 复制）
- 严禁读 sub2api / new-api / litellm 等 reference 实现源码（CLAUDE.md #11）
