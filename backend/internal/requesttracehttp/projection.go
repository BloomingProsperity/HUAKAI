package requesttracehttp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	tracedb "github.com/BloomingProsperity/HUAKAI/internal/db/requesttracedb"
)

// project 拼装并按档位脱敏。tenantID<=0 只允许部署者(跨租户);userID>0 只用于最终用户。
func project(ctx context.Context, store Store, requestID string, tenantID, userID int64, viewer Viewer, now time.Time) (*Response, error) {
	claim, err := store.GetRequestTraceClaim(ctx, tracedb.GetRequestTraceClaimParams{
		RequestID: requestID, TenantID: tenantID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	// 后续事实全部限定在 claim 所属租户,部署者跨租户查询也不会越出该租户。审计与收据表写的是网关 HTTP
	// 请求标识,而账本 claim 用逻辑标识:用 claim 关联的全部标识(路径标识 + 逻辑标识 + HTTP 标识)去 join,
	// 两把键不一致时时间线也不会缺半边。
	scope := claim.TenantID
	ids := relatedRequestIDs(requestID, claim)
	usage, err := store.ListRequestTraceUsageRecords(ctx, tracedb.ListRequestTraceUsageRecordsParams{ClaimID: claim.ID, TenantID: scope})
	if err != nil {
		return nil, err
	}
	billing, err := store.ListRequestTraceBillingEvents(ctx, tracedb.ListRequestTraceBillingEventsParams{ClaimID: claim.ID, TenantID: scope})
	if err != nil {
		return nil, err
	}
	var audit []tracedb.ListRequestTraceAuditEventsRow
	if viewer != ViewerUser {
		audit, err = store.ListRequestTraceAuditEvents(ctx, tracedb.ListRequestTraceAuditEventsParams{RequestIds: ids, TenantID: scope})
		if err != nil {
			return nil, err
		}
	}
	// 租户未知的历史信任链条目只对部署者可见(且已由 claim 锁定租户);租户管理员与用户只读明确归属本租户的。
	receiptRow, err := store.GetRequestTraceReceipt(ctx, tracedb.GetRequestTraceReceiptParams{RequestIds: ids, TenantID: scope, AllowNullTenant: viewer == ViewerPlatform})
	hasReceipt := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	costRow, err := store.GetRequestTraceCostReceipt(ctx, tracedb.GetRequestTraceCostReceiptParams{RequestIds: ids, TenantID: scope})
	hasCostReceipt := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	resp := &Response{Viewer: viewer, Attempts: []Attempt{}, Events: []Event{}, Totals: zeroUsage()}
	resp.Request = projectHead(requestID, claim, viewer)
	routingUnavailable := 0
	for _, row := range usage {
		attempt, routingOK := projectAttempt(row, viewer)
		if !routingOK {
			routingUnavailable++
		}
		resp.Attempts = append(resp.Attempts, attempt)
		addUsage(&resp.Totals, attempt.Usage)
	}
	timeline := make([]timedEvent, 0, len(billing)+len(audit))
	for _, be := range billing {
		timeline = append(timeline, timedEvent{at: be.OccurredAt.Time, event: projectBillingEvent(be)})
	}
	for _, ae := range audit {
		timeline = append(timeline, timedEvent{at: ae.OccurredAt.Time, event: projectAuditEvent(ae, viewer)})
	}
	sort.SliceStable(timeline, func(i, j int) bool { return timeline[i].at.Before(timeline[j].at) })
	for _, te := range timeline {
		resp.Events = append(resp.Events, te.event)
	}
	if hasReceipt {
		resp.Receipt = projectReceipt(receiptRow, viewer)
	}
	if hasCostReceipt {
		resp.CostReceipt = &CostReceipt{
			RequestID: costRow.RequestID, ReceiptSequence: costRow.ReceiptSequence, Model: costRow.Model,
			InputTokens: costRow.InputTokens, OutputTokens: costRow.OutputTokens, CachedTokens: costRow.CachedTokens,
			CostUSDMicros: costRow.CostUsdMicros, CreatedAt: formatTime(costRow.CreatedAt),
		}
	}
	resp.Coverage = buildCoverage(claim, usage, billing, audit, hasReceipt, hasCostReceipt, now)
	if routingUnavailable > 0 && viewer != ViewerUser {
		resp.Coverage.Notes = append(resp.Coverage.Notes, "routing_digest_unavailable")
	}
	return resp, nil
}

// relatedRequestIDs 收集同一请求的全部标识:查询所用标识、账本逻辑标识与账本事件记录的 HTTP 标识,去重。
func relatedRequestIDs(requestID string, claim tracedb.GetRequestTraceClaimRow) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 2+len(claim.HttpRequestIds))
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(requestID)
	add(claim.LogicalRequestID)
	for _, id := range claim.HttpRequestIds {
		add(id)
	}
	return out
}

func projectHead(requestID string, claim tracedb.GetRequestTraceClaimRow, viewer Viewer) RequestHead {
	httpIDs := claim.HttpRequestIds
	if httpIDs == nil {
		httpIDs = []string{}
	}
	head := RequestHead{
		RequestID:        requestID,
		LogicalRequestID: claim.LogicalRequestID,
		HTTPRequestIDs:   httpIDs,
		EndpointFamily:   claim.EndpointFamily,
		RequestedModel:   claim.RequestedModel,
		RequestClass:     claim.RequestClass,
		BillingEffect:    claim.BillingEffect,
		PoolName:         claim.PoolName,
		Status:           claim.Status,
		LastAttemptSeq:   claim.AttemptSeq,
		PredictedCost:    claim.PredictedCost.String(),
		Currency:         claim.CurrencyCode,
		ReservedAt:       formatTime(claim.ReservedAt),
		SettledAt:        optionalTime(claim.SettledAt),
	}
	if claim.ActualCost.Valid {
		s := claim.ActualCost.Decimal.String()
		head.ActualCost = &s
	}
	if claim.Status == "aborted" {
		reason := ""
		if claim.AbortedReason != nil {
			reason = *claim.AbortedReason
		}
		head.FailureCategory = failureCategory(reason)
	}
	if viewer != ViewerUser {
		head.PoolGroupID = claim.PoolingGroupID
		if claim.ProviderAccountID != nil {
			head.FinalAccount = &AccountRef{ProviderAccountID: *claim.ProviderAccountID}
		}
		// 中止原因是网关产生的分类短文,不含上游原文;仍只给运营侧。
		head.AbortedReason = claim.AbortedReason
	}
	if viewer == ViewerPlatform {
		tenant, user, key := claim.TenantID, claim.UserID, claim.APIKeyID
		head.TenantID, head.UserID, head.APIKeyID = &tenant, &user, &key
	}
	return head
}

// projectAttempt 返回投影后的尝试;routingOK=false 表示选号解释存在但无法解析(运营档位打注记)。
func projectAttempt(row tracedb.ListRequestTraceUsageRecordsRow, viewer Viewer) (Attempt, bool) {
	attempt := Attempt{
		AttemptSeq:            row.AttemptSeq,
		Outcome:               outcomeForAttempt(row),
		EndClass:              row.EndClass,
		RequestedModel:        row.RequestedModel,
		Stream:                row.Stream,
		UsageSource:           row.UsageSource,
		SettlementSource:      row.SettlementSource,
		PendingReconciliation: row.PendingReconciliation,
		Usage: AttemptUsage{
			TokensInput:         int64(row.TokensInput),
			TokensOutput:        int64(row.TokensOutput),
			CacheCreationTokens: int64(row.CacheCreationTokens),
			CacheReadTokens:     int64(row.CacheReadTokens),
			ImageOutputTokens:   int64(row.ImageOutputTokens),
			DeliveredTokens:     row.DeliveredTokenCount,
			ActualCost:          row.ActualCost.String(),
			InputCost:           row.InputCost.String(),
			OutputCost:          row.OutputCost.String(),
			CacheCreationCost:   row.CacheCreationCost.String(),
			CacheReadCost:       row.CacheReadCost.String(),
			ImageOutputCost:     row.ImageOutputCost.String(),
		},
		Timing: AttemptTiming{
			RequestedAt:       formatTime(row.RequestedAt),
			UpstreamRequestAt: optionalTime(row.UpstreamRequestAt),
			FirstByteAt:       optionalTime(row.FirstByteAt),
			FirstEventAt:      optionalTime(row.FirstEventAt),
			LastEventAt:       optionalTime(row.LastEventAt),
			SettledAt:         formatTime(row.SettledAt),
		},
		ProtocolLossCount: countJSONArray(row.ProtocolLoss),
	}
	if attempt.Outcome != "success" {
		reason := row.EndClass
		if row.StreamTerminatedReason != nil && *row.StreamTerminatedReason != "" {
			reason = *row.StreamTerminatedReason
		}
		attempt.FailureCategory = failureCategory(reason)
	}
	routingOK := true
	if viewer != ViewerUser {
		// 厂商代号与上游实际模型名属于上游声明标识,最终用户只看自己请求的模型。
		attempt.Provider = row.ProviderCode
		attempt.UpstreamModel = row.UpstreamModel
		attempt.TerminatedReason = row.StreamTerminatedReason
		if row.ProviderAccountID != nil {
			attempt.Account = &AccountRef{
				ProviderAccountID: *row.ProviderAccountID,
				Name:              row.ProviderAccountName,
				ChannelID:         row.ChannelID,
			}
		}
		attempt.Routing, routingOK = projectRouting(row.RoutingReason, viewer)
	}
	return attempt, routingOK
}

// 账务态与结算链一致(见 billing.StreamState):3 = 失败(中止写入的用量行)。
const streamStateFailed = 3

// outcomeForAttempt 把一次尝试折成最终用户也能理解的结果档:先看账务态(中止写入的行账务态为失败、
// 零交付),再看结束分类;只有确有交付却未正常收尾的才算部分成功。
func outcomeForAttempt(row tracedb.ListRequestTraceUsageRecordsRow) string {
	switch row.EndClass {
	case "stream_end_graceful", "non_streaming":
		return "success"
	case "client_disconnect", "orchestrator_cancelled":
		return "cancelled"
	}
	if row.StreamState == streamStateFailed || (row.DeliveredTokenCount == 0 && row.TokensOutput == 0) {
		return "failed"
	}
	// 有交付但没有明确失败原因(缺收尾标记 / 用量歧义 / 终止原因未知)才是部分成功;
	// 明确的上游错误、限流、鉴权失败与超时即使已交付部分内容也按失败呈现,交付量另见 usage。
	switch row.EndClass {
	case "stream_end_no_terminal_marker", "usage_ambiguous", "unknown_termination":
		return "partial"
	}
	return "failed"
}

// knownFailureCategories 是网关真实中止/结束分类码到九类粗分类的精确表;子串规则只做兜底。
var knownFailureCategories = map[string]string{
	"pool_no_capacity":                    "routing_unavailable",
	"pool_select_no_account":              "routing_unavailable",
	"queue_wait_cancelled":                "routing_unavailable",
	"pool_exhausted":                      "routing_unavailable",
	"agent_task_invalid":                  "invalid_request",
	"credential_protocol_incompatible":    "invalid_request",
	"invalid_request_body":                "invalid_request",
	"streaming_translation_not_supported": "invalid_request",
	"streaming_adapter_unregistered":      "invalid_request",
	"non_streaming_not_yet_wired":         "invalid_request",
	"upstream_response_too_large":         "invalid_request",
	"upstream_forbidden":                  "upstream",
	"upstream_empty_response":             "upstream",
	"upstream_adapter_error":              "upstream",
	"upstream_dispatch_error":             "upstream",
	"upstream_auth_failure":               "auth",
	"credential_resolve_error":            "auth",
	"quota_denied":                        "quota",
	"audit_ledger_error":                  "internal",
	"pricing_unavailable":                 "internal",
	"cache_key_error":                     "internal",
	"canonical_response_error":            "internal",
	"client_response_write_error":         "internal",
	"delivery_evidence_unavailable":       "internal",
	"media_task_insert_failed":            "internal",
	"lease_expired":                       "internal",
	"client_disconnect":                   "other",
	"orchestrator_cancelled":              "other",
	"unknown_termination":                 "other",
	"usage_ambiguous":                     "other",
	"stream_end_no_terminal_marker":       "other",
}

// failureCategory 把网关分类码折成合同要求的九类粗分类;原始码只给运营侧。
func failureCategory(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if known, ok := knownFailureCategories[c]; ok {
		return known
	}
	switch {
	case c == "":
		return "other"
	case strings.HasPrefix(c, "transport_") || strings.Contains(c, "connection_refused") || strings.Contains(c, "dns_failure") || strings.Contains(c, "tls_handshake") || strings.Contains(c, "proxy_failure"):
		return "upstream"
	case strings.Contains(c, "no_capacity") || strings.Contains(c, "no_candidate"):
		return "routing_unavailable"
	case strings.Contains(c, "client_disconnect") || strings.Contains(c, "orchestrator_cancel"):
		return "other"
	case strings.Contains(c, "auth") || strings.Contains(c, "credential") || strings.Contains(c, "unauthorized") || strings.Contains(c, "forbidden") || strings.Contains(c, "invalid_grant"):
		return "auth"
	case strings.Contains(c, "quota") || strings.Contains(c, "balance") || strings.Contains(c, "insufficient") || strings.Contains(c, "budget"):
		return "quota"
	case strings.Contains(c, "rate_limit") || strings.Contains(c, "429") || strings.Contains(c, "overload") || strings.Contains(c, "throttl"):
		return "rate_limit"
	case strings.Contains(c, "moderation") || strings.Contains(c, "policy") || strings.Contains(c, "safety") || strings.Contains(c, "blocked"):
		return "security_policy"
	case strings.Contains(c, "invalid_request") || strings.Contains(c, "invalid_") || strings.Contains(c, "not_supported") || strings.Contains(c, "unsupported") || strings.Contains(c, "incompatible") || strings.Contains(c, "too_large") || strings.Contains(c, "malformed"):
		return "invalid_request"
	case strings.Contains(c, "pool_select") || strings.Contains(c, "no_account") || strings.Contains(c, "rout") || strings.Contains(c, "queue_wait") || strings.Contains(c, "exhausted") || strings.Contains(c, "no_candidate"):
		return "routing_unavailable"
	case strings.Contains(c, "upstream") || strings.Contains(c, "timeout") || strings.Contains(c, "5xx") || strings.Contains(c, "4xx") || strings.Contains(c, "dispatch") || strings.Contains(c, "event_size"):
		return "upstream"
	case strings.Contains(c, "ambiguous") || strings.Contains(c, "unknown_termination") || strings.Contains(c, "no_terminal_marker"):
		return "other"
	default:
		return "internal"
	}
}

// routingReasonWire 只解出投影需要的字段;账号 id 只在部署者档位回传。
type routingReasonWire struct {
	SelectionLayer             string         `json:"selection_layer"`
	AffinityKeyClass           string         `json:"affinity_key_class"`
	StickyBreakReason          *string        `json:"sticky_break_reason"`
	CapabilityOutcome          string         `json:"capability_outcome"`
	CandidateCountsByExclusion map[string]int `json:"candidate_counts_by_exclusion"`
	ScoringPolicyVersion       string         `json:"scoring_policy_version"`
	RetryAttemptNumber         int            `json:"retry_attempt_number"`
	WaitAction                 *struct {
		ExitReason string `json:"exit_reason"`
	} `json:"wait_action"`
	PerRequestExclusionSummary []struct {
		AccountID int64  `json:"account_id"`
		Reason    string `json:"reason"`
	} `json:"per_request_exclusion_summary"`
	ForcedRouteOverrideActor any `json:"forced_route_override_actor"`
}

// projectRouting 解出选号解释摘要;没有解释返回 (nil, true),有解释但无法解析返回 (nil, false)。
func projectRouting(raw []byte, viewer Viewer) (*RoutingDigest, bool) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, true
	}
	var wire routingReasonWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, false
	}
	digest := &RoutingDigest{
		SelectionLayer:      wire.SelectionLayer,
		AffinityKeyClass:    wire.AffinityKeyClass,
		StickyBreakReason:   wire.StickyBreakReason,
		CapabilityOutcome:   wire.CapabilityOutcome,
		RetryAttemptNumber:  wire.RetryAttemptNumber,
		ExclusionCounts:     wire.CandidateCountsByExclusion,
		ScoringPolicy:       wire.ScoringPolicyVersion,
		WaitedInQueue:       wire.WaitAction != nil,
		ForcedRouteOverride: wire.ForcedRouteOverrideActor != nil,
	}
	if wire.WaitAction != nil {
		digest.QueueExitReason = wire.WaitAction.ExitReason
	}
	if viewer == ViewerPlatform {
		for _, item := range wire.PerRequestExclusionSummary {
			digest.ExcludedAccounts = append(digest.ExcludedAccounts, ExcludedAccount{ProviderAccountID: item.AccountID, Reason: item.Reason})
		}
	}
	return digest, true
}

func projectBillingEvent(be tracedb.ListRequestTraceBillingEventsRow) Event {
	cost := be.ActualCostSigned.String()
	return Event{
		Source:      "billing",
		EventType:   be.EventType,
		EndClass:    be.EndClass,
		UsageSource: be.UsageSource,
		ActualCost:  &cost,
		OccurredAt:  formatTime(be.OccurredAt),
	}
}

func projectAuditEvent(ae tracedb.ListRequestTraceAuditEventsRow, viewer Viewer) Event {
	ev := Event{
		Source:        ae.Source,
		EventType:     ae.EventType,
		Reason:        ae.Reason,
		PreviousState: ae.PreviousState,
		NewState:      ae.NewState,
		OccurredAt:    formatTime(ae.OccurredAt),
	}
	// 路由审计的 reason 是自由文本(可能引用具体账号/策略),租户管理员只看事件类型,部署者看全文;
	// 其余来源的 reason 都是枚举分类。
	if viewer != ViewerPlatform && ae.Source == "pool_routing" {
		ev.Reason = ""
	}
	if viewer != ViewerUser && ae.ProviderAccountID != nil {
		ev.Account = &AccountRef{ProviderAccountID: *ae.ProviderAccountID}
	}
	return ev
}

func projectReceipt(row tracedb.GetRequestTraceReceiptRow, viewer Viewer) *Receipt {
	receipt := &Receipt{
		LedgerID:          row.LedgerID,
		OccurredAt:        formatTime(row.OccurredAt),
		PubkeyFingerprint: row.PubkeyFingerprint,
	}
	// 模型链含上游实际模型名(上游声明标识),最终用户只看自己请求的模型;运营侧可见。
	if viewer != ViewerUser && len(row.ModelChain) > 0 && json.Valid(row.ModelChain) {
		receipt.ModelChain = json.RawMessage(row.ModelChain)
	}
	// 跳点链含上游跳点代号,只回传给部署者。
	if viewer == ViewerPlatform && len(row.HopChain) > 0 && json.Valid(row.HopChain) {
		receipt.HopChain = json.RawMessage(row.HopChain)
	}
	return receipt
}

// buildCoverage 按 §4.5 的降级合同给出结构化标记;不用文案区分"无权 / 不存在"。
func buildCoverage(claim tracedb.GetRequestTraceClaimRow, usage []tracedb.ListRequestTraceUsageRecordsRow, billing []tracedb.ListRequestTraceBillingEventsRow, audit []tracedb.ListRequestTraceAuditEventsRow, hasReceipt, hasCostReceipt bool, now time.Time) Coverage {
	cov := Coverage{
		HasUsage:         len(usage) > 0,
		HasBillingEvents: len(billing) > 0,
		HasAuditEvents:   len(audit) > 0,
		HasReceipt:       hasReceipt,
		HasCostReceipt:   hasCostReceipt,
		Notes:            []string{},
	}
	for _, be := range billing {
		switch be.EventType {
		case "claim_aborted":
			cov.AbortedAttempts++
		case "claim_committed":
			cov.CommittedAttempts++
		}
	}
	settledAttempts := map[int32]bool{}
	for _, row := range usage {
		settledAttempts[row.AttemptSeq] = true
	}
	// 账本里出现过的尝试数 = 中止 + 提交事件数(每次尝试恰结束于其一);超出用量行覆盖的部分
	// 就是零用量中止(账号未知)。
	ledgerAttempts := cov.AbortedAttempts + cov.CommittedAttempts
	if ledgerAttempts > len(settledAttempts) {
		cov.UnknownAttemptAccount = ledgerAttempts - len(settledAttempts)
	}
	switch {
	case len(usage) == 0 && ledgerAttempts == 0:
		cov.AttemptsDetail = AttemptsDetailNone
		cov.Notes = append(cov.Notes, "no_attempt_facts")
	case len(usage) == 0:
		cov.AttemptsDetail = AttemptsDetailFinalOnly
		cov.Notes = append(cov.Notes, "only_ledger_events")
	case cov.UnknownAttemptAccount > 0:
		cov.AttemptsDetail = AttemptsDetailPartial
		cov.Notes = append(cov.Notes, "pre_acquire_aborts_have_no_account")
	default:
		cov.AttemptsDetail = AttemptsDetailFull
	}
	switch {
	case len(usage) > 0:
		cov.DetailRetention = DetailRetentionWithin
	case cov.CommittedAttempts > 0 || claim.Status == "committed":
		// 有已提交的账本事实却没有任何用量行:刚结算的请求用量还在异步落盘(pending),
		// 早已结算的则是明细已被保留期清理或落盘失败进了死信(expired)。
		if claim.SettledAt.Valid && now.Sub(claim.SettledAt.Time) < usagePersistGrace {
			cov.DetailRetention = DetailRetentionPending
			cov.Notes = append(cov.Notes, "usage_detail_not_yet_persisted")
		} else {
			cov.DetailRetention = DetailRetentionExpired
			cov.Notes = append(cov.Notes, "usage_detail_expired_or_dead_lettered")
		}
	default:
		cov.DetailRetention = DetailRetentionNoDetail
	}
	if claim.Status == "reserving" {
		cov.Notes = append(cov.Notes, "in_flight_or_unsettled")
	}
	if !hasReceipt {
		cov.Notes = append(cov.Notes, "no_receipt")
	}
	if claim.CandidateCount > 1 {
		cov.Notes = append(cov.Notes, "multiple_claims_for_id")
	}
	return cov
}

// usagePersistGrace 是结算后等待用量明细异步落盘的窗口;窗口内无用量行按 pending 而不是 expired 标记。
const usagePersistGrace = 10 * time.Minute

func zeroUsage() AttemptUsage {
	return AttemptUsage{ActualCost: "0", InputCost: "0", OutputCost: "0", CacheCreationCost: "0", CacheReadCost: "0", ImageOutputCost: "0"}
}

func addUsage(total *AttemptUsage, u AttemptUsage) {
	total.TokensInput += u.TokensInput
	total.TokensOutput += u.TokensOutput
	total.CacheCreationTokens += u.CacheCreationTokens
	total.CacheReadTokens += u.CacheReadTokens
	total.ImageOutputTokens += u.ImageOutputTokens
	total.DeliveredTokens += u.DeliveredTokens
	total.ActualCost = addDecimal(total.ActualCost, u.ActualCost)
	total.InputCost = addDecimal(total.InputCost, u.InputCost)
	total.OutputCost = addDecimal(total.OutputCost, u.OutputCost)
	total.CacheCreationCost = addDecimal(total.CacheCreationCost, u.CacheCreationCost)
	total.CacheReadCost = addDecimal(total.CacheReadCost, u.CacheReadCost)
	total.ImageOutputCost = addDecimal(total.ImageOutputCost, u.ImageOutputCost)
}

// timedEvent 在排序阶段保留原始时刻,避免用格式化后的字符串比较(小数位长度不同会颠倒顺序)。
type timedEvent struct {
	at    time.Time
	event Event
}

func countJSONArray(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}

func formatTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

func optionalTime(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format(time.RFC3339Nano)
	return &s
}
