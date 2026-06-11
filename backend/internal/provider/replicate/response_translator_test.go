package replicate

import (
	"encoding/json"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Unix(1750000000, 0).UTC()
}

// TestTranslateImageResponseSingleURLString 抓的回归:output 单 string 形态
// 解析失败,或 created 不走注入的 now(时间戳不可测=形态漂移没人发现)。
func TestTranslateImageResponseSingleURLString(t *testing.T) {
	out, err := TranslateImageResponse([]byte(`{"status":"succeeded","output":"https://r.test/a.png","error":null}`), fixedNow)
	if err != nil {
		t.Fatalf("TranslateImageResponse: %v", err)
	}
	want := `{"created":1750000000,"data":[{"url":"https://r.test/a.png"}]}`
	if string(out) != want {
		t.Fatalf("out=%s want %s", out, want)
	}
}

// TestTranslateImageResponseURLArrayPreservesOrder 抓的回归:数组形态丢图或
// 乱序(用户付 n 张钱拿少图/错序)。
func TestTranslateImageResponseURLArrayPreservesOrder(t *testing.T) {
	out, err := TranslateImageResponse([]byte(`{"status":"succeeded","output":["https://r.test/1.png","https://r.test/2.png"]}`), fixedNow)
	if err != nil {
		t.Fatalf("TranslateImageResponse: %v", err)
	}
	var resp struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("产物不是 JSON: %v", err)
	}
	if len(resp.Data) != 2 || resp.Data[0].URL != "https://r.test/1.png" || resp.Data[1].URL != "https://r.test/2.png" {
		t.Fatalf("data=%+v want 2 张且保序", resp.Data)
	}
}

// TestTranslateImageResponseNonSucceededStatusRejected 误计费守卫:接受
// starting/processing(Prefer: wait 超窗未完成)或 failed/canceled 任何一个,
// 本测试红——这些状态下产物不可交付,产出成功形会让调用层 settle 计费。
// 判别关键:fixture 刻意携带非空 output——只有 status 守卫本身能拒掉它,
// 空 output 检查兜不住(否则放宽 status 白名单测试照样绿=非判别)。
func TestTranslateImageResponseNonSucceededStatusRejected(t *testing.T) {
	for _, status := range []string{"starting", "processing", "failed", "canceled", ""} {
		raw := `{"status":"` + status + `","output":["https://r.test/partial.png"]}`
		if _, err := TranslateImageResponse([]byte(raw), fixedNow); err == nil {
			t.Fatalf("status=%q 应 fail-loud(否则未完成/失败的预测被计费交付)", status)
		}
	}
}

// TestTranslateImageResponseErrorFieldRejectedEvenWhenSucceeded 抓的回归:
// error 非空但 status=succeeded 时按成功放行(上游异常被静默吞掉照常计费)。
// error 的 string 与对象两形态都要拒。
func TestTranslateImageResponseErrorFieldRejectedEvenWhenSucceeded(t *testing.T) {
	for name, raw := range map[string]string{
		"string error": `{"status":"succeeded","output":"https://r.test/a.png","error":"NSFW content detected"}`,
		"object error": `{"status":"failed","error":{"detail":"boom"}}`,
	} {
		if _, err := TranslateImageResponse([]byte(raw), fixedNow); err == nil {
			t.Fatalf("%s 应 fail-loud", name)
		}
	}
}

// TestTranslateImageResponseEmptyOutputRejected 抓的回归:succeeded 但无产物
// (null/空串/空数组/全空串数组)被翻译成空 data 的 200,客户端拿空响应还被计费。
func TestTranslateImageResponseEmptyOutputRejected(t *testing.T) {
	for _, raw := range []string{
		`{"status":"succeeded"}`,
		`{"status":"succeeded","output":null}`,
		`{"status":"succeeded","output":""}`,
		`{"status":"succeeded","output":[]}`,
		`{"status":"succeeded","output":[""]}`,
	} {
		if _, err := TranslateImageResponse([]byte(raw), fixedNow); err == nil {
			t.Fatalf("空 output 应 fail-loud: %s", raw)
		}
	}
}

// TestTranslateImageResponseMalformedShapesFailLoud 抓的回归:坏 JSON 或
// output 非 string/数组形态(如对象)被静默放行。
func TestTranslateImageResponseMalformedShapesFailLoud(t *testing.T) {
	for _, raw := range []string{
		`not-json`,
		`{"status":"succeeded","output":{"url":"https://r.test/a.png"}}`,
		`{"status":"succeeded","output":[1,2]}`,
	} {
		if _, err := TranslateImageResponse([]byte(raw), fixedNow); err == nil {
			t.Fatalf("形态异常应 fail-loud: %s", raw)
		}
	}
}
