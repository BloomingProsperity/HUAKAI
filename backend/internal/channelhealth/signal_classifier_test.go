package channelhealth

import "testing"

func TestSignalClassifierSafeClasses(t *testing.T) {
	tests := []struct {
		name string
		in   ClassifierInput
		want SignalClass
	}{
		{name: "token revoked", in: ClassifierInput{StatusCode: 401, RawUpstreamText: "token revoked: hk_live_secret"}, want: SignalTokenRevoked},
		{name: "rate limit 429", in: ClassifierInput{StatusCode: 429}, want: SignalRateLimit},
		{name: "rate-ish 403", in: ClassifierInput{StatusCode: 403, RawUpstreamText: "quota limit reached"}, want: SignalRateLimit},
		{name: "local 5xx", in: ClassifierInput{StatusCode: 502, LocalGatewayError: true}, want: SignalLocalGateway5xx},
		{name: "upstream 5xx", in: ClassifierInput{StatusCode: 503}, want: SignalUpstream5xx},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.in)
			if got.Class != tt.want {
				t.Fatalf("Class=%s want %s", got.Class, tt.want)
			}
		})
	}
}

func TestKeywordAutoDisable(t *testing.T) {
	// 变异:忽略配置的 keyword,或扫描 RawUpstreamText 而非 safe/code 文本;quota_exceeded 将不再被判定为应禁用。
	cfg := ClassifierConfig{DisableKeywords: []string{"quota_exceeded"}}
	in := ClassifierInput{StatusCode: 402, SafeErrorClass: "quota_exceeded"}

	got := ClassifyWithConfig(in, cfg)
	if got.Class != SignalPolicyAutoDisabled {
		t.Fatalf("Class=%s want %s", got.Class, SignalPolicyAutoDisabled)
	}
	if !isBanSignal(got.Class) {
		t.Fatalf("Class=%s must be disable-worthy", got.Class)
	}

	unchangedInput := ClassifierInput{StatusCode: 402, SafeErrorClass: "billing_error"}
	withoutKeyword := Classify(unchangedInput)
	withKeyword := ClassifyWithConfig(unchangedInput, cfg)
	if withKeyword.Class != withoutKeyword.Class {
		t.Fatalf("non-matching keyword Class=%s want unchanged %s", withKeyword.Class, withoutKeyword.Class)
	}

	emptyConfig := ClassifyWithConfig(in, ClassifierConfig{})
	defaultClass := Classify(in)
	if emptyConfig.Class != defaultClass.Class {
		t.Fatalf("empty keyword config Class=%s want default %s", emptyConfig.Class, defaultClass.Class)
	}

	rawOnly := ClassifyWithConfig(ClassifierInput{
		StatusCode:      402,
		RawUpstreamText: "quota_exceeded",
	}, cfg)
	if rawOnly.Class == SignalPolicyAutoDisabled {
		t.Fatal("keyword auto-disable must not inspect raw upstream text")
	}
}

func TestStatusRangeAutoDisable(t *testing.T) {
	// 变异:忽略配置的 status range;status 503 仍是普通的 upstream_5xx 而非应禁用。
	cfg := ClassifierConfig{DisableStatusRanges: []string{"500-599"}}

	got := ClassifyWithConfig(ClassifierInput{StatusCode: 503}, cfg)
	if got.Class != SignalPolicyAutoDisabled {
		t.Fatalf("Class=%s want %s", got.Class, SignalPolicyAutoDisabled)
	}
	if !isBanSignal(got.Class) {
		t.Fatalf("Class=%s must be disable-worthy", got.Class)
	}

	notMatched := ClassifyWithConfig(ClassifierInput{StatusCode: 429}, cfg)
	if notMatched.Class != SignalRateLimit {
		t.Fatalf("429 Class=%s want existing %s", notMatched.Class, SignalRateLimit)
	}

	emptyConfig := ClassifyWithConfig(ClassifierInput{StatusCode: 503}, ClassifierConfig{})
	defaultClass := Classify(ClassifierInput{StatusCode: 503})
	if emptyConfig.Class != defaultClass.Class {
		t.Fatalf("empty range config Class=%s want default %s", emptyConfig.Class, defaultClass.Class)
	}
}
