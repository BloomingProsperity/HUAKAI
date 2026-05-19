# 2026-05-08 U7-E FieldMatrix 字段级 Verdict Matrix Plan

| Field | Value |
| --- | --- |
| Owner directive | "HUAKAI Upgrade #7 U7-E atomic — FieldMatrix 字段级 verdict matrix" |
| Lane | codex / PLANNER |
| Time | 2026-05-08 |
| Output rule | 只写 plan，不写代码 |

## Scope

### In

- 为字段级能力判断新增 FieldMatrix：`(client_protocol, upstream_protocol, fieldName) -> verdict`。
- 定义字段 verdict 枚举：
  - `FieldPreserved`: 显式登记并保留。
  - `FieldTransformed`: 显式登记并转换，可在 metadata 中区分 lossy / lossless。
  - `FieldDropped`: 显式登记 known drop。
  - `FieldPreservedDefault`: 未登记字段按 PRESERVE-by-default 原则默认保留。
- 提供稳定 lookup API，用于 adapter / ops / tests 查询字段级 verdict。
- 覆盖 OpenAI-compatible 和 Anthropic-compatible 转换路径的最小登记样例。
- 添加单元测试，验证已登记字段、未登记字段、跨 client-upstream pair 边界。
- 保持现有 feature-level `CapabilityMatrix` 可用，FieldMatrix 与其并存。

### Out

- 不改 U7-A 到 U7-D 已合入的 passthrough envelope / extras merge 语义。
- 不扩大 adapter 行为面，不在 U7-E 内新增字段转换逻辑。
- 不把 feature-level `CapabilityMatrix` 替换为 FieldMatrix。
- 不引入数据库 schema、运行时配置中心、外部服务或动态热加载。
- 不触碰 auth、billing、quota、production secrets、LICENSE。

## Success Criteria

- 未登记字段 lookup 返回 `FieldPreservedDefault`，不是 `FieldUnsupported`、空 verdict 或错误。
- 已登记 preserved 字段返回 `FieldPreserved`。
- 已登记 transformed 字段返回 `FieldTransformed`，并能暴露转换说明 metadata。
- 已登记 dropped 字段返回 `FieldDropped`，并能暴露 drop reason。
- 相同字段名在不同 `(client_protocol, upstream_protocol)` pair 下可以返回不同 verdict，避免命名空间串线。
- 缺失 client/upstream pair 时仍按 preserve-by-default 返回 `FieldPreservedDefault`，除非未来明确引入 deny-by-default 模式。
- 现有 `CapabilityMatrix` tests 不回退；新增 FieldMatrix tests 可独立运行。
- public API 命名表达字段级含义，避免和 feature-level capability 混淆。

## Time Estimate

- 计划与设计确认：15-25 分钟。
- 实现 FieldMatrix types / lookup / initial map：45-75 分钟。
- 单元测试与边界 case：45-60 分钟。
- 本地测试与修正：20-40 分钟。
- 总 wall clock：约 2-3 小时。

## Blast Radius

- 主要影响范围：`capability_matrix.go` 所在 package 及其 tests。
- 低到中风险：新增并行结构，不替换现有 feature-level matrix，默认不会改变 adapter runtime 行为。
- 未来接入风险：如果 adapter 后续依赖 FieldMatrix 做运行时决策，错误登记会影响字段保留 / drop 可观测性。
- 文档风险：如果 matrix 登记不随 adapter 变化维护，会形成虚假的 parity 证明。

## Failure Modes

- Matrix 维护腐化：adapter 实际行为变化后，FieldMatrix 未同步，导致 docs / ops verdict 与真实 passthrough 行为不一致。
- 字段命名空间冲突：`tools`、`tool_choice`、`metadata` 等字段在不同 protocol 语义不同，仅按 `fieldName` 查询会误判。
- 默认保留被误读为显式支持：`FieldPreservedDefault` 是 passthrough default，不代表字段经过协议级语义验证。
- Transform 粒度不足：只标 `FieldTransformed` 但不记录 lossy/lossless/reason，会让审计无法判断功能是否缩水。
- Drop reason 缺失：known drop 没有 reason 会变成不可解释的功能缺口。
- 矩阵硬编码膨胀：Go map 初期简单可靠，但随着协议字段增加，review 成本会升高。
- 测试只测 happy path：如果不测跨 pair 边界，同名字段可能误用另一条 protocol mapping。

## Decision Points

### Hardcoded Go Map vs JSON Spec

Recommendation: U7-E atomic 使用 hardcoded Go map。

Reason:
- 当前目标是小而原子的字段级 lookup，不需要外部动态配置。
- Go map 能获得编译期 enum / type 检查，tests 直接覆盖。
- 避免引入 JSON schema、loader、validation、deployment packaging 的额外 blast radius。

Deferred option:
- 后续当字段登记量变大，或 ops UI 需要非代码化展示时，可从 Go map 生成 JSON artifact，或迁移到 checked-in JSON spec + Go validator。

### 是否每条登记需带 reason 字符串

Recommendation: 是，显式登记项应带 reason；`FieldPreservedDefault` 不需要登记 reason，但 lookup 可返回默认 reason。

Reason:
- `FieldDropped` 没有 reason 不满足 truth-first / feature preservation 审计需要。
- `FieldTransformed` 需要说明转换性质，至少区分 `lossless` / `lossy`。
- `FieldPreserved` 的 reason 可简短记录与 U7-A/U7-B/U7-C/U7-D 的关系，例如 explicit passthrough supported。

Minimum metadata:
- `Reason string`
- `TransformKind string` or enum: empty / `lossless` / `lossy`
- optional `Source string` for internal citation, if project style已有类似字段。

## Design Outline

### Candidate Files

- `backend/.../capability_matrix.go` or current existing `capability_matrix.go` path:
  - Add field-level types and lookup next to existing capability-level matrix if same package owns protocol capability decisions.
- `backend/.../capability_matrix_test.go` or new `field_matrix_test.go`:
  - Keep tests close to matrix definitions.
- `docs/...` only if existing U7 docs require matrix update:
  - Record FieldMatrix purpose and preserve-by-default semantics.

Exact path should be confirmed by `rg --files | rg 'capability_matrix\.go$'` before implementation.

### Types

Proposed Go types:

```go
type FieldVerdict string

const (
    FieldPreserved        FieldVerdict = "preserved"
    FieldTransformed      FieldVerdict = "transformed"
    FieldDropped          FieldVerdict = "dropped"
    FieldPreservedDefault FieldVerdict = "preserved_default"
)

type FieldTransformKind string

const (
    FieldTransformNone     FieldTransformKind = ""
    FieldTransformLossless FieldTransformKind = "lossless"
    FieldTransformLossy    FieldTransformKind = "lossy"
)

type FieldMatrixKey struct {
    ClientProtocol   Protocol
    UpstreamProtocol Protocol
    FieldName        string
}

type FieldMatrixEntry struct {
    Verdict       FieldVerdict
    TransformKind FieldTransformKind
    Reason        string
}
```

If existing code uses string protocol identifiers instead of `Protocol`, reuse the existing protocol type. Do not introduce a second protocol enum unless unavoidable.

### Functions

Proposed API:

```go
func LookupFieldVerdict(client Protocol, upstream Protocol, fieldName string) FieldMatrixEntry
```

Behavior:
- Normalize only protocol identifiers if existing matrix already normalizes them.
- Do not silently normalize field names across protocol casing unless existing adapter semantics require it.
- If exact key exists, return registered entry.
- If no exact key exists, return:

```go
FieldMatrixEntry{
    Verdict: FieldPreservedDefault,
    Reason: "unregistered field preserved by passthrough default",
}
```

Optional helper:

```go
func IsExplicitFieldVerdict(entry FieldMatrixEntry) bool
```

This helps ops or tests distinguish explicit known behavior from default passthrough.

### Initial Registration Shape

Start with a small explicit map for fields whose behavior is already established by U7-A to U7-D:

- OpenAI client -> OpenAI-compatible upstream:
  - known preserved request fields that passthrough extras now protects.
  - known transformed fields if OpenAI adapter maps internal HCSF fields to upstream-specific names.
  - known dropped fields only if U7-C tests or code explicitly demonstrate a drop.
- Anthropic client -> Anthropic-compatible upstream:
  - known preserved request fields covered by U7-D passthrough.
  - known transformed fields where HCSF to Anthropic conversion changes names or shape.
  - known dropped fields only if current code has explicit drop behavior.
- Bedrock-on-Anthropic:
  - only register if current adapter path has a distinct upstream protocol enum; otherwise document that it inherits Anthropic-compatible behavior through the same pair.

Do not invent registration claims from memory. Each explicit entry should be backed by current HUAKAI code/tests or existing U7 docs.

## Test Matrix

### Registered Lookup

- Given an explicitly registered preserved field for a concrete pair:
  - lookup returns `FieldPreserved`.
  - reason is non-empty.
  - transform kind is empty / none.
- Given an explicitly registered transformed field:
  - lookup returns `FieldTransformed`.
  - transform kind is `lossless` or `lossy`.
  - reason is non-empty.
- Given an explicitly registered dropped field:
  - lookup returns `FieldDropped`.
  - reason is non-empty.

### Unregistered Lookup

- Given an unknown field name under a known client/upstream pair:
  - lookup returns `FieldPreservedDefault`.
  - no error.
  - reason identifies preserve-by-default.
- Given a known field name under an unregistered protocol pair:
  - lookup returns `FieldPreservedDefault`.
  - does not borrow verdict from another pair.
- Given a completely unknown field under a completely unknown but typed protocol value if the code permits it:
  - lookup returns `FieldPreservedDefault` or validates protocol according to existing project convention.

### Cross Pair Boundaries

- Same field name registered as transformed for OpenAI -> Anthropic must not affect OpenAI -> OpenAI-compatible.
- Same field name registered as dropped for one upstream must not affect Anthropic -> Anthropic-compatible if not explicitly registered there.
- Client/upstream order matters:
  - `client=openai, upstream=anthropic` and `client=anthropic, upstream=openai` are different keys.

### Regression With CapabilityMatrix

- Existing feature-level `CapabilityMatrix` tests continue to pass.
- New FieldMatrix tests do not assert feature-level support like `tool_use`; they assert field-level verdict only.

## Relationship To Existing CapabilityMatrix

FieldMatrix should coexist with `CapabilityMatrix`, not replace it.

- `CapabilityMatrix`: answers coarse feature support questions, for example `text_streaming`, `tool_use`, `json_schema`, etc.
- `FieldMatrix`: answers field preservation / transform / drop behavior for a specific protocol pair and field name.
- A feature can be supported while some fields are transformed or dropped.
- A field can be `FieldPreservedDefault` without implying the corresponding high-level feature is semantically supported.
- Future ops UI can display both:
  - feature-level capability status for product planning.
  - field-level verdict for protocol compatibility and debugging.

## References

- Project instruction source: `/home/codex/HUAKAI/AGENTS.md`
- Existing feature-level matrix: current `capability_matrix.go` in HUAKAI repo, exact path to be confirmed before implementation.
- U7-A background: PassthroughEnvelope + `UnmarshalWithExtras` / `MergeExtrasInto` helpers.
- U7-B background: HCSF three primary types gained `Passthrough` fields.
- U7-C background: OpenAI adapter integrated passthrough behavior for OpenAI-compatible families.
- U7-D background: Anthropic adapter integrated passthrough behavior, including Bedrock-on-Anthropic benefit.
- Clean-room rule: this plan relies on HUAKAI internal code/docs and provided Owner context, not non-MIT reference source.

## Pre-Execution Checklist

- Confirm Owner has approved synthesized U7-E plan after cross-discussion.
- Locate existing `capability_matrix.go` and tests with `rg`.
- Read existing protocol enum / capability matrix naming conventions.
- Confirm whether current package prefers table-driven tests.
- Add hardcoded Go FieldMatrix and lookup API.
- Add tests for registered, default, and cross-pair behavior.
- Run targeted Go tests.
- If committing, stage changes and run `codex exec review --uncommitted --full-auto` per project rule.

## Owner Confirmation Needed

- Confirm hardcoded Go map is acceptable for U7-E atomic, with JSON/spec generation deferred.
- Confirm explicit entries require non-empty reason strings.
- Confirm whether transformed entries must distinguish lossy vs lossless in U7-E or can be optional metadata.
- Confirm whether docs update is required in the same atomic slice or can follow after implementation.

## Chinese Summary

本计划只覆盖 U7-E 的字段级 FieldMatrix 设计，不写实现代码。建议在现有 feature-level `CapabilityMatrix` 旁边新增并存的字段级 lookup：显式登记返回 preserved/transformed/dropped，未登记字段按 PRESERVE-by-default 返回 `FieldPreservedDefault`，避免把未知字段误判为 unsupported。主要风险是矩阵维护腐化、同名字段跨协议串线、以及默认保留被误读成显式支持；计划通过 protocol pair + fieldName 三元 key、reason metadata、lossy/lossless 标注和跨 pair 测试控制风险。没有功能缩水；没有读取或复制非 MIT 参考项目源码，因此 clean-room 风险低。需要 Owner 确认 hardcoded Go map vs JSON spec、登记项是否强制 reason、以及 transformed 是否必须带 lossy/lossless。
