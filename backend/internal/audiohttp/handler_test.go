package audiohttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintenttest"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestAudioSpeech_ChargesUTF8RunesAndSettlesExactlyOnce(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{
		status:  http.StatusOK,
		body:    "audio-bytes",
		headers: http.Header{"Content-Type": []string{"audio/mpeg"}},
	})

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"héllo","voice":"alloy","response_format":"mp3"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := env.selector.last.CapabilityFlags; len(got) != 0 {
		t.Fatalf("选号能力=%v,audio speech 不得携带账号级媒体能力门(modality 由模型注册表判)", got)
	}
	env.assertNoHangingClaims(t)
	if got := env.transport.path; got != "/v1/audio/speech" {
		t.Fatalf("upstream path=%q want /v1/audio/speech", got)
	}
	if got := rec.Body.String(); got != "audio-bytes" {
		t.Fatalf("body=%q want exact upstream audio bytes", got)
	}
	want := decimal.RequireFromString("0.005")
	assertAudioDecimal(t, "reserve PredictedCost", env.claims.reserves[0].req.PredictedCost, want)
	assertAudioDecimal(t, "settle ActualCost", env.settler.settles[0].ActualCost, want)
	byteCountMutation := decimal.RequireFromString("0.006")
	if env.claims.reserves[0].req.PredictedCost.Equal(byteCountMutation) {
		t.Fatal("fixture is non-discriminating: byte-count pricing matched reserve")
	}
}

func TestAudioSpeech_DisabledTenantStopsBeforeUpstream(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	env.claims.err = billing.ErrTenantInactive

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"must not dispatch","voice":"alloy"}`)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"tenant_inactive"`) {
		t.Fatalf("status=%d body=%s want 403 tenant_inactive", rec.Code, rec.Body.String())
	}
	if env.transport.called || len(env.settler.settles) != 0 || len(env.settler.aborts) != 0 {
		t.Fatalf("停用租户仍触发 upstream/settle/abort=%v/%d/%d",
			env.transport.called, len(env.settler.settles), len(env.settler.aborts))
	}
}

// TestAudioSpeech_SettlementIntentFailureStopsBeforeUpstream 守住资金恢复
// 证据写失败时的交付前硬门。变异：删掉 InsertPending 或吞掉其错误，会让
// transport 被调用并把音频交付给客户端。
func TestAudioSpeech_SettlementIntentFailureStopsBeforeUpstream(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{
		status:  http.StatusOK,
		body:    "must-not-be-delivered",
		headers: http.Header{"Content-Type": []string{"audio/mpeg"}},
	})
	env.deps.SettlementIntents = settlementintent.NewPostgresStore(nil)
	env.deps.SettlementIntentEnabled = true

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"must not dispatch","voice":"alloy"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if env.transport.called {
		t.Fatal("恢复证据写失败后不得调用上游")
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1", got)
	}
	env.assertNoHangingClaims(t)
}

func TestAudioSpeech_SettlementIntentSuccessfulLifecycle(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{
		status:  http.StatusOK,
		body:    "audio-ok",
		headers: http.Header{"Content-Type": []string{"audio/mpeg"}},
	})
	store := &settlementintenttest.Store{}
	env.deps.SettlementIntents = store
	env.deps.SettlementIntentEnabled = true

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"lifecycle","voice":"alloy"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(store.Events(), "->"); got != "pending->delivering->settled" {
		t.Fatalf("intent lifecycle=%q want pending->delivering->settled", got)
	}
}

func TestAudioSpeech_DeliveryEvidenceFailureStopsClientDeliveryAndSettlement(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{
		status:  http.StatusOK,
		body:    "must-not-deliver-audio",
		headers: http.Header{"Content-Type": []string{"audio/mpeg"}},
	})
	store := &settlementintenttest.Store{DeliveryError: errors.New("注入交付证据故障")}
	env.deps.SettlementIntents = store
	env.deps.SettlementIntentEnabled = true
	health := &audioHealthSpy{}
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"delivery gate","voice":"alloy"}`)

	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "must-not-deliver-audio") {
		t.Fatalf("status/body=%d/%s want 503 且无上游业务体", rec.Code, rec.Body.String())
	}
	if len(env.settler.settles) != 0 || len(env.settler.aborts) != 1 {
		t.Fatalf("settle/abort=%d/%d want 0/1", len(env.settler.settles), len(env.settler.aborts))
	}
	if got := strings.Join(store.Events(), "->"); got != "pending->aborted" {
		t.Fatalf("intent lifecycle=%q want pending->aborted", got)
	}
	for _, signal := range health.signals {
		if signal.Class != channelhealth.SignalSuccess {
			t.Fatalf("本地交付证据故障写入失败健康信号: %+v", health.signals)
		}
	}
	if health.forceCooldowns != 0 {
		t.Fatalf("本地交付证据故障污染账号健康: signals=%+v force_cooldowns=%d",
			health.signals, health.forceCooldowns)
	}
}

func TestAudioSpeech_RejectsInputOver4096RunesBeforeReserve(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	body := `{"model":"tts-1","input":"` + strings.Repeat("a", 4097) + `","voice":"alloy"}`

	rec := env.invokeJSON(t, body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0 for pre-reserve validation failure", got)
	}
	if env.transport.called {
		t.Fatal("upstream called for over-limit TTS input")
	}
}

func TestAudioSpeech_MissingAudioRateReturns503BeforeReserve(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	env.rateTable.raw = json.RawMessage(`{"models":{"text-only":{"input_micro_usd":"1000"}}}`)

	rec := env.invokeJSON(t, `{"model":"text-only","input":"priced?","voice":"alloy"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0 when audio rate keys are absent", got)
	}
	if env.transport.called {
		t.Fatal("upstream called when pricing was unavailable")
	}
}

func TestAudioTranscriptions_WavDurationDrivesReserveAndSettle(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusOK,
		body:   `{"text":"done"}`,
	})
	store := &settlementintenttest.Store{}
	env.deps.SettlementIntents = store
	env.deps.SettlementIntentEnabled = true
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 40000), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := env.selector.last.CapabilityFlags; len(got) != 0 {
		t.Fatalf("选号能力=%v,audio 转写不得携带账号级媒体能力门(modality 由模型注册表判)", got)
	}
	env.assertNoHangingClaims(t)
	want := decimal.RequireFromString("0.00025")
	assertAudioDecimal(t, "reserve PredictedCost", env.claims.reserves[0].req.PredictedCost, want)
	assertAudioDecimal(t, "settle ActualCost", env.settler.settles[0].ActualCost, want)
	if env.settler.settles[0].Draft.TokensInput != 0 || env.settler.settles[0].Draft.TokensOutput != 0 {
		t.Fatalf("wav second billing unexpectedly used token counts: %+v", env.settler.settles[0].Draft)
	}
	if got := strings.Join(store.Events(), "->"); got != "pending->delivering->settled" {
		t.Fatalf("intent lifecycle=%q want pending->delivering->settled", got)
	}
}

func TestAudioTranscriptions_VerboseJSONProviderDurationOverridesLocalDuration(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusOK,
		body:   `{"text":"done","duration":3.75}`,
	})
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 40000), map[string]string{
		"model":           "whisper-1",
		"response_format": "verbose_json",
	})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	assertAudioDecimal(t, "reserve local duration", env.claims.reserves[0].req.PredictedCost, "0.00025")
	assertAudioDecimal(t, "settle provider duration", env.settler.settles[0].ActualCost, "0.000375")
	if env.claims.reserves[0].req.PredictedCost.Equal(env.settler.settles[0].ActualCost) {
		t.Fatal("fixture is non-discriminating: provider duration did not change settle cost")
	}
}

func TestAudioTranscriptions_TokenUsageSettlesTokenBranch(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusOK,
		body:   `{"text":"done","usage":{"input_tokens":7,"output_tokens":11}}`,
	})
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "gpt-4o-transcribe"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	assertAudioDecimal(t, "token actual cost", env.settler.settles[0].ActualCost, "0.00029")
	if env.claims.reserves[0].req.PredictedCost.Equal(env.settler.settles[0].ActualCost) {
		t.Fatal("token fixture is non-discriminating: reserve estimate matched reported token settle")
	}
	settle := env.settler.settles[0]
	if settle.Draft.TokensInput != 7 || settle.Draft.TokensOutput != 11 {
		t.Fatalf("settled tokens input/output=%d/%d want 7/11", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
}

func TestAudioTranscriptions_CompressedUsesSizeBoundAndPendingReconciliation(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusOK,
		body:   `{"text":"compressed"}`,
	})
	mp3 := append([]byte("ID3"), bytes.Repeat([]byte{0x11}, 15997)...)
	body, contentType := multipartAudioBody(t, "file", "clip.mp3", "audio/mpeg", mp3, map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	want := decimal.RequireFromString("0.0016")
	assertAudioDecimal(t, "reserve size-bound", env.claims.reserves[0].req.PredictedCost, want)
	assertAudioDecimal(t, "settle size-bound", env.settler.settles[0].ActualCost, want)
	if !env.settler.settles[0].Draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true when only compressed size-bound is available")
	}
}

func TestAudioTranscriptions_Upstream5xxAbortsReservedClaim(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"upstream down"}}`,
	})
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code == http.StatusOK {
		t.Fatalf("status=%d body=%s want normalized upstream failure", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on upstream failure", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on upstream failure", got)
	}
}

func TestAudioTranscriptions_ForwardsOriginalMultipartBoundaryBodyAndPath(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: http.StatusOK, body: `{"text":"ok"}`})
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(8000, 8000), map[string]string{
		"model":           "whisper-1",
		"language":        "en",
		"prompt":          "domain words",
		"response_format": "json",
		"temperature":     "0.2",
	})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := env.transport.contentType; got != contentType {
		t.Fatalf("upstream Content-Type=%q want original %q", got, contentType)
	}
	if got := env.transport.path; got != "/v1/audio/transcriptions" {
		t.Fatalf("upstream path=%q want /v1/audio/transcriptions", got)
	}
	if !bytes.Equal([]byte(env.transport.body), body) {
		t.Fatal("upstream multipart body changed; boundary or fields were re-encoded")
	}
	if env.claims.reserves[0].req.NormalizedPayloadHash != sha256Hex(body) {
		t.Fatalf("fingerprint=%q want raw multipart body sha256", env.claims.reserves[0].req.NormalizedPayloadHash)
	}
}

func TestAudioTranscriptions_IdempotencyReplayReturns409WithoutSettleOrDispatch(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: http.StatusOK, body: `{"text":"ok"}`})
	env.claims.idempotencyHit = true
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipartWithKey(t, body, contentType, "idem-audio-1")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1 replay lookup", got)
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on replay without cache", got)
	}
	if env.transport.called {
		t.Fatal("upstream called on idempotency replay")
	}
}

func TestAudioTranscriptions_MissingFileReturns400BeforeReserve(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: http.StatusOK, body: `{"text":"ok"}`})
	body, contentType := multipartAudioBody(t, "", "", "", nil, map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0 without file", got)
	}
}

func TestAudioTranscriptions_BodyOverLimitReturns413BeforeReserve(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: http.StatusOK, body: `{"text":"ok"}`})
	body, contentType := multipartAudioBody(t, "file", "huge.mp3", "audio/mpeg", bytes.Repeat([]byte{0x22}, maxMultipartBodyBytes), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, append(body, 'x'), contentType)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s want 413", rec.Code, rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0 for over-limit body", got)
	}
}

func TestAudioSpeech_GroupRatioDiscountsReserveAndSettle(t *testing.T) {
	base := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	baseRec := base.invokeJSON(t, `{"model":"tts-1","input":"héllo","voice":"alloy"}`)
	if baseRec.Code != http.StatusOK {
		t.Fatalf("baseline status=%d body=%s want 200", baseRec.Code, baseRec.Body.String())
	}
	discounted := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	discounted.deps.PricingRatioResolver = &pricingRatioResolverStub{ratio: decimal.RequireFromString("0.8")}
	discountedRec := discounted.invokeJSON(t, `{"model":"tts-1","input":"héllo","voice":"alloy"}`)
	if discountedRec.Code != http.StatusOK {
		t.Fatalf("discounted status=%d body=%s want 200", discountedRec.Code, discountedRec.Body.String())
	}

	ratio := decimal.RequireFromString("0.8")
	assertAudioDecimal(t, "discounted reserve", discounted.claims.reserves[0].req.PredictedCost, base.claims.reserves[0].req.PredictedCost.Mul(ratio))
	assertAudioDecimal(t, "discounted settle", discounted.settler.settles[0].ActualCost, base.settler.settles[0].ActualCost.Mul(ratio))
	if !strings.Contains(discounted.settler.settles[0].Draft.CostSnapshot, "group_ratio=0.8") {
		t.Fatalf("CostSnapshot=%q want group_ratio=0.8", discounted.settler.settles[0].Draft.CostSnapshot)
	}
}

// 交付后结算失败:响应头早已发出(settle-after-delivery),所以是 200 已交付 + 不 abort,
// 并把完整 settle intent 放进持久恢复队列。Mutation:删恢复接线后 recovery.calls=0。
func TestAudioSpeech_SettleErrorAfterDeliveryKeeps200AndEnqueuesRecovery(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	env.settler.settleErr = errors.New("settle backend down")
	recovery := &audioRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"héllo","voice":"alloy"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 (response delivered before settle)", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1 (settle attempted after delivery)", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 (cannot abort an already-delivered response)", got)
	}
	if recovery.calls != 1 || recovery.event.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("recovery calls/kind=%d/%q want 1/%q",
			recovery.calls, recovery.event.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	payload, err := settlementrecovery.Decode(recovery.event.Payload)
	if err != nil {
		t.Fatalf("decode recovery payload: %v", err)
	}
	if payload.Source != settlementrecovery.SourceAudioDelivered {
		t.Fatalf("recovery source=%q want %q", payload.Source, settlementrecovery.SourceAudioDelivered)
	}
	replayed := payload.ToSettleRequest()
	original := env.settler.settles[0]
	if replayed.ClaimID != original.ClaimID ||
		replayed.ProviderAccountID != original.ProviderAccountID ||
		replayed.RequestedModel != original.RequestedModel ||
		!replayed.ActualCost.Equal(original.ActualCost) {
		t.Fatalf("recovery settle intent drifted: got=%+v want claim/account/model/cost=%d/%d/%q/%s",
			replayed, original.ClaimID, original.ProviderAccountID, original.RequestedModel, original.ActualCost)
	}
}

func TestAudioTranscription_SettleErrorAfterDeliveryKeeps200AndEnqueuesRecovery(t *testing.T) {
	const upstreamBody = `{"text":"delivered transcription"}`
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: http.StatusOK, body: upstreamBody})
	env.settler.settleErr = errors.New("settle backend down")
	recovery := &audioRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK || rec.Body.String() != upstreamBody {
		t.Fatalf("status/body=%d/%s want delivered 200 body", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 after full delivery", got)
	}
	if recovery.calls != 1 || recovery.event.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("recovery calls/kind=%d/%q want 1/%q",
			recovery.calls, recovery.event.EventKind, dlq.EventKindPostDeliverySettlement)
	}
}

func TestAudioSpeech_SettleAndRecoveryDoubleFailureEmitsP0WithoutSecret(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	const secret = "AUDIO_DOUBLE_FAULT_SECRET"
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	env.settler.settleErr = errors.New("settle failed " + secret)
	recovery := &audioRecoveryEnqueuer{err: errors.New("recovery enqueue failed " + secret)}
	env.deps.SettleRecoveryDLQ = recovery
	intentStore := &settlementintenttest.Store{}
	env.deps.SettlementIntents = intentStore
	env.deps.SettlementIntentEnabled = true

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"double fault","voice":"alloy"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200，响应已交付不能反悔", rec.Code, rec.Body.String())
	}
	if recovery.calls != 1 {
		t.Fatalf("recovery calls=%d want 1", recovery.calls)
	}
	if got := strings.Join(intentStore.Events(), "->"); got != "pending->delivering->recovery_pending" {
		t.Fatalf("双故障意图生命周期=%q", got)
	}
	raw, failureClass := intentStore.RecoveryEvidence()
	persisted, err := settlementrecovery.Decode(raw)
	if err != nil || persisted.Source != settlementrecovery.SourceAudioDelivered || failureClass == "" {
		t.Fatalf("双故障恢复证据 source=%q class=%q err=%v", persisted.Source, failureClass, err)
	}
	got := logs.String()
	for _, want := range []string{"money_lost_double_fault", "critical", "P0", "audiohttp.settle_recovery"} {
		if !strings.Contains(got, want) {
			t.Fatalf("P0 log missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, secret) {
		t.Fatalf("P0 log leaked raw failure detail: %s", got)
	}
}

// failingWriter 在 WriteHeader 之后的 Write 一律失败,模拟客户端断连。
type failingWriter struct {
	hdr  http.Header
	code int
}

func (f *failingWriter) Header() http.Header {
	if f.hdr == nil {
		f.hdr = http.Header{}
	}
	return f.hdr
}
func (f *failingWriter) WriteHeader(c int) {
	f.code = c
}
func (f *failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("client gone")
}

// F1 判别测试:交付失败 → abort 不扣费。Mutation: settle 改回交付前 → settle 会被调用
// (settles=1),本断言变红。
func TestAudioSpeech_DeliveryFailureAbortsWithoutSettle(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audiodata"})
	req := httptest.NewRequest(http.MethodPost, env.endpoint.Path(), bytes.NewBufferString(`{"model":"tts-1","input":"hi","voice":"alloy"}`))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	w := &failingWriter{}
	middleware.RequestID(env.handler()).ServeHTTP(w, req)

	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 — must not charge undelivered audio", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on delivery failure", got)
	}
}

// F2 判别测试:billingCtx 脱离请求取消。Mutation: 改回返回 ex.ctx → bctx.Err()==Canceled,变红。
func TestBillingCtxDetachesFromRequestCancellation(t *testing.T) {
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel() // 客户端断连
	ex := &execution{ctx: reqCtx}
	bctx, bcancel := ex.billingCtx()
	defer bcancel()
	if bctx.Err() != nil {
		t.Fatalf("billingCtx must detach from request cancellation, got err=%v", bctx.Err())
	}
}

func TestAudioTranslations_RoutesToTranslationsPath(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranslations, upstreamResponse{status: http.StatusOK, body: `{"text":"english"}`})
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "whisper-1"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := env.transport.path; got != "/v1/audio/translations" {
		t.Fatalf("upstream path=%q want /v1/audio/translations", got)
	}
	env.assertNoHangingClaims(t)
}

type audioTestEnv struct {
	selector  *selectorStub
	deps      Deps
	claims    *recordingClaimGate
	settler   *recordingSettler
	transport *recordingRoundTripper
	rateTable *rateTableStub
	endpoint  audioEndpoint
}

type upstreamResponse struct {
	status  int
	body    string
	headers http.Header
}

func newAudioTestEnv(t *testing.T, endpoint audioEndpoint, resp upstreamResponse) *audioTestEnv {
	t.Helper()
	claims := &recordingClaimGate{nextClaimID: 9201}
	settler := &recordingSettler{}
	rt := &recordingRoundTripper{resp: resp}
	rates := &rateTableStub{raw: audioRateTableFixture()}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	sel := &selectorStub{}
	deps := Deps{
		Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
		Registry:              registryStub{},
		Router:                routerStub{},
		ClaimGate:             claims,
		RateTables:            rates,
		Selector:              sel,
		CredentialVault:       vaultStub{},
		Dispatcher:            &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf},
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	return &audioTestEnv{selector: sel, deps: deps, claims: claims, settler: settler, transport: rt, rateTable: rates, endpoint: endpoint}
}

func (e *audioTestEnv) invokeJSON(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, e.endpoint.Path(), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequestID(e.handler()).ServeHTTP(rec, req)
	return rec
}

func (e *audioTestEnv) invokeMultipart(t *testing.T, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	return e.invokeMultipartWithKey(t, body, contentType, "")
}

func (e *audioTestEnv) invokeMultipartWithKey(t *testing.T, body []byte, contentType, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, e.endpoint.Path(), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", contentType)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	middleware.RequestID(e.handler()).ServeHTTP(rec, req)
	return rec
}

func (e *audioTestEnv) handler() http.HandlerFunc {
	switch e.endpoint {
	case audioEndpointTranscriptions:
		return NewTranscriptionHandler(e.deps)
	case audioEndpointTranslations:
		return NewTranslationHandler(e.deps)
	default:
		return NewSpeechHandler(e.deps)
	}
}

func (e *audioTestEnv) assertNoHangingClaims(t *testing.T) {
	t.Helper()
	closed := map[int64]string{}
	for _, req := range e.settler.settles {
		closed[req.ClaimID] = "settled"
	}
	for _, req := range e.settler.aborts {
		if prior := closed[req.claimID]; prior != "" {
			t.Fatalf("claim %d closed twice: %s and aborted", req.claimID, prior)
		}
		closed[req.claimID] = "aborted"
	}
	for _, req := range e.claims.reserves {
		if got := closed[req.claimID]; got == "" {
			t.Fatalf("reserved claim %d was not settled or aborted", req.claimID)
		}
	}
}

type authStub struct {
	ident auth.Identity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.ident, s.err
}

type registryStub struct{}

func (registryStub) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "audio/" + model,
		ProviderModelID:  model,
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"audio", "audio_speech", "audio_transcription"},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type routerStub struct{}

func (routerStub) Plan(_ context.Context, in router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index:           0,
			PoolGroupID:     101,
			Reason:          "primary",
			UpstreamModelID: in.Model.ProviderModelID,
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:test",
	}, nil
}

type recordingClaimGate struct {
	nextClaimID    int64
	reserves       []reservedClaim
	idempotencyHit bool
	err            error
}

type reservedClaim struct {
	claimID int64
	req     billing.ReserveRequest
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.reserves = append(g.reserves, reservedClaim{claimID: g.nextClaimID, req: req})
	if g.err != nil {
		return nil, g.err
	}
	return &billing.ReserveResult{ClaimID: g.nextClaimID, IdempotencyHit: g.idempotencyHit}, nil
}

type rateTableStub struct {
	raw json.RawMessage
}

func (s *rateTableStub) GetRateTable(context.Context, string) (billing.RateTable, error) {
	return billing.RateTable{Version: "test-policy", PricingData: s.raw}, nil
}

func (s *rateTableStub) GetRateTableSnapshot(context.Context, int64) (billing.RateTable, error) {
	return billing.RateTable{}, billing.ErrRateTableNotFound
}

func (s *rateTableStub) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
	return nil, nil
}

type selectorStub struct{ last pool.SelectionRequest }

func (s *selectorStub) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.last = req
	return &pool.SelectionResult{
		AccountID:         44,
		AcquisitionToken:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RoutingReasonJSON: []byte(`{"reason":"test"}`),
	}, nil
}

type vaultStub struct{}

func (vaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		AccountID:   44,
		TenantID:    7,
		Platform:    "openai",
		AccountType: "api_key",
	}, nil
}

type recordingSettler struct {
	settles   []billing.SettleRequest
	aborts    []abortCall
	settleErr error
}

type audioRecoveryEnqueuer struct {
	calls int
	event dlq.Event
	err   error
}

func (e *audioRecoveryEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	e.calls++
	e.event = event
	if e.err != nil {
		return 0, e.err
	}
	return 1, nil
}

type abortCall struct {
	tenantID int64
	claimID  int64
	reason   string
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settles = append(s.settles, req)
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

func (s *recordingSettler) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	s.aborts = append(s.aborts, abortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	return nil
}

func (s *recordingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *recordingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

type pricingRatioResolverStub struct {
	ratio decimal.Decimal
}

func (s *pricingRatioResolverStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s == nil || s.ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return s.ratio, nil
}

type recordingRoundTripper struct {
	mu          sync.Mutex
	resp        upstreamResponse
	called      bool
	path        string
	contentType string
	body        string
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	raw, _ := io.ReadAll(req.Body)
	rt.called = true
	rt.path = req.URL.Path
	rt.contentType = req.Header.Get("Content-Type")
	rt.body = string(raw)
	status := rt.resp.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := rt.resp.headers.Clone()
	if headers == nil {
		headers = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(rt.resp.body)),
		Request:    req,
	}, nil
}

func multipartAudioBody(t *testing.T, fieldName, fileName, contentType string, fileBytes []byte, fields map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	if fieldName != "" {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+fieldName+`"; filename="`+fileName+`"`)
		header.Set("Content-Type", contentType)
		part, err := mw.CreatePart(header)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(fileBytes); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return buf.Bytes(), mw.FormDataContentType()
}

func wavPCM16Fixture(sampleRate, samples int) []byte {
	dataBytes := samples * 2
	totalSize := 36 + dataBytes
	out := make([]byte, 44+dataBytes)
	copy(out[0:4], "RIFF")
	putLE32(out[4:8], uint32(totalSize))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	putLE32(out[16:20], 16)
	putLE16(out[20:22], 1)
	putLE16(out[22:24], 1)
	putLE32(out[24:28], uint32(sampleRate))
	putLE32(out[28:32], uint32(sampleRate*2))
	putLE16(out[32:34], 2)
	putLE16(out[34:36], 16)
	copy(out[36:40], "data")
	putLE32(out[40:44], uint32(dataBytes))
	return out
}

func putLE16(dst []byte, v uint16) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
}

func putLE32(dst []byte, v uint32) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v >> 16)
	dst[3] = byte(v >> 24)
}

func audioRateTableFixture() json.RawMessage {
	return json.RawMessage(`{
		"providers": {
			"openai": {
				"models": {
					"tts-1": {
						"pricing_scheme": "per_char",
						"input_char_micro_usd": "1000"
					},
					"whisper-1": {
						"pricing_scheme": "per_second",
						"input_second_micro_usd": "100"
					},
					"gpt-4o-transcribe": {
						"pricing_scheme": "token",
						"input_micro_usd": "10",
						"output_micro_usd": "20"
					}
				}
			}
		}
	}`)
}

func assertAudioDecimal(t *testing.T, field string, got decimal.Decimal, want any) {
	t.Helper()
	var wantDecimal decimal.Decimal
	switch v := want.(type) {
	case string:
		wantDecimal = decimal.RequireFromString(v)
	case decimal.Decimal:
		wantDecimal = v
	default:
		t.Fatalf("unsupported decimal expectation %T", want)
	}
	if !got.Equal(wantDecimal) {
		t.Fatalf("%s=%s want %s", field, got, wantDecimal)
	}
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
