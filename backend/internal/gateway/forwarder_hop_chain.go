package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway/streamdelivery"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ApplyForwardRequestHopChain 把 T3 四跳网关链写入一个 HCSF
// envelope。Detail 故意留空,这样请求/响应内容
// 不会泄露进 hop attestation。
func ApplyForwardRequestHopChain(env *proto.HCSF, req ForwardRequest) {
	if env == nil {
		return
	}
	env.Accounting.HopChain = buildForwardHopChain(req, time.Now())
}

// BuildHopChain 返回一次已完成 non-streaming 请求的完整 audit 链：
// gateway ingress/router/pool/account 加 provider/response 两跳，且不写入
// prompt / response 内容。
func BuildHopChain(req ForwardRequest, providerEndpoint string, startedAt, completedAt time.Time) []proto.HopAttestation {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	if completedAt.IsZero() || completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	chain := buildForwardHopChain(req, startedAt)
	chain = append(chain,
		proto.HopAttestation{
			Hop:       proto.HopProvider,
			Timestamp: hopTimestamp(startedAt, 4),
			RequestID: req.RequestID,
			Provider:  req.Provider,
			Endpoint:  providerEndpoint,
		},
		proto.HopAttestation{
			Hop:        proto.HopResponse,
			Timestamp:  hopTimestamp(completedAt, 0),
			RequestID:  req.RequestID,
			DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		},
	)
	return chain
}

// FinalizeUpstream 调用 upstream adapter 的 finalizer,并用与 Forward
// 相同的 hop 链,给它返回的所有 HCSF envelope 加上标注。
func (f *StreamForwarder) FinalizeUpstream(ctx context.Context, adapter proto.UpstreamAdapter, state any, req ForwardRequest) ([]any, error) {
	if adapter == nil {
		return nil, nil
	}
	events, err := adapter.FinalizeUpstreamStream(ctx, state)
	if err != nil {
		return nil, err
	}
	annotateForwardHopChainEvents(events, req)
	return events, nil
}

func buildForwardHopChain(req ForwardRequest, now time.Time) []proto.HopAttestation {
	return []proto.HopAttestation{
		{
			Hop:       proto.HopIngress,
			Timestamp: hopTimestamp(now, 0),
			RequestID: req.RequestID,
		},
		{
			Hop:       proto.HopRouter,
			Timestamp: hopTimestamp(now, 1),
			RequestID: req.RequestID,
			RouteID:   req.RouteID,
		},
		{
			Hop:       proto.HopPool,
			Timestamp: hopTimestamp(now, 2),
			RequestID: req.RequestID,
			PoolID:    req.PoolID,
		},
		{
			Hop:           proto.HopAccount,
			Timestamp:     hopTimestamp(now, 3),
			RequestID:     req.RequestID,
			AccountIDHash: accountIDHash(req),
		},
	}
}

func annotateForwardHopChainEvent(event any, req ForwardRequest) any {
	switch env := event.(type) {
	case *proto.HCSF:
		ApplyForwardRequestHopChain(env, req)
		return env
	case proto.HCSF:
		ApplyForwardRequestHopChain(&env, req)
		return env
	default:
		return event
	}
}

func annotateForwardHopChainEvents(events []any, req ForwardRequest) {
	for i := range events {
		events[i] = annotateForwardHopChainEvent(events[i], req)
	}
}

func (f *StreamForwarder) emitFinalUpstreamEvents(
	ctx context.Context,
	adapter proto.UpstreamAdapter,
	state any,
	w http.ResponseWriter,
	clientState any,
	acc *UsageAccumulator,
	req ForwardRequest,
) error {
	events, err := f.FinalizeUpstream(ctx, adapter, state, req)
	if err != nil {
		return err
	}
	for _, canonical := range events {
		if usage, ok := canonicalUsage(canonical); ok {
			acc.Update(UsageSourceInferred, usage)
		}
		if canonicalTerminal(canonical) {
			acc.Freeze()
		}
		if f.ClientAdapter == nil {
			continue
		}
		chunks, clientLosses, err := f.clientChunks(ctx, canonical, clientState, SSEEvent{})
		if err != nil {
			return err
		}
		if len(clientLosses) > 0 {
			acc.StreamProtocolLoss = append(acc.StreamProtocolLoss, clientLosses...)
		}
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if err := streamdelivery.WriteAndFlush(w, chunk); err != nil {
				return ErrClientDisconnect
			}
		}
	}
	return nil
}

func hopTimestamp(now time.Time, offset int) string {
	return now.UTC().Add(time.Duration(offset) * time.Nanosecond).Format(time.RFC3339Nano)
}

func accountIDHash(req ForwardRequest) string {
	if req.AccountID == 0 {
		return ""
	}
	material := strconv.FormatInt(req.TenantID, 10) + ":" + strconv.FormatInt(req.AccountID, 10)
	if req.AcquisitionToken.String() != "" {
		material += ":" + req.AcquisitionToken.String()
	}
	sum := sha256.Sum256([]byte(material))
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:]))
}
