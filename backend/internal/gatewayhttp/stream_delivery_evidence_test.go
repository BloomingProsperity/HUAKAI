package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type streamWriteResult struct {
	written int
	err     error
}

type streamScriptedWriter struct {
	header  http.Header
	results []streamWriteResult
	calls   int
	flushes int
}

func (w *streamScriptedWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*streamScriptedWriter) WriteHeader(int) {}

func (w *streamScriptedWriter) Write(body []byte) (int, error) {
	result := streamWriteResult{written: -1}
	if w.calls < len(w.results) {
		result = w.results[w.calls]
	}
	w.calls++
	n := result.written
	if n < 0 || n > len(body) {
		n = len(body)
	}
	return n, result.err
}

func (w *streamScriptedWriter) Flush() { w.flushes++ }

type fixedChunkClientAdapter struct{}

func (fixedChunkClientAdapter) RequestToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("测试未使用 RequestToCanonical")
}

func (fixedChunkClientAdapter) CanonicalToClientResponse(context.Context, *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("测试未使用 CanonicalToClientResponse")
}

func (fixedChunkClientAdapter) CanonicalEventToClientChunk(context.Context, any, any) ([][]byte, []proto.ProtocolLossEntry, error) {
	return [][]byte{[]byte("data: translated-business\n\n")}, nil, nil
}

func (fixedChunkClientAdapter) FinalizeClientStream(context.Context, any) ([][]byte, error) {
	return nil, nil
}

// TestStreamDeliveryEvidence_FirstFrameZeroOrShortWriteAborts 覆盖 raw 直通与翻译路径。
// fixture 的上游内容会先产生正数 DeliveredTokenCount；恢复 token→交付推断变异后，
// 本测试会从 Abort 变成 Settle，直接变红。
func TestStreamDeliveryEvidence_FirstFrameZeroOrShortWriteAborts(t *testing.T) {
	tests := []struct {
		name          string
		clientAdapter proto.ClientAdapter
		writeResult   streamWriteResult
	}{
		{name: "raw_首帧零写", writeResult: streamWriteResult{written: 0, err: io.ErrClosedPipe}},
		{name: "raw_首帧短写", writeResult: streamWriteResult{written: 1}},
		{name: "翻译_首帧零写", clientAdapter: fixedChunkClientAdapter{}, writeResult: streamWriteResult{written: 0, err: io.ErrClosedPipe}},
		{name: "翻译_首帧短写", clientAdapter: fixedChunkClientAdapter{}, writeResult: streamWriteResult{written: 1}},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			settler := &recordingSettler{}
			ex := newStreamDeliveryEvidenceExecution(t, int64(88000+i), settler)
			w := &streamScriptedWriter{results: []streamWriteResult{tc.writeResult}}

			delivered, failure := ex.forwardSSEAndSettle(w, streamDeliveryDispatchResult(), time.Now(), tc.clientAdapter)

			if delivered {
				t.Fatal("首帧未整帧写成功却报告已交付")
			}
			if failure == nil {
				t.Fatal("首帧写失败且无历史交付时必须返回可重试失败")
			}
			if len(settler.calls) != 0 {
				t.Fatalf("Settle calls=%d want 0", len(settler.calls))
			}
			if len(settler.aborts) != 1 {
				t.Fatalf("Abort calls=%d want 1", len(settler.aborts))
			}
		})
	}
}

// TestStreamDeliveryEvidence_FullFrameBeforeShortWriteStillSettles 守住历史交付单调性：
// 第一帧完整交付后第二帧短写，最终仍应结算，不能把已交付响应 Abort 掉。
func TestStreamDeliveryEvidence_FullFrameBeforeShortWriteStillSettles(t *testing.T) {
	tests := []struct {
		name          string
		clientAdapter proto.ClientAdapter
	}{
		{name: "raw"},
		{name: "翻译", clientAdapter: fixedChunkClientAdapter{}},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			settler := &recordingSettler{}
			ex := newStreamDeliveryEvidenceExecution(t, int64(88100+i), settler)
			w := &streamScriptedWriter{results: []streamWriteResult{
				{written: -1},
				{written: 1},
			}}

			delivered, failure := ex.forwardSSEAndSettle(w, streamDeliveryDispatchResult(), time.Now(), tc.clientAdapter)

			if !delivered {
				t.Fatal("第一帧已完整写入，后续短写不得抹掉历史交付")
			}
			if failure != nil {
				t.Fatalf("已有业务交付后 forward failure=%v want nil", failure)
			}
			if len(settler.calls) != 1 || len(settler.aborts) != 0 {
				t.Fatalf("Settle/Abort calls=%d/%d want 1/0", len(settler.calls), len(settler.aborts))
			}
		})
	}
}

func newStreamDeliveryEvidenceExecution(t *testing.T, claimID int64, settler billing.Settler) *chatExecution {
	t.Helper()
	body := []byte(openAIStreamingRequestBody())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	deps := streamingReplayDeps(t, claimID, false, "", nil)
	deps.Settler = settler
	return &chatExecution{
		d:                 deps,
		r:                 req,
		ctx:               req.Context(),
		startedAt:         time.Now().UTC(),
		ident:             validIdentity(),
		body:              body,
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		clientProtocol:    proto.ClientProtocolOpenAIChat,
		requestID:         "req-stream-delivery-evidence",
		resolved:          registry.Resolved{ProtocolFamily: "openai_chat", ProviderModelID: "gpt-4o"},
		plan:              router.RoutePlan{SnapshotVersion: "stream-delivery-evidence"},
		reserveRes:        &billing.ReserveResult{ClaimID: claimID},
		acquiredAccountID: 1,
		upstreamModelID:   "gpt-4o",
		cacheVendor:       "openai",
		currentAttemptSeq: 1,
		forwardReq: gateway.ForwardRequest{
			TenantID:       validIdentity().TenantID,
			AccountID:      1,
			RequestID:      "req-stream-delivery-evidence",
			ProtocolFamily: "openai_chat",
			ClientProtocol: string(proto.ClientProtocolOpenAIChat),
			Model:          "gpt-4o",
			RequestedModel: "gpt-4o",
			Provider:       "openai",
		},
	}
}

func streamDeliveryDispatchResult() *gateway.DispatchResult {
	return &gateway.DispatchResult{
		UpstreamReader: strings.NewReader(openAIStreamingFixture()),
		StatusCode:     http.StatusOK,
		Headers:        make(http.Header),
	}
}
