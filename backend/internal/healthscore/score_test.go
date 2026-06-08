package healthscore

import "testing"

func TestHealthScoreBusiness(t *testing.T) {
	if got := Business(0.01, 1000); got != 100 {
		t.Fatalf("Business(1%% error, 1000ms p99)=%d want 100", got)
	}
	if got := Business(0.10, 3000); got != 0 {
		t.Fatalf("Business(10%% error, 3000ms p99)=%d want 0", got)
	}

	lowError := Business(0.02, 1500)
	highError := Business(0.08, 1500)
	if highError >= lowError {
		t.Fatalf("score must fall as error rate rises: low_error=%d high_error=%d; mutation reversing errorScore makes this red", lowError, highError)
	}

	lowLatency := Business(0.02, 1200)
	highLatency := Business(0.02, 2500)
	if highLatency >= lowLatency {
		t.Fatalf("score must fall as p99 TTFT rises: low_latency=%d high_latency=%d", lowLatency, highLatency)
	}
}

func TestHealthScoreOverallWeightsBusinessAndInfra(t *testing.T) {
	if got := Overall(100, 0); got != 70 {
		t.Fatalf("Overall(100,0)=%d want 70 business-weighted score", got)
	}
	if got := Overall(0, 100); got != 30 {
		t.Fatalf("Overall(0,100)=%d want 30 infra-weighted score", got)
	}
}
