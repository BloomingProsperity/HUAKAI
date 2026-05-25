// Package gateway implements F-GW-002: streaming forwarder + usage accounting.
//
// See docs/specs/streaming-forwarder.md for the released spec.
package gateway

import (
	"context"
	"io"
	"net/http"
)

// Forwarder orchestrates F-GW-002 Phase A-D streaming handoff.
type Forwarder interface {
	Forward(ctx context.Context, upstreamReader io.Reader, clientWriter http.ResponseWriter, req ForwardRequest) (UsageRecordDraft, error)
}
