package tokencheck

import "math"

const ratioEpsilon = 1e-12

// CrossCheck 使用默认阈值比对上游报告 token 与本地估算 token。
func CrossCheck(reported, estimated int) Discrepancy {
	return CrossCheckWithThresholds(reported, estimated, DefaultThresholds)
}

// CrossCheckWithThresholds 使用调用方提供的阈值进行交叉校验。
func CrossCheckWithThresholds(reported, estimated int, thresholds Thresholds) Discrepancy {
	if reported <= 0 || estimated <= 0 {
		return Discrepancy{
			Reported:  reported,
			Estimated: estimated,
			Verdict:   VerdictUnknown,
		}
	}

	thresholds = normalizeThresholds(thresholds)
	ratio := float64(reported) / float64(estimated)
	drift := math.Abs(ratio - 1)
	verdict := VerdictOK
	if drift+ratioEpsilon >= thresholds.FailRatio {
		verdict = VerdictFail20
	} else if drift+ratioEpsilon >= thresholds.WarnRatio {
		verdict = VerdictWarn5
	}

	return Discrepancy{
		Reported:  reported,
		Estimated: estimated,
		Ratio:     ratio,
		Verdict:   verdict,
	}
}

func normalizeThresholds(thresholds Thresholds) Thresholds {
	if thresholds.WarnRatio <= 0 {
		thresholds.WarnRatio = DefaultThresholds.WarnRatio
	}
	if thresholds.FailRatio <= 0 {
		thresholds.FailRatio = DefaultThresholds.FailRatio
	}
	if thresholds.FailRatio < thresholds.WarnRatio {
		thresholds.FailRatio = thresholds.WarnRatio
	}
	return thresholds
}
