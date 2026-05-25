package tokencheck

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestCacheVerifyHitConsistent(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 100, CacheReadInputTokens: 40}
	got := CacheVerify{}.Verify(usage, hopsWithDetail(`{"cache_hit_ratio":0.4,"cache_read_tokens":40}`))
	if !got.EvidenceFound || !got.OK() {
		t.Fatalf("consistent hit should pass: %+v", got)
	}
}

func TestCacheVerifyMissConsistent(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 100, CacheReadInputTokens: 0}
	got := CacheVerify{}.Verify(usage, hopsWithDetail(`{"cache_hit_ratio":0,"cache_read_tokens":0}`))
	if !got.EvidenceFound || !got.OK() {
		t.Fatalf("consistent miss should pass: %+v", got)
	}
}

func TestCacheVerifyRatioMismatchEmitsWarning(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 100, CacheReadInputTokens: 20}
	got := CacheVerify{}.Verify(usage, hopsWithDetail(`{"cache_hit_ratio":0.8,"cache_read_tokens":20}`))
	requireOneWarning(t, got, "accounting.usage.cache_hit_ratio")
}

func TestCacheVerifyReadTokenMismatchEmitsWarning(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 100, CacheReadInputTokens: 20}
	got := CacheVerify{}.Verify(usage, hopsWithDetail(`{"cache_hit_ratio":0.2,"cache_read_tokens":25}`))
	requireOneWarning(t, got, "accounting.usage.cache_read_input_tokens")
}

func TestCacheVerifyPercentRatioAccepted(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 200, CacheReadInputTokens: 50}
	got := CacheVerify{}.Verify(usage, hopsWithDetail(`{"cache_hit_ratio":25,"cache_read_tokens":50}`))
	if !got.OK() || got.ReportedHitRatio != 0.25 {
		t.Fatalf("percent ratio should normalize and pass: %+v", got)
	}
}

func TestCacheVerifyNoEvidenceNoWarning(t *testing.T) {
	usage := proto.CanonicalUsage{InputTokens: 100, CacheReadInputTokens: 10}
	got := CacheVerify{}.Verify(usage, nil)
	if got.EvidenceFound || !got.OK() {
		t.Fatalf("missing evidence should be non-blocking: %+v", got)
	}
}

func hopsWithDetail(detail string) []proto.HopAttestation {
	return []proto.HopAttestation{{
		Hop:    proto.HopProvider,
		Detail: json.RawMessage(detail),
	}}
}

func requireOneWarning(t *testing.T, got CacheVerifyResult, field string) {
	t.Helper()
	if len(got.ProtocolLoss) != 1 {
		t.Fatalf("protocol loss count %d, want 1: %+v", len(got.ProtocolLoss), got)
	}
	loss := got.ProtocolLoss[0]
	if loss.Field != field {
		t.Fatalf("loss field %q, want %q", loss.Field, field)
	}
	if loss.Severity != proto.ProtocolLossWarning || loss.Code != cacheVerifyCode {
		t.Fatalf("loss is not cache warning: %+v", loss)
	}
	if loss.IsSilentDrop() {
		t.Fatalf("loss is silent drop: %+v", loss)
	}
}
