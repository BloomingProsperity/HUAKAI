#[cfg(feature = "mimicry-boring")]
use std::sync::Arc;

use super::{
    AvailableMimicryFeatures, BackendResolverError, FingerprintProfile, MimicryBackend,
    resolve_profile_mimicry_backend,
};
#[cfg(feature = "mimicry-boring")]
use crate::proxy_engine::{GatewayHttpClient, try_build_http_client_with_profile};

/// mimicry 生产 dispatch gate 的最终判定。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DispatchDecision {
    /// Boring backend 可进入生产 dispatch；实际 HTTP client 构造由 proxy_engine 侧负责。
    AllowBoring,
    /// OpenSSL adapter 已通过 exact local capture 后才允许进入生产 dispatch。
    AllowOpenSsl,
    /// 已知字段 gap profile 只能保留 plumbing/profile/test/local-capture，生产 dispatch 必须拒绝。
    BlockKnownGap { reason: String },
    /// 模板声明的 TLS backend 当前没有可用生产实现。
    BlockUnsupportedTemplate { reason: String },
}

/// mimicry 生产 dispatch 的可执行动作。
///
/// `DispatchDecision` 只回答是否允许；本类型把 Boring 分支真正落到
/// proxy_engine 出站 HTTP client。OpenSSL adapter 仍沿用既有 R-1 路径，
/// 本轮不改变它的构造时机。
pub enum MimicryAction {
    #[cfg(feature = "mimicry-boring")]
    UseBoringClient(GatewayHttpClient),
    UseOpenSslAdapter,
    BlockKnownGap {
        reason: String,
    },
    BlockUnsupportedTemplate {
        reason: String,
    },
}

pub fn decide_dispatch(profile: &FingerprintProfile) -> DispatchDecision {
    match try_decide_dispatch(profile) {
        Ok(decision) => decision,
        Err(
            BackendResolverError::ProfileBackendMismatch { reason }
            | BackendResolverError::BackendUnavailable { reason }
            | BackendResolverError::UnsupportedTemplate { reason },
        ) => DispatchDecision::BlockUnsupportedTemplate { reason },
    }
}

pub fn try_decide_dispatch(
    profile: &FingerprintProfile,
) -> Result<DispatchDecision, BackendResolverError> {
    let backend = resolve_profile_mimicry_backend(profile, AvailableMimicryFeatures::current())?;

    Ok(match backend {
        MimicryBackend::Boring => DispatchDecision::AllowBoring,
        MimicryBackend::Openssl => DispatchDecision::AllowOpenSsl,
        MimicryBackend::KnownGapBlocked { reason } => DispatchDecision::BlockKnownGap { reason },
    })
}

pub fn decide_dispatch_with_features(
    profile: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> DispatchDecision {
    match try_decide_dispatch_with_features(profile, available_features) {
        Ok(decision) => decision,
        Err(
            BackendResolverError::ProfileBackendMismatch { reason }
            | BackendResolverError::BackendUnavailable { reason }
            | BackendResolverError::UnsupportedTemplate { reason },
        ) => DispatchDecision::BlockUnsupportedTemplate { reason },
    }
}

pub fn try_decide_dispatch_with_features(
    profile: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<DispatchDecision, BackendResolverError> {
    let backend = resolve_profile_mimicry_backend(profile, available_features)?;

    Ok(match backend {
        MimicryBackend::Boring => DispatchDecision::AllowBoring,
        MimicryBackend::Openssl => DispatchDecision::AllowOpenSsl,
        MimicryBackend::KnownGapBlocked { reason } => DispatchDecision::BlockKnownGap { reason },
    })
}

pub fn is_dispatch_allowed(decision: &DispatchDecision) -> bool {
    matches!(
        decision,
        DispatchDecision::AllowBoring | DispatchDecision::AllowOpenSsl
    )
}

/// W11-F P1-3+P1-4 fix 2026-05-24: production canary error — mimicry profile
/// 不能进入生产 dispatch 时使用此类型, 让 startup 路径 (build_gateway_connector,
/// GatewayState::new) 能 fail-fast 而不是静默走 "已知有缺口" 的 profile。
///
/// 旧路径: build_gateway_connector(mimicry-boring) 直接 `BoringTlsConnector::new(profile)`
/// 完全跳过 decide_dispatch, KnownGap / UnsupportedTemplate 标记的 profile 也会被
/// 接入 production HTTP client = L1 (TLS preflight) + L2 (H2/HTTP profile) 守门
/// 永远不被生产路径触发 = 守门形同虚设。
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum MimicryProductionCanaryError {
    #[error("mimicry profile blocked by known gap: {0}")]
    KnownGap(String),
    #[error("mimicry profile uses unsupported template: {0}")]
    UnsupportedTemplate(String),
}

/// W11-F P1-3+P1-4 fix 2026-05-24: 在 production HTTP client 构造前必须调本函数。
///
/// AllowBoring / AllowOpenSsl -> Ok(()), 表示 profile 已通过 L1 backend resolver +
/// L2 dispatch gate, 可安全进入生产 dispatch。
/// BlockKnownGap / BlockUnsupportedTemplate -> Err, 调用方应 fail-fast (panic 或
/// 上抛 GatewayError) 让运维知道 profile 必须修复后才能上 production。
///
/// 调用点 (W11-F P1-3+P1-4 wiring):
/// - proxy_engine/http_client.rs::build_gateway_connector (mimicry-boring 分支)
///   -> 通过则 BoringTlsConnector::new, 不通过 panic。
/// - mimicry/dispatch.rs::build_mimicry_action -> 已通过 DispatchDecision 自然分流。
/// - 未来 GatewayState::new 可在构造 HTTP client 前调本函数生成显式 startup-time error。
pub fn verify_profile_dispatchable_for_production(
    profile: &FingerprintProfile,
) -> Result<(), MimicryProductionCanaryError> {
    match decide_dispatch(profile) {
        DispatchDecision::AllowBoring | DispatchDecision::AllowOpenSsl => Ok(()),
        DispatchDecision::BlockKnownGap { reason } => {
            Err(MimicryProductionCanaryError::KnownGap(reason))
        }
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            Err(MimicryProductionCanaryError::UnsupportedTemplate(reason))
        }
    }
}

pub fn build_mimicry_action(profile: &FingerprintProfile) -> MimicryAction {
    match decide_dispatch(profile) {
        DispatchDecision::AllowBoring => build_boring_action(profile),
        DispatchDecision::AllowOpenSsl => MimicryAction::UseOpenSslAdapter,
        DispatchDecision::BlockKnownGap { reason } => MimicryAction::BlockKnownGap { reason },
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            MimicryAction::BlockUnsupportedTemplate { reason }
        }
    }
}

#[cfg(feature = "mimicry-boring")]
fn build_boring_action(profile: &FingerprintProfile) -> MimicryAction {
    // W11-F F-2.3+ round 2 (Codex round 1 P2, 2026-05-25):
    // try_build_http_client_with_profile now has an L1 preflight gate that returns
    // Err(KnownGap) / Err(UnsupportedTemplate) for Codex / Gemini even when the
    // dispatch canary above said `AllowBoring` — because `backend_from_profile_intent`
    // has an L2-A8 special case that downgrades Codex KnownGap → Openssl, which
    // `resolve_vendor_mimicry_backend` then upgrades back to Boring whenever
    // mimicry-boring is on.
    //
    // The earlier eager `build_http_client_with_profile(...)` would `.expect(...)`
    // that Err and panic at production traffic — turning a typed fail-closed signal
    // into a process abort (the whole gateway falls over instead of just blocking
    // one request). Adopting the fallible try_ variant and mapping the error into
    // the corresponding `MimicryAction::Block*` variant keeps the fail-closed
    // semantic but recoverable at the request scope.
    //
    // Mutation: reverting to `build_http_client_with_profile(...).expect(...)`
    // makes `boring_dispatch_action_returns_block_for_codex_known_gap` and
    // `..._returns_block_for_gemini_pending` panic with "production dispatch
    // canary" instead of returning the structured Block* — both tests red.
    match try_build_http_client_with_profile(Arc::new(profile.clone())) {
        Ok(client) => MimicryAction::UseBoringClient(client),
        Err(MimicryProductionCanaryError::KnownGap(reason)) => {
            MimicryAction::BlockKnownGap { reason }
        }
        Err(MimicryProductionCanaryError::UnsupportedTemplate(reason)) => {
            MimicryAction::BlockUnsupportedTemplate { reason }
        }
    }
}

#[cfg(not(feature = "mimicry-boring"))]
fn build_boring_action(_profile: &FingerprintProfile) -> MimicryAction {
    MimicryAction::BlockUnsupportedTemplate {
        reason: "boring dispatch requires the mimicry-boring feature".to_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::{BuiltinProfile, load_builtin_profile};

    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn boring_dispatch_action_builds_profile_http_client() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::UseBoringClient(client) => {
                let _: GatewayHttpClient = client;
            }
            _ => panic!("mimicry-boring build 应构造 Boring HTTP client"),
        }
    }

    /// W11-F P1-3+P1-4 canary 单元判别: Anthropic Claude Code 内置 profile 必须
    /// 通过 production dispatch canary (无论 mimicry-boring 是否开 — Anthropic 模板有
    /// OpenSslAdapter intent, OpenSSL feature off 时落 KnownGap 但 boring feature off
    /// 时仍走 KnownGap; 这里覆盖 boring feature 编进二进制的主路径)。
    ///
    /// 判别性 + mutation: 改 verify_profile_dispatchable_for_production 返 Err
    /// 内置 profile 任意情况 -> 此测试红 (canary 错伤主路径)。
    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn production_canary_accepts_anthropic_builtin_profile() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile 应加载");
        verify_profile_dispatchable_for_production(&profile)
            .expect("Anthropic 内置 profile (boring feature 编入时) 必须通过 production canary");
    }

    /// W11-F P1-3+P1-4 canary 反向判别: kiro rustls 模板必须被 canary 拒 (UnsupportedTemplate
    /// 或 KnownGap 任一 — backend_resolver 先撞 rustls + openssl-only 字段守门返
    /// UnsupportedTemplate; 若改成纯 KnownGap 路径也仍合规)。
    ///
    /// 判别性 + mutation:
    /// - mutation: 删 verify_profile_dispatchable_for_production 任一 Block 分支 -> Ok 返 ->
    ///   测试 expect_err 红 (KnownGap / UnsupportedTemplate profile 被静默放行)。
    /// - mutation: 改 backend_resolver KiroCli 走 OpenSSL -> Allow 返 -> 测试红 (上游
    ///   contract 破坏立刻发现)。
    #[test]
    fn production_canary_rejects_blocked_kiro_profile() {
        let profile =
            load_builtin_profile(BuiltinProfile::KiroCli).expect("Kiro profile 应加载");
        let err = verify_profile_dispatchable_for_production(&profile)
            .expect_err("Kiro rustls 模板必须被 canary 拒, 不允许进 production");
        assert!(
            matches!(
                err,
                MimicryProductionCanaryError::KnownGap(_)
                    | MimicryProductionCanaryError::UnsupportedTemplate(_)
            ),
            "canary 应返 KnownGap 或 UnsupportedTemplate (Block 分类等价), 实际: {err:?}"
        );
    }

    /// 2026-05-24 第三方 AI 反馈 fix: 原 cfg 仅 `not(feature = "mimicry-boring")` 太宽,
    /// `--features mimicry-openssl` 也命中 (该 feature 下 boring 关闭但 openssl 开启),
    /// Anthropic profile 的 OpenSslAdapter intent 在 `decide_dispatch` 返
    /// `AllowOpenSsl` → `build_mimicry_action` 返 `UseOpenSslAdapter` 而非
    /// `BlockKnownGap` → 原 `panic!("...不应构造 Boring HTTP client")` 触发 (虽然
    /// 实际返回的不是 Boring client)。
    ///
    /// 该测试的语义本应是 "Boring + OpenSSL 两族 backend 都关掉时 Anthropic 必落
    /// known-gap" — 收窄到两 feature 都 off 才编译, 与 verify.sh feature matrix 协调:
    /// `default::` (两者都 off) 跑此测试; `--features mimicry-boring` 跑 above 的
    /// boring 路径正测试; `--features mimicry-openssl` 走 OpenSslAdapter 路径不需要
    /// 此对照。
    ///
    /// mutation: 把 cfg 改回单 `not(feature = "mimicry-boring")` →
    /// `--features mimicry-openssl` feature-matrix 跑会 panic → CI 红。
    #[cfg(all(not(feature = "mimicry-boring"), not(feature = "mimicry-openssl")))]
    #[test]
    fn boring_dispatch_action_is_not_available_without_boring_feature() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::BlockKnownGap { reason } => {
                assert!(
                    reason.contains("mimicry-boring"),
                    "feature-off build 必须阻断 Boring dispatch，实际: {reason}"
                );
            }
            _ => panic!("feature-off build 不应构造 Boring HTTP client"),
        }
    }

    /// W11-F F-2.3+ round 2 (Codex round 1 P2, 2026-05-25): `build_mimicry_action`
    /// for Codex MUST return `MimicryAction::BlockKnownGap` (structured fail-closed
    /// signal) — NOT panic via the eager builder's `.expect(...)`. The previous
    /// shape was: `decide_dispatch(codex) -> AllowBoring` (because of the L2-A8
    /// resolver special case), then `build_boring_action -> build_http_client_with_profile
    /// (eager .expect)` which panicked on the L1 preflight Err that round 1 added.
    /// The fix in `build_boring_action` (this commit, round 2) adopts the fallible
    /// `try_` variant and maps Err → MimicryAction::Block*.
    ///
    /// Discriminating mutation: reverting `build_boring_action` to the eager
    /// `build_http_client_with_profile(...).expect(...)` flips this test from
    /// "returns BlockKnownGap" to "panics with `production dispatch canary`"
    /// — `match action { ... }` never even runs because the call panicked.
    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn boring_dispatch_action_returns_block_for_codex_known_gap() {
        let profile =
            load_builtin_profile(BuiltinProfile::CodexCli).expect("Codex profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::BlockKnownGap { reason } => {
                assert!(
                    reason.contains("cipher_suites")
                        || reason.contains("extensions")
                        || reason.contains("supported_groups")
                        || reason.contains("signature_algorithms"),
                    "Codex BlockKnownGap reason must name at least one of the 4 \
                     hard-coded gap fields (got: {reason})"
                );
            }
            MimicryAction::UseBoringClient(_) => panic!(
                "Codex must NOT get a Boring client — L1 preflight gate is the \
                 fail-closed for KnownGap profiles"
            ),
            MimicryAction::UseOpenSslAdapter => panic!(
                "Codex must NOT route through OpenSSL adapter via the Boring \
                 action path — decide_dispatch returned AllowBoring not AllowOpenSsl"
            ),
            MimicryAction::BlockUnsupportedTemplate { reason } => panic!(
                "Codex should land in BlockKnownGap, not BlockUnsupportedTemplate: {reason}"
            ),
        }
    }

    /// W11-F F-2.3+ round 2 (Codex round 1 P2, 2026-05-25): same as the Codex
    /// test above but for Gemini (Pending status). `is_dispatchable` rejects
    /// Pending so the typed L1 gate inside `try_build_http_client_with_profile`
    /// returns Err — without the round-2 fix in `build_boring_action`, that Err
    /// would panic via `.expect(...)`. With the fix, Gemini lands in
    /// `MimicryAction::BlockKnownGap` with reason naming Pending + profile mode
    /// so F-2.3a runtime preflight wiring can grep these literals.
    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn boring_dispatch_action_returns_block_for_gemini_pending() {
        let profile = load_builtin_profile(BuiltinProfile::GeminiAdvanced)
            .expect("Gemini profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::BlockKnownGap { reason } => {
                assert!(
                    reason.contains("Pending") && reason.contains("GeminiAdvanced"),
                    "Gemini BlockKnownGap reason must reference Pending + \
                     GeminiAdvanced profile mode (got: {reason})"
                );
            }
            MimicryAction::UseBoringClient(_) => panic!(
                "Gemini must NOT get a Boring client — L1 preflight Pending must \
                 fail-closed until F-2.3a runtime preflight wires"
            ),
            MimicryAction::UseOpenSslAdapter => panic!(
                "Gemini went through Boring action path; expected BlockKnownGap"
            ),
            MimicryAction::BlockUnsupportedTemplate { reason } => panic!(
                "Gemini should land in BlockKnownGap, not BlockUnsupportedTemplate: {reason}"
            ),
        }
    }
}
