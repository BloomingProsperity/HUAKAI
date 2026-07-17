# 2026-07-15 并发缺陷修复增量 A 实施报告

## 结论

缺陷 1 已按综合裁定修复：`Reserve` 在 reservation 读取、新 claim 策略读取、released/expired 复活策略读取三个位置遇到 PostgreSQL `40001`/`40P01` 时，不再构造 `quota_fail_closed`，而是把原错误交给既有外层整事务重试环。普通错误与非重试 SQLSTATE 仍保持一次事务内 fail-closed；schema、对外错误码、3 次重试预算和默认开关均未改变。

实施会话只读取 HUAKAI 本仓库，没有读取 `~/refs/` 或任何参考项目源码。

## 修改文件

- `backend/internal/quota/service.go`：增加三个重试交接 guard 和一个只返回布尔值的本地 helper。
- `backend/internal/quota/service_reserve_retry_test.go`：新增 AT-CD1-001..005 判别性 unit 测试；覆盖两个可重试 SQLSTATE、三个错误点位、预算耗尽、普通错误与 `23514` 负集合，并精确断言结果、决策码、事务次数、reservation/window/audit 次数。
- `backend/internal/quota/service_reserve_retry_integration_test.go`：新增 AT-CD1-006 真 PG 测试；通过测试用 `beginTx` 包装器建立 Serializable 旧快照，再并发更新 reservation，要求第一次 `SELECT ... FOR UPDATE` 产生真实 `40001`、第二个事务成功复用。
- `docs/process/reviews/2026-07-15-concurrency-defects-increment-a.md`：本报告。

`service.go` 是 codebudget 既有基线项（基线 892 行），修复后 904 行，增长约 1.35%，未超过基线 +5% 余量；两个新增测试文件分别为 368/103 行。为遵守“生产改动只限 `service.go`”，没有借本增量搬动其它生产逻辑。`internal/codebudget` 门已通过。

## 计划歧义处理

Codex 计划 AT-CD1-003 表格写了复活成功 `IdempotencyHit=false`，但当前生产契约和既有真 PG 测试明确把同一 reservation 的复活标记为 `IdempotencyHit=true`。综合裁定同时要求“其余行为不变”，因此新增测试精确断言既有值 `true`，没有在本缺陷修复中改变幂等语义。

## 测试环境

指定的 `GOCACHE=/home/ubuntu/HUAKAI/.gocache` 可用。沙箱禁止写入指定的 `GOTMPDIR=/home/ubuntu/.gotmp`，首次预检在编译前失败，原始输出如下；它不计作代码 RED：

```text
go: creating work dir: mkdir /home/ubuntu/.gotmp/go-build3487500127: read-only file system
EXIT_CODE=1
```

后续命令仅将 `GOTMPDIR` 回退为沙箱可写的 `/tmp/huakai-gotmp`，`GOCACHE` 保持指定值。

## 测试先行：未修复代码的 RED 原始输出

命令：

```text
env GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/tmp/huakai-gotmp go test ./internal/quota/ -run 'TestServiceReserve_ATCD100[1-5]' -count=1
```

原始输出：

```text
--- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict/40001 (0.00s)
        service_reserve_retry_test.go:31: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict/40P01 (0.00s)
        service_reserve_retry_test.go:31: Reserve err=quota: denied: quota_fail_closed; want nil
--- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict/40001 (0.00s)
        service_reserve_retry_test.go:46: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict/40P01 (0.00s)
        service_reserve_retry_test.go:46: Reserve err=quota: denied: quota_fail_closed; want nil
--- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/released_40001 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/released_40P01 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/expired_40001 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/expired_40P01 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
--- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion (0.00s)
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/reservation_read_40001 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/reservation_read_40P01 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/new_policy_read_40001 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/new_policy_read_40P01 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/revive_policy_read_40001 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
    --- FAIL: TestServiceReserve_ATCD1004_RetryableConflictExhaustion/revive_policy_read_40P01 (0.00s)
        service_reserve_retry_test.go:91: err=quota: denied: quota_fail_closed; want retryable=true denied=false
FAIL
FAIL	github.com/BloomingProsperity/HUAKAI/internal/quota	0.004s
FAIL
EXIT_CODE=1
```

AT-CD1-005 在同一轮保持通过，证明普通错误和 `23514` 没有被测试误要求为可重试。

## 实现后的 GREEN

AT-CD1-001..005 首轮 GREEN：

```text
ok  	github.com/BloomingProsperity/HUAKAI/internal/quota	0.184s
EXIT_CODE=0
```

三次变异恢复后的再次 GREEN：

```text
ok  	github.com/BloomingProsperity/HUAKAI/internal/quota	0.180s
EXIT_CODE=0
```

## 亲手变异记录

每轮均先用 `cp` 备份正确的 `service.go`，只删除对应 guard，运行单点测试，然后用 `cp` 恢复。三轮恢复后源码与备份 SHA-256 均一致：

```text
afb7d080001235d448ca4027396e5d58645a7ea17d7025da945939e2122678cd
```

### 变异 1：删除 reservation 读取 guard

```text
--- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict/40001 (0.00s)
        service_reserve_retry_test.go:31: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1001_RetryableReservationReadConflict/40P01 (0.00s)
        service_reserve_retry_test.go:31: Reserve err=quota: denied: quota_fail_closed; want nil
FAIL
FAIL	github.com/BloomingProsperity/HUAKAI/internal/quota	0.003s
FAIL
EXIT_CODE=1
```

### 变异 2：删除新 claim 策略读取 guard

```text
--- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict/40001 (0.00s)
        service_reserve_retry_test.go:46: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1002_RetryableNewPolicyConflict/40P01 (0.00s)
        service_reserve_retry_test.go:46: Reserve err=quota: denied: quota_fail_closed; want nil
FAIL
FAIL	github.com/BloomingProsperity/HUAKAI/internal/quota	0.003s
FAIL
EXIT_CODE=1
```

### 变异 3：删除复活路径策略读取 guard

```text
--- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict (0.00s)
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/released_40001 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/released_40P01 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/expired_40001 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
    --- FAIL: TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict/expired_40P01 (0.00s)
        service_reserve_retry_test.go:62: Reserve err=quota: denied: quota_fail_closed; want nil
FAIL
FAIL	github.com/BloomingProsperity/HUAKAI/internal/quota	0.003s
FAIL
EXIT_CODE=1
```

## 最终门禁

- `go test ./internal/quota/`：

  ```text
  ok  	github.com/BloomingProsperity/HUAKAI/internal/quota	0.285s
  EXIT_CODE=0
  ```

- `go test -tags integration_pg ./internal/quota/ -run '^$' -count=1`（仅编译，不运行真 PG）：

  ```text
  ok  	github.com/BloomingProsperity/HUAKAI/internal/quota	0.003s [no tests to run]
  EXIT_CODE=0
  ```

- `go vet ./internal/quota/`：无 stdout，`EXIT_CODE=0`。
- `go test ./internal/codebudget/ -count=1`：

  ```text
  ok  	github.com/BloomingProsperity/HUAKAI/internal/codebudget	0.046s
  EXIT_CODE=0
  ```

AT-CD1-006 按指令没有在沙箱运行；需由 Claude 在有 PostgreSQL socket/DSN 的本机执行。

## Owner 汇报

1. **做了什么**：修复 quota Reserve 三个瞬时事务冲突吞错点，并新增 AT-CD1-001..006。
2. **改了哪些文件**：见“修改文件”四项。
3. **为什么这样做**：只有把 `40001`/`40P01` 原样退出当前事务，既有外层循环才能以新事务重跑完整 Reserve。
4. **有没有功能缩水**：没有；普通错误 fail-closed、重试预算、错误码、默认开关和复活幂等语义均保持。
5. **有没有 clean-room 风险**：没有；未读取参考项目源码，代码和测试均只依据 HUAKAI 本地计划与实现。
6. **有没有安全风险**：未扩大重试错误集合；主要风险是未来新增吞错点漏接，统一 helper 与三点变异测试已降低该风险。
7. **哪些地方需要 Owner 确认**：本增量无新增产品决策；AT-CD1-003 的计划文字歧义已按既有契约保守处理。
8. **下一步建议**：Claude 本机运行 AT-CD1-006 真 PG 测试，再进入综合计划增量 B/C。
