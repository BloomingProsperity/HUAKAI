package gateway

import (
	"fmt"
	"net/http"
)

// UpstreamHTTPError 由 buffered/HCSF 路径返回上游非 2xx 响应时构造, 用于把
// 上游真实 status code + body + headers 透传到 caller (chat handler 等),
// 保留 client retry 语义 (401/429) + cooldown / channel-health 分类信号
// (不是所有失败都塌成 502 + status=0)。
//
// 跟流式路径的 stream forwarder 一致: 流式直接把 resp.StatusCode 写到 w,
// buffered 路径此前会丢失这层信息。
type UpstreamHTTPError struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

// Error 实现 error 接口, 只保留 status。上游 body 留在 Body 字段供分类和内部日志读取,
// 不能进入公开错误消息或 generic error string。
func (e *UpstreamHTTPError) Error() string {
	if e == nil {
		return "<nil UpstreamHTTPError>"
	}
	return fmt.Sprintf("dispatcher: 上游状态码 %d", e.StatusCode)
}

// RetryAfter 返回上游 Retry-After header 值 (秒), 找不到返回 0。caller 用于
// 决定 cooldown 时长 (429 + Retry-After 比 generic 429 信号更精确)。
func (e *UpstreamHTTPError) RetryAfter() string {
	if e == nil || e.Header == nil {
		return ""
	}
	return e.Header.Get("Retry-After")
}
