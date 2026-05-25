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
