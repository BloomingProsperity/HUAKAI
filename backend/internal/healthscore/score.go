package healthscore

import "math"

const (
	errorPerfectRate = 0.01
	errorZeroRate    = 0.10
	ttftPerfectMs    = 1000
	ttftZeroMs       = 3000
)

// Business 根据错误率和 TTFT p99 给用户可见的健康度打分。错误率越低、
// p99 延迟越低,得分越高。
func Business(errorRate, ttftP99Ms float64) int {
	errorScore := linearScore(errorRate, errorPerfectRate, errorZeroRate)
	ttftScore := linearScore(ttftP99Ms, ttftPerfectMs, ttftZeroMs)
	return clampScore(math.Round((errorScore + ttftScore) / 2))
}

// Overall 把业务健康度与基础设施健康度合成为一个 0-100 的分数。
// 业务影响占更大的权重。
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
