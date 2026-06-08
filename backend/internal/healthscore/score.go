package healthscore

import "math"

const (
	errorPerfectRate = 0.01
	errorZeroRate    = 0.10
	ttftPerfectMs    = 1000
	ttftZeroMs       = 3000
)

// Business scores user-visible health from error rate and TTFT p99. A lower
// error rate and lower p99 latency produce a higher score.
func Business(errorRate, ttftP99Ms float64) int {
	errorScore := linearScore(errorRate, errorPerfectRate, errorZeroRate)
	ttftScore := linearScore(ttftP99Ms, ttftPerfectMs, ttftZeroMs)
	return clampScore(math.Round((errorScore + ttftScore) / 2))
}

// Overall combines business health and infrastructure health into a single
// 0-100 score. Business impact carries the larger share.
func Overall(businessScore, infraScore int) int {
	return clampScore(math.Round((float64(clampInt(businessScore))*70 + float64(clampInt(infraScore))*30) / 100))
}

func linearScore(value, perfect, zero float64) float64 {
	if value <= perfect {
		return 100
	}
	if value >= zero {
		return 0
	}
	return ((zero - value) / (zero - perfect)) * 100
}

func clampScore(value float64) int {
	if value <= 0 {
		return 0
	}
	if value >= 100 {
		return 100
	}
	return int(value)
}

func clampInt(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
