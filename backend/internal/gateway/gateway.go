// 包 gateway 实现 F-GW-002:流式转发器 + 用量计量。
//
// 已发布的规范见 docs/specs/streaming-forwarder.md。
package gateway

import (
	"context"
	"io"
	"net/http"
)

// Forwarder 编排 F-GW-002 Phase A-D 的流式交接。
type Forwarder interface {
	Forward(ctx context.Context, upstreamReader io.Reader, clientWriter http.ResponseWriter, req ForwardRequest) (UsageRecordDraft, error)
}
