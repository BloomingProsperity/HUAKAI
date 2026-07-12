package platformsettings

import (
	"encoding/json"
	"testing"
)

func TestQuotaProbeSettingsDefaultsAndBounds(t *testing.T) {
	if value, ok := DefaultValue(KeyQuotaProbeEnabled); !ok || value != "true" {
		t.Fatalf("quota probe 默认开关=%q ok=%v，期望 true", value, ok)
	}
	if value, ok := DefaultValue(KeyQuotaProbeIntervalMinutes); !ok || value != "30" {
		t.Fatalf("quota probe 默认周期=%q ok=%v，期望 30", value, ok)
	}
	for raw, want := range map[string]string{"1": "5", "5": "5", "1440": "1440", "9999": "1440"} {
		got, err := ValidateValue(KeyQuotaProbeIntervalMinutes, raw)
		if err != nil {
			t.Fatalf("ValidateValue(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("ValidateValue(%q)=%q，期望 %q", raw, got, want)
		}
	}
}

func TestMediaTaskDefaultEstimateIncludesMusic(t *testing.T) {
	raw, ok := DefaultValue(KeyMediaTaskDefaultEstimatedCents)
	if !ok {
		t.Fatal("媒体任务默认估价键未注册")
	}
	var estimates map[string]int64
	if err := json.Unmarshal([]byte(raw), &estimates); err != nil {
		t.Fatalf("解析默认估价: %v", err)
	}
	if estimates["music_generation"] != 300 {
		t.Fatalf("music_generation 默认估价=%d，期望 300", estimates["music_generation"])
	}
	if estimates["image_generation"] != 100 || estimates["video_generation"] != 1000 {
		t.Fatalf("既有图片/视频估价被改坏：%v", estimates)
	}
}
