// 包 tls — JA3 指纹算法实现。
// 算法来源：https://github.com/salesforce/ja3
// 格式：TLSVersion,Ciphers,Extensions,EllipticCurves,EllipticCurvePointFormats
// 每个字段内元素以 "-" 分隔，字段间以 "," 分隔，最终对字符串取 MD5。
package tls

import (
	"crypto/md5"
	"fmt"
	"strconv"
	"strings"
)

// JA3Result 保存单次 JA3 计算的输入字符串和最终哈希。
type JA3Result struct {
	// InputString 是拼接后的 JA3 明文字符串（便于调试和对比）
	InputString string `json:"input_string"`
	// Hash 是 InputString 的 MD5 十六进制字符串
	Hash string `json:"hash"`
}

// ComputeJA3 根据已解析的 ClientHello 计算 JA3 指纹。
// GREASE 值在计算前被过滤，符合 JA3 规范。
func ComputeJA3(ch *ClientHello) JA3Result {
	// 1. TLS 版本号：优先使用 supported_versions 扩展中的最高 TLS 版本；
	//    若无该扩展，则回落到 legacy_version。
	version := ja3Version(ch)

	// 2. 密码套件：过滤 GREASE，过滤 TLS_EMPTY_RENEGOTIATION_INFO_SCSV (0x00FF)
	ciphers := FilterGREASE(ch.CipherSuites)
	filtered := make([]uint16, 0, len(ciphers))
	for _, c := range ciphers {
		if c != 0x00FF { // TLS_EMPTY_RENEGOTIATION_INFO_SCSV
			filtered = append(filtered, c)
		}
	}

	// 3. 扩展类型：过滤 GREASE，过滤 padding (0x0015) 和 pre_shared_key (0x0029)
	extTypes := FilterGREASE(ch.ExtensionTypes())
	filteredExt := make([]uint16, 0, len(extTypes))
	for _, e := range extTypes {
		if e != ExtPadding && e != ExtPreSharedKey {
			filteredExt = append(filteredExt, e)
		}
	}

	// 4. 椭圆曲线（supported_groups），过滤 GREASE
	groups := FilterGREASE(ch.SupportedGroups())

	// 5. EC 点格式
	pointFmts := ch.ECPointFormats()

	// 拼接为 JA3 字符串
	s := strings.Join([]string{
		strconv.Itoa(int(version)),
		uint16SliceToDashString(filtered),
		uint16SliceToDashString(filteredExt),
		uint16SliceToDashString(groups),
		uint8SliceToDashString(pointFmts),
	}, ",")

	hash := fmt.Sprintf("%x", md5.Sum([]byte(s)))
	return JA3Result{InputString: s, Hash: hash}
}

// ja3Version 选取用于 JA3 计算的 TLS 版本号。
// 若 supported_versions 中存在 TLS 1.3 (0x0304)，返回 0x0304；
// 否则返回 ClientHello legacy_version。
func ja3Version(ch *ClientHello) uint16 {
	vers := ch.SupportedVersions()
	for _, v := range vers {
		if v == 0x0304 { // TLS 1.3
			return v
		}
	}
	// 取 supported_versions 中非 GREASE 的最大值
	for _, v := range vers {
		if !IsGREASE(v) && v > 0x0300 {
			return v
		}
	}
	return ch.LegacyVersion
}

// uint16SliceToDashString 将 uint16 切片转换为以 "-" 分隔的十进制字符串。
func uint16SliceToDashString(vals []uint16) string {
	if len(vals) == 0 {
		return ""
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, "-")
}

// uint8SliceToDashString 将 uint8 切片转换为以 "-" 分隔的十进制字符串。
func uint8SliceToDashString(vals []uint8) string {
	if len(vals) == 0 {
		return ""
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, "-")
}
