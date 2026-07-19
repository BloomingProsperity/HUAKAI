package mediatask

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
)

func TestEstimateCentsUsesPricingEvalPerUnitScaling(t *testing.T) {
	// 变异:把 cents 直接当作 micro-USD,而不是把 cents 转换成 micro-USD;
	// 这会让 123 分的样例返回 0 cents,必须变红。
	cfg := Config{DefaultEstimatedCents: map[string]int64{"image_generation": 123}}

	got, err := EstimateCents(context.Background(), cfg, "image_generation")
	if err != nil {
		t.Fatalf("EstimateCents: %v", err)
	}
	if got != 123 {
		t.Fatalf("estimated cents=%d want 123", got)
	}
}

func TestEstimateCentsRejectsMissingTaskType(t *testing.T) {
	cfg := Config{DefaultEstimatedCents: map[string]int64{"image_generation": 100}}

	if _, err := EstimateCents(context.Background(), cfg, "video_generation"); err == nil {
		t.Fatal("missing default estimate returned nil error")
	}
}

// TestEstimateCentsMapsProviderTaskTypesToCategory 守卫「provider 专属 task_type 归类目估价」:
// mj/suno/video client 提交 mj_imagine/suno_generate/video_generate 等专属 task_type,而种子
// 默认只按 image/music/video 三类目配。变异:删 categoryForTaskType 回落后,这些专属 task_type
// 全部缺键报错(mj/suno/video 提交全 fail-closed=功能挂)——本测试转红。
func TestEstimateCentsMapsProviderTaskTypesToCategory(t *testing.T) {
	cfg := Config{DefaultEstimatedCents: map[string]int64{
		"image_generation": 100, "music_generation": 300, "video_generation": 1000,
	}}
	cases := map[string]int64{
		"mj_imagine":     100,
		"mj_describe":    100,
		"mj_video":       1000, // midjourney 视频动作 → video 类目(非 image)
		"suno_generate":  300,
		"suno_custom":    300,
		"video_generate": 1000,
	}
	for taskType, want := range cases {
		got, err := EstimateCents(context.Background(), cfg, taskType)
		if err != nil {
			t.Fatalf("EstimateCents(%q): %v(provider task_type 未归类目 → 提交挂)", taskType, err)
		}
		if got != want {
			t.Fatalf("EstimateCents(%q)=%d want %d(类目映射错)", taskType, got, want)
		}
	}
}

// TestEstimateCentsExactTaskTypeKeyOverridesCategory 守卫「精确 task_type 键优先于粗类目键」
// (允许运维对具体动作细配)。变异:删精确键优先分支(直接查类目)→ 返回类目价 100 而非专属价
// 500,转红。
func TestEstimateCentsExactTaskTypeKeyOverridesCategory(t *testing.T) {
	cfg := Config{DefaultEstimatedCents: map[string]int64{
		"image_generation": 100, "mj_imagine": 500,
	}}
	got, err := EstimateCents(context.Background(), cfg, "mj_imagine")
	if err != nil {
		t.Fatalf("EstimateCents: %v", err)
	}
	if got != 500 {
		t.Fatalf("estimated cents=%d want 500(精确键应优先于类目)", got)
	}
}

func TestCentsRoundTripUSD(t *testing.T) {
	got := centsToUSD(123)
	if !got.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("centsToUSD(123)=%s want 1.23", got)
	}
	if usdToCents(decimal.RequireFromString("1.239")) != 124 {
		t.Fatal("usdToCents must round half up to the nearest cent")
	}
}
