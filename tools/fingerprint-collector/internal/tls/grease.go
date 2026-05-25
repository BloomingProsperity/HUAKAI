// 包 tls 提供 TLS ClientHello 的解析和指纹计算功能。
package tls

// greaseValues 是 GREASE（为可扩展性生成随机扩展和持续测试）的全部合法值集合。
// RFC 8701 定义了这些占位值，用于测试协议实现的扩展兼容性。
var greaseValues = map[uint16]bool{
	0x0a0a: true,
	0x1a1a: true,
	0x2a2a: true,
	0x3a3a: true,
	0x4a4a: true,
	0x5a5a: true,
	0x6a6a: true,
	0x7a7a: true,
	0x8a8a: true,
	0x9a9a: true,
	0xaaaa: true,
	0xbaba: true,
	0xcaca: true,
	0xdada: true,
	0xeaea: true,
	0xfafa: true,
}

// IsGREASE 判断给定的 uint16 值是否是 GREASE 占位值。
func IsGREASE(v uint16) bool {
	return greaseValues[v]
}

// FilterGREASE 从 uint16 切片中过滤掉所有 GREASE 值，返回新切片。
// JA3/JA4 计算时需要先过滤 GREASE，以确保哈希稳定可复现。
func FilterGREASE(values []uint16) []uint16 {
	result := make([]uint16, 0, len(values))
	for _, v := range values {
		if !IsGREASE(v) {
			result = append(result, v)
		}
	}
	return result
}
