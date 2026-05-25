// 包 tls — JA4 指纹算法实现。
// 算法来源：https://github.com/FoxIO-LLC/ja4
// 格式：<protocol><version><sni><cipher_count><ext_count>_<cipher_hash>_<ext_hash>
//
// 详细规则（参考 JA4 规范 v1.0）：
//   - GREASE 值在所有计数和哈希中均被移除
//   - protocol: "t" = TLS, "q" = QUIC, "d" = DTLS（此工具仅输出 "t"）
//   - version: 两位十六进制字符串，取 supported_versions 中最高的非 GREASE 版本；
//     若无 supported_versions，使用 legacy_version
//   - sni: "d" 表示有 SNI（域名），"i" 表示没有
//   - cipher_count: 两位十进制，密码套件数（过滤 GREASE 后）
//   - ext_count: 两位十进制，扩展数（过滤 GREASE 后）
//   - cipher_hash: 排序后的密码套件列表取 SHA-256 的前 12 个十六进制字符
//   - ext_hash: 排序后的扩展类型列表（去掉 SNI 和 ALPN）+ "_" + ALPN token，
//     取 SHA-256 前 12 个十六进制字符
package tls

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// JA4Result 保存单次 JA4 计算的结果。
type JA4Result struct {
	// Raw 是未截断的 JA4 字符串（便于调试）
	Raw string `json:"raw"`
	// Hash 是最终规范化的 JA4 字符串（已截取 12 位哈希）
	Hash string `json:"hash"`
}

// ComputeJA4 根据已解析的 ClientHello 计算 JA4 指纹。
func ComputeJA4(ch *ClientHello) JA4Result {
	// --- 第一部分：10 字符前缀 ---

	// protocol: 仅处理 TLS
	protocol := "t"

	// version: 取非 GREASE 最高 supported_versions，回落 legacy_version
	version := ja4TLSVersion(ch)

	// sni: 有 SNI 则 "d"，否则 "i"
	sniFlag := "i"
	if ch.SNI() != "" {
		sniFlag = "d"
	}

	// cipher_count: 过滤 GREASE 后的密码套件数（两位，最大 99）
	filteredCiphers := FilterGREASE(ch.CipherSuites)
	// 过滤 TLS_EMPTY_RENEGOTIATION_INFO_SCSV
	cleanCiphers := make([]uint16, 0, len(filteredCiphers))
	for _, c := range filteredCiphers {
		if c != 0x00FF {
			cleanCiphers = append(cleanCiphers, c)
		}
	}
	cipherCount := len(cleanCiphers)
	if cipherCount > 99 {
		cipherCount = 99
	}

	// ext_count: 过滤 GREASE 后的扩展数（两位，最大 99）
	filteredExtTypes := FilterGREASE(ch.ExtensionTypes())
	extCount := len(filteredExtTypes)
	if extCount > 99 {
		extCount = 99
	}

	alpnFirst := ja4ALPNToken(ch.ALPNProtocols())

	prefix := fmt.Sprintf("%s%s%s%02d%02d_%s",
		protocol,
		version,
		sniFlag,
		cipherCount,
		extCount,
		alpnFirst,
	)

	// --- 第二部分：cipher_hash ---
	// 密码套件排序后取 SHA-256 前 12 位十六进制
	sortedCiphers := make([]uint16, len(cleanCiphers))
	copy(sortedCiphers, cleanCiphers)
	sort.Slice(sortedCiphers, func(i, j int) bool {
		return sortedCiphers[i] < sortedCiphers[j]
	})
	cipherStr := uint16SliceToCommaSepHex(sortedCiphers)
	cipherHash := sha256Truncate12(cipherStr)

	// --- 第三部分：ext_hash ---
	// 扩展类型排序（去掉 SNI 0x0000 和 ALPN 0x0010），再附加 "_" + ALPN token
	extForHash := make([]uint16, 0, len(filteredExtTypes))
	for _, e := range filteredExtTypes {
		if e != ExtServerName && e != ExtALPN {
			extForHash = append(extForHash, e)
		}
	}
	sort.Slice(extForHash, func(i, j int) bool {
		return extForHash[i] < extForHash[j]
	})
	extStr := uint16SliceToCommaSepHex(extForHash)
	// 附加 "_" + ALPN token（HUAKAI 模板使用最后一个 advertised ALPN 的前 2 字符）
	fullExtStr := extStr + "_" + alpnFirst
	extHash := sha256Truncate12(fullExtStr)

	// 最终格式：prefix_cipherHash_extHash
	final := prefix + "_" + cipherHash + "_" + extHash
	return JA4Result{Raw: final, Hash: final}
}

func ja4ALPNToken(alpns []string) string {
	if len(alpns) == 0 {
		return "00"
	}
	alpn := alpns[len(alpns)-1]
	if len(alpn) >= 2 {
		return alpn[:2]
	}
	if len(alpn) == 1 {
		return alpn + "0"
	}
	return "00"
}

// ja4TLSVersion 返回 JA4 使用的两字符版本标识。
// 取 supported_versions 中最高的非 GREASE 版本；若无则用 legacy_version。
func ja4TLSVersion(ch *ClientHello) string {
	vers := ch.SupportedVersions()
	maxVer := uint16(0)
	for _, v := range vers {
		if !IsGREASE(v) && v > maxVer {
			maxVer = v
		}
	}
	if maxVer == 0 {
		maxVer = ch.LegacyVersion
	}
	return ja4VersionString(maxVer)
}

// ja4VersionString 将 TLS 版本号映射为 JA4 规范的两字符版本标识。
func ja4VersionString(v uint16) string {
	switch v {
	case 0x0304:
		return "13" // TLS 1.3
	case 0x0303:
		return "12" // TLS 1.2
	case 0x0302:
		return "11" // TLS 1.1
	case 0x0301:
		return "10" // TLS 1.0
	case 0x0300:
		return "s3" // SSL 3.0
	case 0x0200:
		return "s2" // SSL 2.0
	default:
		// 未知版本：输出两位十六进制（高字节）
		return fmt.Sprintf("%02x", v>>8)
	}
}

// uint16SliceToCommaSepHex 将 uint16 切片转换为逗号分隔的十进制字符串，用于 JA4 哈希输入。
// 注意：JA4 规范使用十进制而非十六进制。
func uint16SliceToCommaSepHex(vals []uint16) string {
	if len(vals) == 0 {
		return ""
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, ",")
}

// sha256Truncate12 计算字符串的 SHA-256 哈希，返回前 12 个十六进制字符。
func sha256Truncate12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)[:12]
}
