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

// Infra 据"自动托管的上游渠道里有多少处于健康可服务态"给基础设施健康打分(0-100)。
// 它与业务面(错误率/TTFT)物理不同源:反映上游账号被本系统自动冷却/降级/禁用的比例,即供应商侧可用性。
//
//	healthyChannels = 可服务渠道数(active + ramping)
//	managedChannels = 自动托管渠道数(健康 + degraded/cooling_down/disabled);不含 operator 手动暂停的
//	                  manual_paused —— 那是人为意图、非基础设施故障,既不算健康也不进分母。
//
// managedChannels<=0(无渠道数据 / 全是手动暂停 / 取数不可用)时返回 100:保守不误报,避免"没数据"
// 被当成"基础设施全挂"。否则按健康占比线性打分(沿用 clampScore 的 0-100 钳制风格)。
func Infra(healthyChannels, managedChannels int64) int {
	if managedChannels <= 0 {
		return 100
	}
	if healthyChannels < 0 {
		healthyChannels = 0
	}
	if healthyChannels > managedChannels {
		healthyChannels = managedChannels
	}
	return clampScore(math.Round(float64(healthyChannels) / float64(managedChannels) * 100))
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
