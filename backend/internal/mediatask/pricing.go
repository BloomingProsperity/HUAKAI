package mediatask

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

// MediaTaskCategory 是媒体任务的粗类目;估价种子默认按类目键配置。
const (
	MediaCategoryImage = "image_generation"
	MediaCategoryMusic = "music_generation"
	MediaCategoryVideo = "video_generation"
)

// categoryForTaskType 把 provider 专属 task_type(mj_imagine / suno_generate / video_generate 等)
// 归到粗类目。provider client 提交的是专属 task_type,而估价种子默认(KeyMediaTaskDefaultEstimatedCents)
// 只按 image/music/video 三类目配——不归类会全部缺键 fail-closed(mj/suno/video 提交全挂)。
// mj_video 是 midjourney 的视频动作,归 video 而非 image。
func categoryForTaskType(taskType string) string {
	switch {
	case taskType == "mj_video" || strings.HasPrefix(taskType, "video_"):
		return MediaCategoryVideo
	case strings.HasPrefix(taskType, "suno_"):
		return MediaCategoryMusic
	case strings.HasPrefix(taskType, "mj_") || strings.HasPrefix(taskType, "image_"):
		return MediaCategoryImage
	default:
		return ""
	}
}

func EstimateCents(ctx context.Context, cfg Config, taskType string) (int64, error) {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		return 0, fmt.Errorf("%w: task_type", ErrInvalidInput)
	}
	// 先试精确 task_type 键(允许运维对具体动作细配),再回落到粗类目键(种子默认按类目配)。
	cents, ok := cfg.DefaultEstimatedCents[taskType]
	if !ok {
		category := categoryForTaskType(taskType)
		if category != "" {
			cents, ok = cfg.DefaultEstimatedCents[category]
		}
		if !ok {
			return 0, fmt.Errorf("%w: missing default estimate for %s (category %q)", ErrInvalidInput, taskType, category)
		}
	}
	if cents < 0 {
		return 0, fmt.Errorf("%w: negative default estimate for %s", ErrInvalidInput, taskType)
	}
	result, err := pricingeval.Resolve(ctx, nil, pricingeval.Usage{
		BillableUnits: decimal.NewFromInt(1),
	}, pricingeval.FlatRateFallback{
		PerUnit:    decimal.NewFromInt(cents).Mul(decimal.NewFromInt(10_000)),
		Multiplier: decimal.NewFromInt(1),
		HasPerUnit: true,
	}, cfg.BillingPolicyVersion)
	if err != nil {
		return 0, err
	}
	return usdToCents(result.Total), nil
}

func centsToUSD(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}

func usdToCents(cost decimal.Decimal) int64 {
	if cost.IsNegative() {
		return 0
	}
	return cost.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}
