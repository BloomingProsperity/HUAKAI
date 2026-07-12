// Package payloadhash 生成请求原始字节的稳定摘要和脱敏引用。
package payloadhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Sum 对原始请求字节求 SHA-256。字节顺序或空白不同会得到不同摘要，
// 使幂等判别与实际送入 relay 的载荷保持一致。
func Sum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func RedactedRef(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return "sha256:" + Sum(body)
}
