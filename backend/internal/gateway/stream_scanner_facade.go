package gateway

import (
	"context"
	"io"
	"iter"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway/streamscan"
)

// 流扫描的实现独立于网关编排；保留本包门面以稳定现有调用合同。
type SSEEvent = streamscan.SSEEvent
type StreamScanner = streamscan.StreamScanner
type StreamScannerRegistry = streamscan.StreamScannerRegistry
type StaticStreamScannerRegistry = streamscan.StaticStreamScannerRegistry
type SSEStreamScanner = streamscan.SSEStreamScanner
type NDJSONStreamScanner = streamscan.NDJSONStreamScanner
type BedrockEventStreamScanner = streamscan.BedrockEventStreamScanner

var (
	ErrUnknownStreamScanner = streamscan.ErrUnknownStreamScanner
	ErrScannerOverflow      = streamscan.ErrScannerOverflow
	ErrBedrockException     = streamscan.ErrBedrockException
	ErrBedrockChunkPayload  = streamscan.ErrBedrockChunkPayload
)

func NewStaticStreamScannerRegistry() *StaticStreamScannerRegistry {
	return streamscan.NewStaticStreamScannerRegistry()
}

func BuildDefaultStreamScannerRegistry() *StaticStreamScannerRegistry {
	return streamscan.BuildDefaultStreamScannerRegistry()
}

func ScanSSEEvents(ctx context.Context, reader io.Reader, bufferCap int) iter.Seq2[SSEEvent, error] {
	return streamscan.ScanSSEEvents(ctx, reader, bufferCap)
}
