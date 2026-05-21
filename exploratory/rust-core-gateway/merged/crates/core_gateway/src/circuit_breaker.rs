// route_query 熔断器状态机。
// Closed 正常放行, Open 快速失败, HalfOpen 只允许一个探测。
//
// 实现以一把 Mutex 串行化全部状态读写: 熔断器每次 route query 仅访问两次
// (获取 permit + 结算), 临界区只有几条指令, 紧随其后是毫秒级 gRPC 调用 ——
// 锁开销可忽略。改用 Mutex 而非无锁多原子, 是为了让"相位 / 代次 / 时限"始终
// 一致, 从根上杜绝 CAS 时序竞态。
//
// 代次(generation): 每次相位转换自增。permit 携带获取时的代次, 结算时代次
// 不符即作废 —— 既丢弃陈旧的 Closed 失败(避免旧请求的延迟失败掀翻刚恢复的
// 熔断器), 也丢弃被回收的探测(避免被取消的探测错误地改写新一轮状态)。

use std::{sync::Mutex, time::Duration};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Phase {
    Closed,
    Open,
    HalfOpen,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum CircuitPermit {
    Closed { generation: u64 },
    HalfOpenProbe { generation: u64 },
}

impl CircuitPermit {
    fn generation(self) -> u64 {
        match self {
            CircuitPermit::Closed { generation } | CircuitPermit::HalfOpenProbe { generation } => {
                generation
            }
        }
    }
}

#[derive(Debug)]
pub(crate) struct CircuitBreakerOpen;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct CircuitOpenSnapshot {
    pub failures: u32,
    pub open_until_ms: u64,
    pub probe_failure: bool,
}

// 受 Mutex 保护的全部可变状态; 字段只能在持锁时读写。
struct CircuitState {
    phase: Phase,
    consecutive_failures: u32,
    open_until_ms: u64,
    // HalfOpen 探测名额的回收时限; 到点后未结算的探测视为被取消, 名额可被回收。
    half_open_deadline_ms: u64,
    generation: u64,
}

pub(crate) struct CircuitBreaker {
    state: Mutex<CircuitState>,
    failure_threshold: u32,
    cooldown_ms: u64,
    // HalfOpen 探测名额的回收时长; 取自探测 RPC 超时上限, 见 new()。
    probe_reclaim_ms: u64,
}

impl CircuitBreaker {
    /// `probe_timeout` 应取实际探测 RPC 的超时上限(route_client 的 rpc_timeout);
    /// HalfOpen 探测名额的回收时限据此设定, 确保仍在飞的探测不会被误回收。
    pub(crate) fn new(failure_threshold: u32, cooldown: Duration, probe_timeout: Duration) -> Self {
        let cooldown_ms = duration_millis_u64(cooldown);
        Self {
            state: Mutex::new(CircuitState {
                phase: Phase::Closed,
                consecutive_failures: 0,
                open_until_ms: 0,
                half_open_deadline_ms: 0,
                generation: 0,
            }),
            failure_threshold: failure_threshold.max(1),
            cooldown_ms,
            // 回收时长取探测超时的 2 倍, 并以 cooldown 兜底: 正常完成或超时的探测
            // (耗时 <= probe_timeout)绝不会被误回收, 只有被取消而永不结算的探测才会。
            probe_reclaim_ms: duration_millis_u64(probe_timeout)
                .saturating_mul(2)
                .max(cooldown_ms),
        }
    }

    // 互斥锁中毒不影响熔断判定(状态无完整性不变量), 直接恢复使用。
    fn lock(&self) -> std::sync::MutexGuard<'_, CircuitState> {
        self.state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    pub(crate) fn try_acquire(&self, now_ms: u64) -> Result<CircuitPermit, CircuitBreakerOpen> {
        let mut state = self.lock();
        match state.phase {
            Phase::Closed => Ok(CircuitPermit::Closed {
                generation: state.generation,
            }),
            Phase::Open => {
                if now_ms < state.open_until_ms {
                    return Err(CircuitBreakerOpen);
                }
                // 冷却到点: 本调用成为唯一探测。
                Ok(self.enter_half_open(&mut state, now_ms))
            }
            Phase::HalfOpen => {
                if now_ms < state.half_open_deadline_ms {
                    // 探测仍在回收时限内 → 名额有效, 其它请求快速失败。
                    return Err(CircuitBreakerOpen);
                }
                // 探测超时仍未结算(多半是请求被取消) → 回收名额, 本调用成为新探测。
                Ok(self.enter_half_open(&mut state, now_ms))
            }
        }
    }

    // 进入 HalfOpen 并把当前调用方作为唯一探测返回。调用方已持锁。
    fn enter_half_open(&self, state: &mut CircuitState, now_ms: u64) -> CircuitPermit {
        state.phase = Phase::HalfOpen;
        state.half_open_deadline_ms = now_ms.saturating_add(self.probe_reclaim_ms);
        state.generation = state.generation.wrapping_add(1);
        CircuitPermit::HalfOpenProbe {
            generation: state.generation,
        }
    }

    pub(crate) fn record_success(&self, permit: CircuitPermit) {
        let mut state = self.lock();
        // 代次不符 → permit 所属的那一轮已被后续转换取代, 不参与结算。
        if permit.generation() != state.generation {
            return;
        }
        match permit {
            // 代次相符即说明仍在同一 Closed 代次, 直接清零失败计数。
            CircuitPermit::Closed { .. } => {
                state.consecutive_failures = 0;
                state.open_until_ms = 0;
            }
            CircuitPermit::HalfOpenProbe { .. } => self.close(&mut state),
        }
    }

    pub(crate) fn record_retryable_failure(
        &self,
        permit: CircuitPermit,
        now_ms: u64,
    ) -> Option<CircuitOpenSnapshot> {
        let mut state = self.lock();
        // 代次不符 → 陈旧失败, 不再累加, 避免掀翻刚恢复的熔断器。
        if permit.generation() != state.generation {
            return None;
        }
        match permit {
            CircuitPermit::Closed { .. } => {
                state.consecutive_failures = state.consecutive_failures.saturating_add(1);
                if state.consecutive_failures >= self.failure_threshold {
                    Some(self.open(&mut state, now_ms, false))
                } else {
                    None
                }
            }
            CircuitPermit::HalfOpenProbe { .. } => {
                state.consecutive_failures = state.consecutive_failures.saturating_add(1);
                Some(self.open(&mut state, now_ms, true))
            }
        }
    }

    pub(crate) fn is_rejecting(&self, now_ms: u64) -> bool {
        let state = self.lock();
        match state.phase {
            Phase::Closed => false,
            Phase::Open => now_ms < state.open_until_ms,
            Phase::HalfOpen => now_ms < state.half_open_deadline_ms,
        }
    }

    pub(crate) fn consecutive_failures(&self) -> u32 {
        self.lock().consecutive_failures
    }

    /// 持有 `permit` 的请求是否可以再发起一次尝试。
    /// HalfOpen 探测只有一次机会; Closed permit 仅在仍属当前代次时可重试 ——
    /// 熔断器一旦转换, 该请求就不再是"已放行"流量, 重试必须停, 以免绕过单探测门控。
    pub(crate) fn may_retry(&self, permit: CircuitPermit) -> bool {
        match permit {
            CircuitPermit::Closed { generation } => generation == self.lock().generation,
            CircuitPermit::HalfOpenProbe { .. } => false,
        }
    }

    // 打开熔断器并自增代次。调用方已持锁。
    fn open(
        &self,
        state: &mut CircuitState,
        now_ms: u64,
        probe_failure: bool,
    ) -> CircuitOpenSnapshot {
        let open_until_ms = now_ms.saturating_add(self.cooldown_ms);
        state.phase = Phase::Open;
        state.open_until_ms = open_until_ms;
        state.half_open_deadline_ms = 0;
        state.generation = state.generation.wrapping_add(1);
        CircuitOpenSnapshot {
            failures: state.consecutive_failures,
            open_until_ms,
            probe_failure,
        }
    }

    // 闭合熔断器并自增代次。调用方已持锁。
    fn close(&self, state: &mut CircuitState) {
        state.phase = Phase::Closed;
        state.consecutive_failures = 0;
        state.open_until_ms = 0;
        state.half_open_deadline_ms = 0;
        state.generation = state.generation.wrapping_add(1);
    }
}

fn duration_millis_u64(duration: Duration) -> u64 {
    duration.as_millis().min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    fn breaker(threshold: u32, cooldown_ms: u64, probe_ms: u64) -> CircuitBreaker {
        CircuitBreaker::new(
            threshold,
            Duration::from_millis(cooldown_ms),
            Duration::from_millis(probe_ms),
        )
    }

    #[test]
    fn closed_stays_closed_below_threshold() {
        let breaker = breaker(2, 100, 50);
        let permit = breaker.try_acquire(1).expect("closed 应放行");

        assert!(breaker.record_retryable_failure(permit, 1).is_none());
        assert_eq!(breaker.consecutive_failures(), 1);
        assert!(!breaker.is_rejecting(2));
    }

    #[test]
    fn threshold_failure_opens_until_cooldown() {
        let breaker = breaker(1, 100, 50);
        let permit = breaker.try_acquire(10).expect("closed 应放行");
        let opened = breaker
            .record_retryable_failure(permit, 10)
            .expect("达到阈值应打开");

        assert_eq!(opened.failures, 1);
        assert_eq!(opened.open_until_ms, 110);
        assert!(breaker.is_rejecting(109));
    }

    #[test]
    fn cooldown_allows_exactly_one_half_open_probe() {
        let breaker = breaker(1, 100, 50);
        let permit = breaker.try_acquire(10).expect("closed 应放行");
        breaker.record_retryable_failure(permit, 10);

        let probe = breaker.try_acquire(110).expect("冷却到点应放一个探测");
        assert!(matches!(probe, CircuitPermit::HalfOpenProbe { .. }));
        assert!(
            breaker.try_acquire(110).is_err(),
            "探测在飞时其它请求应拒绝"
        );
    }

    #[test]
    fn half_open_probe_success_closes_and_resets_failures() {
        let breaker = breaker(1, 100, 50);
        let permit = breaker.try_acquire(10).expect("closed 应放行");
        breaker.record_retryable_failure(permit, 10);
        let probe = breaker.try_acquire(110).expect("应获得探测名额");

        breaker.record_success(probe);

        assert_eq!(breaker.consecutive_failures(), 0);
        assert!(!breaker.is_rejecting(111));
        assert!(matches!(
            breaker.try_acquire(111).expect("闭合后应正常放行"),
            CircuitPermit::Closed { .. }
        ));
    }

    #[test]
    fn half_open_probe_failure_reopens_for_fresh_cooldown() {
        let breaker = breaker(1, 100, 50);
        let permit = breaker.try_acquire(10).expect("closed 应放行");
        breaker.record_retryable_failure(permit, 10);
        let probe = breaker.try_acquire(110).expect("应获得探测名额");

        let reopened = breaker
            .record_retryable_failure(probe, 110)
            .expect("探测失败应重开");

        assert!(reopened.probe_failure);
        assert_eq!(reopened.open_until_ms, 210);
        assert!(breaker.is_rejecting(209));
        assert!(!breaker.is_rejecting(210));
    }

    #[test]
    fn stale_closed_failure_after_recovery_is_ignored() {
        let breaker = breaker(1, 100, 50);
        // 旧请求在熔断器仍 Closed 时拿到 permit。
        let stale = breaker.try_acquire(1).expect("closed 应放行");
        // 另一路请求触发熔断器打开。
        let trip = breaker.try_acquire(1).expect("closed 应放行");
        breaker
            .record_retryable_failure(trip, 1)
            .expect("达到阈值应打开");
        // 冷却到点, 探测成功使熔断器闭合并进入新代次。
        let probe = breaker.try_acquire(101).expect("应获得探测名额");
        breaker.record_success(probe);
        assert_eq!(breaker.consecutive_failures(), 0);

        // 旧 permit 此刻才失败 —— 必须被代次检查丢弃。
        assert!(breaker.record_retryable_failure(stale, 102).is_none());
        assert_eq!(breaker.consecutive_failures(), 0, "陈旧失败不得累加");
        assert!(!breaker.is_rejecting(103), "陈旧失败不得重开熔断器");
    }

    #[test]
    fn half_open_probe_not_reclaimed_before_probe_timeout() {
        // cooldown(50)远短于 probe_timeout(200): 回收时限必须跟 probe_timeout 走,
        // 否则探测 RPC 仍在飞时名额就被回收 —— 熔断器会不断 fan-out 新探测。
        let breaker = breaker(1, 50, 200);
        let trip = breaker.try_acquire(0).expect("closed 应放行");
        breaker
            .record_retryable_failure(trip, 0)
            .expect("达到阈值应打开");

        // 冷却到点(50)拿到探测名额; 回收时限 = 50 + max(2*200, 50) = 450。
        let _probe = breaker.try_acquire(50).expect("冷却到点应获得探测名额");

        // 已远超 cooldown 但探测 RPC 可能仍在飞 —— 名额不得被回收。
        assert!(
            breaker.try_acquire(300).is_err(),
            "probe_timeout 内不得回收探测名额"
        );

        // 超过回收时限后(被取消的)探测名额才可被回收, 否则熔断器将永久卡死。
        assert!(
            matches!(
                breaker.try_acquire(450).expect("回收时限后名额应被回收"),
                CircuitPermit::HalfOpenProbe { .. }
            ),
            "回收时限到点应放出新探测"
        );
    }

    #[test]
    fn may_retry_only_for_current_closed_permit() {
        let breaker = breaker(1, 100, 50);
        let permit = breaker.try_acquire(1).expect("closed 应放行");
        assert!(breaker.may_retry(permit), "当代次 Closed permit 可重试");

        // 另一路请求打开熔断器后, 旧 Closed permit 不得再重试。
        let trip = breaker.try_acquire(1).expect("closed 应放行");
        breaker
            .record_retryable_failure(trip, 1)
            .expect("达到阈值应打开");
        assert!(
            !breaker.may_retry(permit),
            "熔断器转换后陈旧 permit 不得重试"
        );

        // 探测 permit 永不重试 —— 探测只有一次机会。
        let probe = breaker.try_acquire(101).expect("应获得探测名额");
        assert!(!breaker.may_retry(probe), "HalfOpen 探测不得重试");
    }

    #[test]
    fn reclaimed_probe_settlement_is_ignored() {
        // 被回收的探测即便事后才结算, 也不得影响新一轮探测的状态。
        let breaker = breaker(1, 50, 100);
        let trip = breaker.try_acquire(0).expect("closed 应放行");
        breaker.record_retryable_failure(trip, 0).expect("应打开");

        // 第一个探测被取消, 永不结算; 回收时限 = 50 + max(200,50) = 250。
        let abandoned = breaker.try_acquire(50).expect("应获得探测名额");
        // 回收时限到点, 新请求回收名额成为新探测。
        let fresh = breaker
            .try_acquire(300)
            .expect("回收时限后应获得新探测名额");
        assert!(matches!(fresh, CircuitPermit::HalfOpenProbe { .. }));

        // 被遗弃的旧探测此刻才"成功"结算 —— 代次已变, 必须被忽略。
        breaker.record_success(abandoned);
        assert!(
            breaker.is_rejecting(300),
            "陈旧探测的成功结算不得闭合新一轮熔断器"
        );
    }
}
