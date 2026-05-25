use bytes::Bytes;

pub(super) fn build_idempotency_key(
    request_id: &str,
    attempt_id: &str,
    acquisition_token: &Bytes,
) -> String {
    let attempt_uuid = attempt_id
        .strip_prefix("attempt-")
        .unwrap_or(attempt_id)
        .to_owned();
    let fingerprint = stable_fingerprint(request_id, attempt_id, acquisition_token);
    format!("idem-v7-{attempt_uuid}-{fingerprint:016x}")
}

fn stable_fingerprint(request_id: &str, attempt_id: &str, acquisition_token: &Bytes) -> u64 {
    // FNV-1a 足够做幂等 key 的短指纹; 不用于安全边界。
    let mut hash = 0xcbf2_9ce4_8422_2325u64;
    for byte in request_id
        .as_bytes()
        .iter()
        .chain(attempt_id.as_bytes())
        .chain(acquisition_token.iter())
    {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hash
}

#[cfg(test)]
mod tests {
    use bytes::Bytes;
    use uuid::Uuid;

    use super::*;

    #[test]
    fn idempotency_key_uses_attempt_uuid_and_token_fingerprint() {
        let attempt_id = format!("attempt-{}", Uuid::now_v7());
        let key = build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-1"));
        assert!(key.contains(attempt_id.trim_start_matches("attempt-")));
        assert_eq!(
            key,
            build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-1"))
        );
        assert_ne!(
            key,
            build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-2"))
        );
    }
}
