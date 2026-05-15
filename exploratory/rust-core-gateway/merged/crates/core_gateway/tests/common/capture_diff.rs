// L2-A3 test-only TLS capture diff normalizer.
// 这里只做字段级正向核验：每个模板字段都会进入 diff 结果，而不是只在失败时追加错误。

use std::collections::BTreeSet;

use core_gateway::mimicry::{BackendIntent, FingerprintProfile, ProfileMatchPolicy};

use super::tls_capture::CapturedClientHello;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CaptureDiff {
    pub profile_blocked: bool,
    pub legacy_version: FieldStatus<u16>,
    pub cipher_suites: ListFieldStatus<u16>,
    pub extensions: ListFieldStatus<u16>,
    pub supported_groups: ListFieldStatus<u16>,
    pub signature_algorithms: ListFieldStatus<u16>,
    pub ec_point_formats: ListFieldStatus<u8>,
    pub alpn_protocols: ListFieldStatus<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FieldStatus<T> {
    Match { value: T },
    Mismatch { expected: T, actual: T },
    NotInTemplate { actual: T },
    NotCaptured { expected: T },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ListFieldStatus<T> {
    OrderedMatch { value: Vec<T> },
    OrderMismatch { expected: Vec<T>, actual: Vec<T> },
    SetMatch { value: Vec<T> },
    SetMismatch { extra: Vec<T>, missing: Vec<T> },
}

pub fn diff_capture_against_profile(
    captured: &CapturedClientHello,
    profile: &FingerprintProfile,
) -> CaptureDiff {
    let match_policy = profile.match_policy();

    CaptureDiff {
        profile_blocked: profile_is_blocked(profile),
        legacy_version: compare_field(
            expected_legacy_version(profile),
            captured_legacy_version(captured),
        ),
        cipher_suites: compare_list(
            &profile.tls.cipher_suites,
            &captured.cipher_suites,
            match_policy,
        ),
        extensions: compare_list(&profile.tls.extensions, &captured.extensions, match_policy),
        supported_groups: compare_list(
            &profile.tls.supported_groups,
            &captured.supported_groups,
            match_policy,
        ),
        signature_algorithms: compare_list(
            &profile.tls.signature_algorithms,
            &captured.signature_algorithms,
            match_policy,
        ),
        ec_point_formats: compare_list(
            &profile.tls.ec_point_formats,
            &captured.ec_point_formats,
            match_policy,
        ),
        alpn_protocols: compare_list(
            &profile.tls.alpn_protocols,
            &captured.alpn_protocols,
            match_policy,
        ),
    }
}

pub fn diff_has_mismatch(diff: &CaptureDiff) -> bool {
    field_has_mismatch(&diff.legacy_version)
        || list_has_mismatch(&diff.cipher_suites)
        || list_has_mismatch(&diff.extensions)
        || list_has_mismatch(&diff.supported_groups)
        || list_has_mismatch(&diff.signature_algorithms)
        || list_has_mismatch(&diff.ec_point_formats)
        || list_has_mismatch(&diff.alpn_protocols)
}

fn profile_is_blocked(profile: &FingerprintProfile) -> bool {
    matches!(
        profile.backend_intent(),
        BackendIntent::KnownGapBlocked { .. } | BackendIntent::UnsupportedTemplate { .. }
    )
}

fn expected_legacy_version(profile: &FingerprintProfile) -> Option<u16> {
    profile
        .tls
        .ja3
        .split(',')
        .next()
        .and_then(|version| version.parse::<u16>().ok())
}

fn captured_legacy_version(captured: &CapturedClientHello) -> Option<u16> {
    // 合成测试可用 0 表达“该标量未被捕获”；真实 parser 不会产出这个 TLS legacy_version。
    if captured.legacy_version == 0 {
        None
    } else {
        Some(captured.legacy_version)
    }
}

fn compare_field<T>(expected: Option<T>, actual: Option<T>) -> FieldStatus<T>
where
    T: Clone + PartialEq,
{
    match (expected, actual) {
        (Some(expected), Some(actual)) if expected == actual => {
            FieldStatus::Match { value: expected }
        }
        (Some(expected), Some(actual)) => FieldStatus::Mismatch { expected, actual },
        (Some(expected), None) => FieldStatus::NotCaptured { expected },
        (None, Some(actual)) => FieldStatus::NotInTemplate { actual },
        (None, None) => unreachable!("模板和 capture 同时缺少标量字段时无法构造字段级状态"),
    }
}

fn compare_list<T>(
    expected: &[T],
    actual: &[T],
    match_policy: ProfileMatchPolicy,
) -> ListFieldStatus<T>
where
    T: Clone + Ord,
{
    match match_policy {
        ProfileMatchPolicy::SampleSetRandomized => compare_set(expected, actual),
        ProfileMatchPolicy::ExactStable | ProfileMatchPolicy::KnownGapBlocked => {
            compare_ordered(expected, actual)
        }
    }
}

fn compare_ordered<T>(expected: &[T], actual: &[T]) -> ListFieldStatus<T>
where
    T: Clone + PartialEq,
{
    if expected == actual {
        ListFieldStatus::OrderedMatch {
            value: expected.to_vec(),
        }
    } else {
        ListFieldStatus::OrderMismatch {
            expected: expected.to_vec(),
            actual: actual.to_vec(),
        }
    }
}

fn compare_set<T>(expected: &[T], actual: &[T]) -> ListFieldStatus<T>
where
    T: Clone + Ord,
{
    let expected_set = sorted_unique(expected);
    let actual_set = sorted_unique(actual);

    if expected_set == actual_set {
        return ListFieldStatus::SetMatch {
            value: expected_set,
        };
    }

    let expected_lookup = expected_set.iter().collect::<BTreeSet<_>>();
    let actual_lookup = actual_set.iter().collect::<BTreeSet<_>>();
    let extra = actual_set
        .iter()
        .filter(|value| !expected_lookup.contains(value))
        .cloned()
        .collect();
    let missing = expected_set
        .iter()
        .filter(|value| !actual_lookup.contains(value))
        .cloned()
        .collect();

    ListFieldStatus::SetMismatch { extra, missing }
}

fn sorted_unique<T>(values: &[T]) -> Vec<T>
where
    T: Clone + Ord,
{
    values
        .iter()
        .cloned()
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect()
}

fn field_has_mismatch<T>(status: &FieldStatus<T>) -> bool {
    !matches!(status, FieldStatus::Match { .. })
}

fn list_has_mismatch<T>(status: &ListFieldStatus<T>) -> bool {
    matches!(
        status,
        ListFieldStatus::OrderMismatch { .. } | ListFieldStatus::SetMismatch { .. }
    )
}
