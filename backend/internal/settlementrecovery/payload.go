// 包 settlementrecovery 处理"流式 / 非流式响应已交付给客户端但 Tx2
// settlement 未确认提交"的 durable recovery intent。
//
// 调用方在 settle 失败(或 eventbus billing handler 失败)时把
// RequestCompletionEvent 转 Payload + Enqueue 到 usage_record_dlq 表,
// event_kind='post_delivery_settlement'。DLQ worker 拿到后重调
// public billing.Settler.Settle,并用三证 proof(claim committed +
// usage_records + billing_events) 区分"已成功提交"和"未提交",防重复扣费。
//
// 决策上下文:docs/process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md
package settlementrecovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// Source 标识 enqueue 入口,用于 observability + runbook 排查。
type Source string

const (
	// SourceStream:chat_completions_stream.go forwardSSEAndSettle 后 settle 失败。
	SourceStream Source = "stream"
	// SourceDirectSettle:chat_completions_billing.go settleCompletion 非流式
	// direct settle 失败(CompletionBus==nil 或 fallback)。
	SourceDirectSettle Source = "non_stream_direct"
	// SourceEventbusBillingHandler:observability/billing_persister_handler
	// 在 eventbus 内处理失败,委托 DLQ 重放。
	SourceEventbusBillingHandler Source = "eventbus_billing_handler"
	// SourceImagesDelivered 表示图片业务响应已完整交付后结算未确认。
	SourceImagesDelivered Source = "images_delivered"
	// SourceAudioDelivered 表示音频业务响应已完整交付后结算未确认。
	SourceAudioDelivered Source = "audio_delivered"
	// SourceEmbeddingsDelivered 表示嵌入业务响应已完整交付后结算未确认。
	SourceEmbeddingsDelivered Source = "embeddings_delivered"
	// SourceRerankDelivered 表示重排业务响应已完整交付后结算未确认。
	SourceRerankDelivered Source = "rerank_delivered"
)

// Payload 是 post_delivery_settlement DLQ 行的 JSON payload。
//
// 设计:不直接 JSON marshal billing.SettleRequest。Payload 镜像
// SettleRequest 所有可持久字段,worker 重 settle 时重构 SettleRequest。
// 后 scheduler outbox 改为 bool intent,必须持久化,否则原 Tx2
// 失败后 recovery 成功时会漏写 scheduler_outbox。
type Payload struct {
	Source                    Source                 `json:"source"`
	Settle                    settleRequestPersisted `json:"settle"`
	EventID                   string                 `json:"event_id,omitempty"`
	RequestID                 string                 `json:"request_id"`
	AuditLedgerID             string                 `json:"audit_ledger_id,omitempty"`
	AuditLedgerDLQRef         string                 `json:"audit_ledger_dlq_ref,omitempty"`
	AuditSignatureFingerprint string                 `json:"audit_signature_fingerprint,omitempty"`
}

// settleRequestPersisted 镜像 billing.SettleRequest 的可持久字段。
// 字段顺序跟 billing.SettleRequest 对齐(billing.go:78-105),方便审计两边漏字段。
type settleRequestPersisted struct {
	ClaimID             int64                    `json:"claim_id"`
	AccountID           int64                    `json:"account_id"`
	AcquisitionToken    uuid.UUID                `json:"acquisition_token"`
	UsageRecordPayload  json.RawMessage          `json:"usage_record_payload"`
	BillingEventPayload json.RawMessage          `json:"billing_event_payload"`
	ActualCost          decimal.Decimal          `json:"actual_cost"`
	TenantID            int64                    `json:"tenant_id"`
	APIKeyID            int64                    `json:"api_key_id"`
	UserID              int64                    `json:"user_id"`
	ProviderAccountID   int64                    `json:"provider_account_id"`
	AttemptSeq          int32                    `json:"attempt_seq"`
	RequestedModel      string                   `json:"requested_model"`
	RequestedAt         time.Time                `json:"requested_at"`
	UpstreamModel       string                   `json:"upstream_model"`
	Provider            string                   `json:"provider"`
	Stream              bool                     `json:"stream"`
	Draft               gateway.UsageRecordDraft `json:"draft"`
	// ProtocolLoss 镜像 billing.SettleRequest.ProtocolLoss(billing.go:99);
	// 之前缺此字段 → settle 失败 DLQ replay 重放时 usage_records.protocol_loss 退化成 "[]"。
	ProtocolLoss          json.RawMessage       `json:"protocol_loss,omitempty"`
	StreamAttempt         *billing.Attempt      `json:"stream_attempt,omitempty"`
	Fingerprint           string                `json:"fingerprint"`
	AuditRequestID        string                `json:"audit_request_id"`
	AuditRouteID          string                `json:"audit_route_id,omitempty"`
	AuditPoolGroupID      int64                 `json:"audit_pool_group_id,omitempty"`
	AuditProviderEndpoint string                `json:"audit_provider_endpoint,omitempty"`
	EmitSchedulerOutbox   bool                  `json:"emit_scheduler_outbox"`
	SnapshotVersion       string                `json:"snapshot_version"`
	BillingEffect         billing.BillingEffect `json:"billing_effect,omitempty"`
}

// Validate 失败原因 — worker 用这些判断"该 quarantine 还是默默重试"。
var (
	ErrPayloadMissingClaimID  = errors.New("settlementrecovery: payload missing claim_id")
	ErrPayloadMissingTenantID = errors.New("settlementrecovery: payload missing tenant_id")
	ErrPayloadInvalidSource   = errors.New("settlementrecovery: payload invalid source")
	ErrAuditRefPolicyNil      = errors.New("settlementrecovery: audit ref policy not configured")
)

// FromCompletionEvent 把 eventbus.RequestCompletionEvent 转 Payload。
//
// AuditRequestID 规范化兜底(finding 2026 05 24):上层 settleCompletion
// / Handler.Handle 都是在**栈本地副本**上把 SettleRequest.AuditRequestID 补成
// event.RequestID,recovery payload 构造时拿到的是外层未规范化的原始 event ——
// 不在此处兜底,worker 重放写 NULL audit_request_id,断 audit/receipt 关联。
// 单点兜底 = 守所有 caller(stream / eventbus billing handler / 未来新 source)。
//
// SettleRequest→Payload 的字段映射统一由 FromSettleRequest 承载(单点),本函数只补
// eventbus 专属的 EventID / AuditLedgerDLQRef。
func FromCompletionEvent(src Source, event eventbus.RequestCompletionEvent) Payload {
	p := FromSettleRequest(src, event.RequestID, event.SettleRequest)
	p.EventID = event.ID
	p.AuditLedgerID = event.AuditLedgerID
	p.AuditLedgerDLQRef = event.AuditLedgerDLQRef
	p.AuditSignatureFingerprint = event.AuditSignatureFingerprint
	return p
}

// FromSettleRequest 把一个 billing.SettleRequest + requestID 直接转 Payload,供不经
// eventbus 的直接 settle 路径(如 completionshttp /v1/completions 流式交付后结算)在
// settle 失败时构造 recovery intent。settleRequestPersisted 是未导出类型,包外无法
// 直接构造,故此构造器是 completions 等 handler 落 DLQ 的唯一入口。AuditRequestID
// 规范化兜底逻辑与 FromCompletionEvent 一致(同一单点)。
func FromSettleRequest(src Source, requestID string, req billing.SettleRequest) Payload {
	auditRequestID := req.AuditRequestID
	if auditRequestID == "" {
		auditRequestID = requestID
	}
	return Payload{
		Source:    src,
		RequestID: requestID,
		Settle: settleRequestPersisted{
			ClaimID:               req.ClaimID,
			AccountID:             req.AccountID,
			AcquisitionToken:      req.AcquisitionToken,
			UsageRecordPayload:    json.RawMessage(req.UsageRecordPayload),
			BillingEventPayload:   json.RawMessage(req.BillingEventPayload),
			ActualCost:            req.ActualCost,
			TenantID:              req.TenantID,
			APIKeyID:              req.APIKeyID,
			UserID:                req.UserID,
			ProviderAccountID:     req.ProviderAccountID,
			AttemptSeq:            req.AttemptSeq,
			RequestedModel:        req.RequestedModel,
			RequestedAt:           req.RequestedAt,
			UpstreamModel:         req.UpstreamModel,
			Provider:              req.Provider,
			Stream:                req.Stream,
			Draft:                 req.Draft,
			ProtocolLoss:          req.ProtocolLoss,
			StreamAttempt:         req.StreamAttempt,
			Fingerprint:           req.Fingerprint,
			AuditRequestID:        auditRequestID,
			AuditRouteID:          req.AuditRouteID,
			AuditPoolGroupID:      req.AuditPoolGroupID,
			AuditProviderEndpoint: req.AuditProviderEndpoint,
			EmitSchedulerOutbox:   req.EmitSchedulerOutbox,
			SnapshotVersion:       req.SnapshotVersion,
			BillingEffect:         req.BillingEffect,
		},
	}
}

// Validate 在 enqueue 前 + worker decode 后两端调,确保 payload 不破坏 settle 必填条件。
func (p Payload) Validate() error {
	switch p.Source {
	case SourceStream, SourceDirectSettle, SourceEventbusBillingHandler, SourceImagesDelivered, SourceAudioDelivered,
		SourceEmbeddingsDelivered, SourceRerankDelivered:
	default:
		return fmt.Errorf("%w: %q", ErrPayloadInvalidSource, p.Source)
	}
	if p.Settle.ClaimID == 0 {
		return ErrPayloadMissingClaimID
	}
	if p.Settle.TenantID == 0 {
		return ErrPayloadMissingTenantID
	}
	return nil
}

// ValidateAuditRef 对来自 RequestCompletionEvent 的恢复 payload 复用 inline/eventbus
// 同一审计引用口径。直接 SettleRequest 来源没有 event_id，保持其既有 inline 语义。
func (p Payload) ValidateAuditRef(policy *eventbus.AuditRefPolicy) error {
	if p.EventID == "" {
		return nil
	}
	if policy == nil {
		return ErrAuditRefPolicyNil
	}
	event := eventbus.RequestCompletionEvent{
		ID:                        p.EventID,
		TenantID:                  p.Settle.TenantID,
		ClaimID:                   p.Settle.ClaimID,
		RequestID:                 p.RequestID,
		AuditLedgerID:             p.AuditLedgerID,
		AuditLedgerDLQRef:         p.AuditLedgerDLQRef,
		AuditSignatureFingerprint: p.AuditSignatureFingerprint,
	}
	if err := eventbus.ValidateMoneyPathAuditRef(&event, policy); err != nil {
		return fmt.Errorf("settlementrecovery: validate audit ref: %w", err)
	}
	return nil
}

// ToSettleRequest 把 Payload 转 billing.SettleRequest,worker 拿去重调
// Settler.Settle。EmitSchedulerOutbox 保留原 intent,确保 recovery 成功的
// Tx2 与正常 Tx2 写同等 scheduler outbox 证据。
func (p Payload) ToSettleRequest() billing.SettleRequest {
	return billing.SettleRequest{
		ClaimID:               p.Settle.ClaimID,
		AccountID:             p.Settle.AccountID,
		AcquisitionToken:      p.Settle.AcquisitionToken,
		UsageRecordPayload:    []byte(p.Settle.UsageRecordPayload),
		BillingEventPayload:   []byte(p.Settle.BillingEventPayload),
		ActualCost:            p.Settle.ActualCost,
		TenantID:              p.Settle.TenantID,
		APIKeyID:              p.Settle.APIKeyID,
		UserID:                p.Settle.UserID,
		ProviderAccountID:     p.Settle.ProviderAccountID,
		AttemptSeq:            p.Settle.AttemptSeq,
		RequestedModel:        p.Settle.RequestedModel,
		RequestedAt:           p.Settle.RequestedAt,
		UpstreamModel:         p.Settle.UpstreamModel,
		Provider:              p.Settle.Provider,
		Stream:                p.Settle.Stream,
		Draft:                 p.Settle.Draft,
		ProtocolLoss:          p.Settle.ProtocolLoss,
		StreamAttempt:         p.Settle.StreamAttempt,
		Fingerprint:           p.Settle.Fingerprint,
		AuditRequestID:        p.Settle.AuditRequestID,
		AuditRouteID:          p.Settle.AuditRouteID,
		AuditPoolGroupID:      p.Settle.AuditPoolGroupID,
		AuditProviderEndpoint: p.Settle.AuditProviderEndpoint,
		EmitSchedulerOutbox:   p.Settle.EmitSchedulerOutbox,
		SnapshotVersion:       p.Settle.SnapshotVersion,
		BillingEffect:         p.Settle.BillingEffect,
	}
}

// Encode 给 dlq.Event.Payload 用。
func (p Payload) Encode() ([]byte, error) {
	return json.Marshal(p)
}

// Decode 从 dlq.Event.Payload 还原 Payload。
func Decode(raw []byte) (Payload, error) {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, fmt.Errorf("settlementrecovery: decode payload: %w", err)
	}
	return p, nil
}
