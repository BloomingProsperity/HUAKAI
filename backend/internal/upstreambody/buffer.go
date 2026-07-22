// Package upstreambody 提供上游响应体的有界读取。
package upstreambody

import "io"

// MaxBufferedResponseBytes 是需要完整缓冲的上游响应体上限。
const MaxBufferedResponseBytes = 1 << 20

// ReadBounded 读取上游响应体，并额外读取一个字节以准确识别超限。
// 超限时仍返回上限以内的字节，供调用方保留其错误响应诊断合同。
func ReadBounded(r io.Reader) (body []byte, oversized bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r, MaxBufferedResponseBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > MaxBufferedResponseBytes {
		return body[:MaxBufferedResponseBytes], true, nil
	}
	return body, false, nil
}
