// 包 gateway 实现 F-GW-002:流式转发器 + 用量计量。
//
// 当前转发与流式合同见 docs/HUAKAI工程设计手册.md §7。
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
