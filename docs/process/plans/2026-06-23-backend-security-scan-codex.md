# 2026-06-23 backend security scan Codex plan

| Owner directive | `/goal` 要求读取 `goal-objective.md` 后，对 HUAKAI `backend/` 做基于源码核实的安全审查：逐子系统核实真实可利用漏洞，每条发现必须有 `file:line` 证据、可利用路径、严重度与具体修法。 |
| Scope | 仅做 Codex 侧独立计划与后续安全审查；主要范围是 `/home/ubuntu/HUAKAI/backend`。必要时只读取支撑证据的根目录配置、CI、compose、docs 规则与安全扫描产物。 |
| Out of scope | 不改业务代码、不改数据库 schema、不碰 `LICENSE`、不处理其它 agent 的目标、不清理现有未跟踪文件、不读取或复制非 MIT 借鉴项目源码。 |
| Success criteria | 产出中文安全审查报告，按 S0/S1/S2/S3 分组；每条发现具备实际读取过的 `file:line` 证据、可达性分析、影响、修法；末尾包含复核确认清单、设计取舍、覆盖盲区。若走 codex-security 扫描流程，还需保留 scan artifact 与 coverage ledger。 |
| Time estimate | 计划写作 10 分钟；preflight 与威胁模型 30-60 分钟；分子系统发现与验证至少数小时，视子系统数量与可跑测试情况延长。 |
| Blast radius | 计划阶段只新增本文档。执行阶段以只读审查为主；若后续 Owner 要求修复，才进入单独小补丁计划和 per-commit review。 |
| Failure modes | 误信旧文档导致假阳性：以源码和测试为准；扫描范围过大导致覆盖不诚实：用 coverage ledger 标出已核实、压制、不可适用、延期；缺少 DB/容器导致无法实跑：列入覆盖盲区；发现涉及高风险改动：只报告修法，不直接修改。 |
| Decision points | 是否允许启动完整 codex-security 四阶段扫描；是否允许使用 multi-agent worker；是否允许联网或拉取最新分支；是否对 S0/S1 发现进入修复阶段。 |
| Pre-execution checklist | 1. 保持与其它目标隔离；2. 读取 `docs/RULES.md` 与本计划；3. 跑 codex-security `security_scan` profile preflight；4. 解析 scan artifact 路径；5. 建立或复用本扫描自己的 goal；6. 严格按 threat-model → finding-discovery → validation → attack-path-analysis → final output 顺序执行。 |

## 独立性说明

这是 Codex 独立计划。写作前未读取任何同名 `claude` 安全扫描计划；若 Claude 另行产出计划，后续应由 Owner 或 PM 汇总双方计划的 agreements、conflicts、gaps，再形成合成执行计划。

## 规则约束

- 全程中文汇报；英文路径、函数名、类型名、SQL 关键字保留原文。
- 每个安全结论必须来自实际打开过的源码行；文档只能作为线索，不作为实现状态证据。
- 不做参考项目源码比对，不复制外部项目标识符、注释、结构或实现。
- 区分可达漏洞、设计取舍、测试盲区；不可达风险不升级成 S0/S1。
- money、鉴权、租户隔离、SSRF、审计完整性、凭据明文是最高优先级。
- 不修改高风险文件；若修复建议涉及 auth core、billing ledger、quota enforcement、schema、deployment、真实 secret，必须另行请求 Owner 确认。

## 执行顺序

1. **Capability preflight**
   - 用 codex-security `config_preflight.py --profile security_scan` 检查当前 CLI/工具能力。
   - 若结果 `blocked` 或 `incomplete`，先把具体阻塞和可选修复报给 Owner，不伪称完成完整扫描。

2. **扫描目录与证据账本**
   - 采用 codex-security 默认路径：`$TMPDIR/codex-security-scans/HUAKAI/<scan_id>/`。
   - 写入 threat model、discovery report、coverage ledger、候选发现 ledger、validation report、attack path report。

3. **Threat model**
   - 以 `goal-objective.md` 的资产和子系统列表为种子。
   - 核对真实入口、认证边界、money 写点、凭据出口、审计链、DoS 面。

4. **Finding discovery**
   - 先按目标文件点名的 A-K 子系统建立检查清单。
   - 使用 `rg` 定位关键写点，例如 `UPDATE provider_accounts ... WHERE id=`、`user_balances` 正向 credit、`tenant_id` 请求体来源、SSRF guard、cache key、rate limiter。
   - 每个候选发现必须记录入口、最近控制点、sink、影响和反证需要。

5. **Validation**
   - 对候选发现逐条回读真实源码与测试。
   - 能跑的单元测试、静态 grep、必要的只读 SQL/迁移检查都跑；需要 DB/容器才可确认的项标成覆盖盲区。
   - 有前置约束或上游 fail-closed 兜底时降级或压制。

6. **Attack-path analysis**
   - 仅对 validation 存活项写利用路径。
   - 明确攻击者身份、前置条件、触发请求或状态、影响范围、租户/用户/资金/凭据边界。

7. **Final output**
   - 中文报告按 S0 → S1 → S2 → S3 分组。
   - 每条发现包含标题、严重度与不变量编号、`file:line` 证据、关键片段、可利用路径、具体修法。
   - 末尾附复核确认清单、设计取舍、覆盖盲区。

## 第一批重点核实点

- `audit_verify_handler` 是否公开披露 `hop_chain`、`model_chain` 或账号哈希。
- `provider_accounts.tenant_id` DDL 是否阻止 `tenant_id <= 0`，以及 vault 是否仅靠 Go 层弱守卫。
- `admin_credentials_handler` 是否允许 scoped platform admin 通过 JSON body 跨租户写凭据。
- refund worker 的 DLQ payload 是否能覆盖租户导致跨租户退款。
- payment 回调 `out_trade_no` 与 body `tenant_id` 是否可伪造跨租户入账。
- `MarkTOTPSuccess` 是否用原子 `last_used_step < step` 防 TOTP 重放。
- L2 cache key 是否包含 tenant/scope。
- `key_rate_limit_selector` 是否默认关闭且与新 key 默认限流预期冲突。
- `ssrfpolicy` 与 passthrough endpoint guard 是否默认拒私网并做解析时和 dial 时双复核。
- Go uTLS 是否与“不做 H2、锁 H1”出口决策一致。

## 停止条件

- preflight 阻塞且无法在不改配置的前提下继续时，停止并报告。
- 发现需要高风险修改时，只报告，不直接修。
- 若覆盖无法诚实完成，报告已核实范围与未覆盖范围，不用推测补齐。
