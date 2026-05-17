//! HUAKAI ClientHello 字节级布局 + JA3 hash 计算
//!
//! 本模块不读 rquest / curl_cffi / wreq / utls 等任何反代项目 source,
//! 仅按 salesforce/ja3 公开 README 算法 + TLS RFC + HUAKAI fingerprint
//! profile 真采样数据自研。

use crate::mimicry::profile::FingerprintProfile as MimicryProfile;

/// HUAKAI ClientHello 字节级布局描述。
///
/// 字段来源: HUAKAI fingerprint-collector 采样的真实 vendor JA3 五元组,
/// 不是 OpenSSL 自动默认值。`from_profile` 只复制 profile 已有字段,
/// 不补 TLS 库默认值。
#[derive(Clone, Debug)]
pub struct ClientHelloLayout {
    pub tls_version: u16,
    pub cipher_suites: Vec<u16>,
    pub extensions: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub ec_point_formats: Vec<u8>,
    pub signature_algorithms: Vec<u16>,
    pub alpn_protocols: Vec<String>,
    pub sni_hostname: Option<String>,
    ja3_extensions: Vec<u16>,
}

impl ClientHelloLayout {
    /// 从 HUAKAI MimicryProfile 构造 layout, 不补默认值。
    pub fn from_profile(profile: &MimicryProfile, sni_hostname: Option<String>) -> Self {
        let ja3_parts = parse_ja3_parts(&profile.tls.ja3);
        let tls_version = ja3_parts
            .as_ref()
            .map(|parts| parts.tls_version)
            .or_else(|| profile.tls.supported_versions.first().copied())
            .unwrap_or(0x0303);
        let ja3_extensions = ja3_parts
            .as_ref()
            .map(|parts| parts.extensions.clone())
            .unwrap_or_else(|| profile.tls.extensions.clone());

        Self {
            tls_version,
            cipher_suites: profile.tls.cipher_suites.clone(),
            extensions: profile.tls.extensions.clone(),
            supported_groups: profile.tls.supported_groups.clone(),
            ec_point_formats: profile.tls.ec_point_formats.clone(),
            signature_algorithms: profile.tls.signature_algorithms.clone(),
            alpn_protocols: profile.tls.alpn_protocols.clone(),
            sni_hostname,
            ja3_extensions,
        }
    }

    /// 按 salesforce/ja3 公开算法生成 canonical 字符串。
    ///
    /// 算法: tls_version + cipher_list + ext_list + curves + ec_point_formats。
    /// 分隔: 字段内 '-', 字段间 ','。GREASE 值不进入字符串。
    pub fn ja3_string(&self) -> String {
        [
            self.tls_version.to_string(),
            join_u16_decimal(&self.cipher_suites),
            join_u16_decimal(&self.ja3_extensions),
            join_u16_decimal(&self.supported_groups),
            join_u8_decimal(&self.ec_point_formats),
        ]
        .join(",")
    }

    /// 按 salesforce/ja3 公开算法计算 MD5 lowercase hex。
    pub fn ja3_hash(&self) -> String {
        md5_lower_hex(self.ja3_string().as_bytes())
    }
}

#[derive(Debug)]
struct Ja3Parts {
    tls_version: u16,
    extensions: Vec<u16>,
}

fn parse_ja3_parts(text: &str) -> Option<Ja3Parts> {
    let fields = text.split(',').collect::<Vec<_>>();
    if fields.len() != 5 {
        return None;
    }

    Some(Ja3Parts {
        tls_version: fields[0].parse().ok()?,
        extensions: parse_u16_decimal_list(fields[2])?,
    })
}

fn parse_u16_decimal_list(text: &str) -> Option<Vec<u16>> {
    if text.is_empty() {
        return Some(Vec::new());
    }

    text.split('-').map(|item| item.parse().ok()).collect()
}

fn join_u16_decimal(values: &[u16]) -> String {
    values
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

fn join_u8_decimal(values: &[u8]) -> String {
    values
        .iter()
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

fn md5_lower_hex(input: &[u8]) -> String {
    let mut message = input.to_vec();
    let bit_len = (message.len() as u64).wrapping_mul(8);
    message.push(0x80);
    while message.len() % 64 != 56 {
        message.push(0);
    }
    message.extend_from_slice(&bit_len.to_le_bytes());

    let mut a0 = 0x6745_2301u32;
    let mut b0 = 0xefcd_ab89u32;
    let mut c0 = 0x98ba_dcfeu32;
    let mut d0 = 0x1032_5476u32;

    for chunk in message.chunks_exact(64) {
        let mut words = [0u32; 16];
        for (index, word) in words.iter_mut().enumerate() {
            let offset = index * 4;
            *word = u32::from_le_bytes([
                chunk[offset],
                chunk[offset + 1],
                chunk[offset + 2],
                chunk[offset + 3],
            ]);
        }

        let mut a = a0;
        let mut b = b0;
        let mut c = c0;
        let mut d = d0;

        for index in 0..64 {
            let (f, g) = if index < 16 {
                ((b & c) | ((!b) & d), index)
            } else if index < 32 {
                ((d & b) | ((!d) & c), (5 * index + 1) % 16)
            } else if index < 48 {
                (b ^ c ^ d, (3 * index + 5) % 16)
            } else {
                (c ^ (b | (!d)), (7 * index) % 16)
            };

            let next_d = d;
            d = c;
            c = b;
            b = b.wrapping_add(
                a.wrapping_add(f)
                    .wrapping_add(MD5_K[index])
                    .wrapping_add(words[g])
                    .rotate_left(MD5_S[index]),
            );
            a = next_d;
        }

        a0 = a0.wrapping_add(a);
        b0 = b0.wrapping_add(b);
        c0 = c0.wrapping_add(c);
        d0 = d0.wrapping_add(d);
    }

    let mut digest = [0u8; 16];
    digest[0..4].copy_from_slice(&a0.to_le_bytes());
    digest[4..8].copy_from_slice(&b0.to_le_bytes());
    digest[8..12].copy_from_slice(&c0.to_le_bytes());
    digest[12..16].copy_from_slice(&d0.to_le_bytes());
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

const MD5_S: [u32; 64] = [
    7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9,
    14, 20, 5, 9, 14, 20, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 6, 10, 15,
    21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21,
];

const MD5_K: [u32; 64] = [
    0xd76a_a478,
    0xe8c7_b756,
    0x2420_70db,
    0xc1bd_ceee,
    0xf57c_0faf,
    0x4787_c62a,
    0xa830_4613,
    0xfd46_9501,
    0x6980_98d8,
    0x8b44_f7af,
    0xffff_5bb1,
    0x895c_d7be,
    0x6b90_1122,
    0xfd98_7193,
    0xa679_438e,
    0x49b4_0821,
    0xf61e_2562,
    0xc040_b340,
    0x265e_5a51,
    0xe9b6_c7aa,
    0xd62f_105d,
    0x0244_1453,
    0xd8a1_e681,
    0xe7d3_fbc8,
    0x21e1_cde6,
    0xc337_07d6,
    0xf4d5_0d87,
    0x455a_14ed,
    0xa9e3_e905,
    0xfcef_a3f8,
    0x676f_02d9,
    0x8d2a_4c8a,
    0xfffa_3942,
    0x8771_f681,
    0x6d9d_6122,
    0xfde5_380c,
    0xa4be_ea44,
    0x4bde_cfa9,
    0xf6bb_4b60,
    0xbebf_bc70,
    0x289b_7ec6,
    0xeaa1_27fa,
    0xd4ef_3085,
    0x0488_1d05,
    0xd9d4_d039,
    0xe6db_99e5,
    0x1fa2_7cf8,
    0xc4ac_5665,
    0xf429_2244,
    0x432a_ff97,
    0xab94_23a7,
    0xfc93_a039,
    0x655b_59c3,
    0x8f0c_cc92,
    0xffef_f47d,
    0x8584_5dd1,
    0x6fa8_7e4f,
    0xfe2c_e6e0,
    0xa301_4314,
    0x4e08_11a1,
    0xf753_7e82,
    0xbd3a_f235,
    0x2ad7_d2bb,
    0xeb86_d391,
];

/// 判断是否 GREASE 值 (RFC 8701)。
///
/// GREASE values: 0x0A0A, 0x1A1A, 0x2A2A, ..., 0xFAFA。
pub(crate) fn is_grease(value: u16) -> bool {
    let high = (value >> 8) as u8;
    let low = value as u8;
    high == low && (value & 0x0f0f) == 0x0a0a
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::profile::{BuiltinProfile, load_builtin_profile};

    #[test]
    fn grease_values_are_skipped_by_ja3() {
        assert!(is_grease(0x0a0a));
        assert!(is_grease(0x1a1a));
        assert!(is_grease(0xfafa));
        assert!(!is_grease(0x0303));
        assert!(!is_grease(0x0a0b));
    }

    #[test]
    fn md5_known_vector_matches_rfc_example() {
        assert_eq!(md5_lower_hex(b"abc"), "900150983cd24fb0d6963f7d28e17f72");
    }

    #[test]
    fn anthropic_ja3_string_uses_collector_field_order() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let layout = ClientHelloLayout::from_profile(&profile, Some(profile.target_host.clone()));

        assert_eq!(layout.ja3_string(), profile.tls.ja3);
        assert_eq!(layout.tls_version, 772);
        assert_eq!(
            layout.ja3_string(),
            "772,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-65037-23-65281-10-11-35-16-5-13-18-51-45-43,29-23-24,0"
        );
    }

    #[test]
    fn anthropic_ja3_hash_matches_sample() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let layout = ClientHelloLayout::from_profile(&profile, None);

        assert_eq!(layout.ja3_hash(), "de88744b20558d50f03a5f0ea176ee98");
        assert_eq!(layout.ja3_hash(), profile.tls.ja3_hash);
    }

    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn ja3_wire_boring_connector_accepts_anthropic_profile() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();

        crate::mimicry::client_hello_builder::build_boring_connector(
            &profile,
            Some(profile.target_host.clone()),
        )
        .unwrap();
    }
}
