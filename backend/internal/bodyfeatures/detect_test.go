package bodyfeatures

import "testing"

// TestDetect_OpenAIChatVisionDiscriminates 守 vision 检测: 一个带 image_url
// content part 的 OpenAI Chat body -> vision=true; 仅差那一个 part 改回 text 的
// body -> vision=false。两个 fixture 只差 image part。
// 变异: 删 partIsVision 里 case "image_url" -> 第一个断言转红。
func TestDetect_OpenAIChatVisionDiscriminates(t *testing.T) {
	withImage := `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"describe this"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}]}`
	if v, _, _, _ := Detect([]byte(withImage)); !v {
		t.Fatalf("vision: image_url part present, want true")
	}

	textOnly := `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"describe this"}]}]}`
	if v, _, _, _ := Detect([]byte(textOnly)); v {
		t.Fatalf("vision: text-only parts, want false")
	}
}

// TestDetect_VisionEmptyImageGuard 守空图误报: data URI base64 payload 为空时不算
// 真图。
// 变异: 去掉 isEmptyDataURI 判定 -> 空图被当真图 -> 转红。
func TestDetect_VisionEmptyImageGuard(t *testing.T) {
	emptyDataURI := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,"}}]}]}`
	if v, _, _, _ := Detect([]byte(emptyDataURI)); v {
		t.Fatalf("vision: empty base64 data URI must not count as a real image")
	}
	realDataURI := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	if v, _, _, _ := Detect([]byte(realDataURI)); !v {
		t.Fatalf("vision: non-empty base64 data URI must count as a real image")
	}
}

// TestDetect_ToolsDiscriminates 守 tools 检测: 非空 tools[] -> tools=true;
// 仅 legacy functions[] -> tools=true (覆盖 OpenAI 旧字段回退); 空 tools:[] -> false。
// 变异: 把 nonEmptyArray 换成 present (空数组算有) -> 空数组用例转红。
func TestDetect_ToolsDiscriminates(t *testing.T) {
	withTools := `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`
	if _, tl, _, _ := Detect([]byte(withTools)); !tl {
		t.Fatalf("tools: non-empty tools[], want true")
	}

	legacyFunctions := `{"messages":[{"role":"user","content":"hi"}],"functions":[{"name":"get_weather"}]}`
	if _, tl, _, _ := Detect([]byte(legacyFunctions)); !tl {
		t.Fatalf("tools: legacy functions[] only, want true")
	}

	emptyTools := `{"messages":[{"role":"user","content":"hi"}],"tools":[]}`
	if _, tl, _, _ := Detect([]byte(emptyTools)); tl {
		t.Fatalf("tools: empty tools[] is not a signal, want false")
	}
}

// TestDetect_JSONDiscriminates 守 json 检测: json_schema / json_object -> json=true;
// type:text 或缺省 -> false。
// 变异: 把 formatTypeIsJSON 改成 "存在 response_format 即 true" -> type:text 用例转红。
func TestDetect_JSONDiscriminates(t *testing.T) {
	jsonSchema := `{"messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{}}}}`
	if _, _, j, _ := Detect([]byte(jsonSchema)); !j {
		t.Fatalf("json: response_format json_schema, want true")
	}
	jsonObject := `{"messages":[],"response_format":{"type":"json_object"}}`
	if _, _, j, _ := Detect([]byte(jsonObject)); !j {
		t.Fatalf("json: response_format json_object, want true")
	}
	textFormat := `{"messages":[],"response_format":{"type":"text"}}`
	if _, _, j, _ := Detect([]byte(textFormat)); j {
		t.Fatalf("json: response_format type=text is not structured output, want false")
	}
	none := `{"messages":[{"role":"user","content":"hi"}]}`
	if _, _, j, _ := Detect([]byte(none)); j {
		t.Fatalf("json: no response_format, want false")
	}
}

// TestDetect_ResponsesTextFormatJSON 守 OpenAI Responses 把 json 嵌在 text.format 下的形状。
// 变异: 删 detectJSON 里的 text.format 分支 -> 转红。
func TestDetect_ResponsesTextFormatJSON(t *testing.T) {
	body := `{"input":"hi","text":{"format":{"type":"json_schema","name":"x","schema":{}}}}`
	if _, _, j, _ := Detect([]byte(body)); !j {
		t.Fatalf("json: Responses text.format json_schema, want true")
	}
	textFmt := `{"input":"hi","text":{"format":{"type":"text"}}}`
	if _, _, j, _ := Detect([]byte(textFmt)); j {
		t.Fatalf("json: Responses text.format type=text, want false")
	}
}

// TestDetect_AnthropicShape 守经 NewMessagesHandler 进来的 Anthropic 形状:
// content block type=image + 顶层 tools[] (带 input_schema)。
// 变异: 删 partIsVision 里 case "image" -> vision 转红; 删 tools 检测 -> tools 转红。
func TestDetect_AnthropicShape(t *testing.T) {
	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}],` +
		`"tools":[{"name":"get_weather","input_schema":{"type":"object"}}]}`
	v, tl, _, _ := Detect([]byte(body))
	if !v {
		t.Fatalf("vision: Anthropic image block, want true")
	}
	if !tl {
		t.Fatalf("tools: Anthropic top-level tools[], want true")
	}
}

// TestDetect_ResponsesInputImage 守 OpenAI Responses input_image part: top-level
// input 数组里出现 input_image -> vision=true; 仅 input_text -> false。
// 变异: 删 partIsVision 里 case "input_image" -> 转红。
func TestDetect_ResponsesInputImage(t *testing.T) {
	withImage := `{"input":[{"type":"input_image","image_url":"https://example.com/cat.png"}]}`
	if v, _, _, _ := Detect([]byte(withImage)); !v {
		t.Fatalf("vision: Responses input_image part, want true")
	}
	textOnly := `{"input":[{"type":"input_text","text":"hi"}]}`
	if v, _, _, _ := Detect([]byte(textOnly)); v {
		t.Fatalf("vision: Responses input_text only, want false")
	}
}

// TestDetect_AudioInputPartDiscriminates 守 audio 检测的输入支路: 一条带
// input_audio content part (有 data/format 载荷) 的 OpenAI Chat body -> audio=true;
// 仅差那一个 part 改回 text 的 body -> audio=false。两个 fixture 只差 input_audio part。
// 变异: 删 partIsAudio 里 type=="input_audio" 分支 (或令其恒 false) -> 第一个断言转红。
func TestDetect_AudioInputPartDiscriminates(t *testing.T) {
	withAudio := `{"model":"gpt-4o-audio-preview","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"transcribe this"},` +
		`{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`
	v, tl, j, a := Detect([]byte(withAudio))
	if !a {
		t.Fatalf("audio: input_audio part present, want audio=true")
	}
	// input_audio 绝不能泄漏到其它三个标志 —— 它只属于 audio。
	if v || tl || j {
		t.Fatalf("audio: input_audio must flip audio only, got (v=%v,tl=%v,j=%v)", v, tl, j)
	}

	textOnly := `{"model":"gpt-4o-audio-preview","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"transcribe this"}]}]}`
	if _, _, _, a := Detect([]byte(textOnly)); a {
		t.Fatalf("audio: text-only parts, want audio=false")
	}
}

// TestDetect_AudioEmptyInputGuard 守空音频误报: input_audio part 没有载荷对象时不算
// 真音频 (镜像 vision 的 empty-payload guard)。
// 变异: 把 partIsAudio 改成只看 type 不看 present(input_audio) -> 空 part 误报 -> 转红。
func TestDetect_AudioEmptyInputGuard(t *testing.T) {
	emptyPart := `{"messages":[{"role":"user","content":[` +
		`{"type":"input_audio"}]}]}`
	if _, _, _, a := Detect([]byte(emptyPart)); a {
		t.Fatalf("audio: input_audio part with no payload must not count, want false")
	}
	nullPayload := `{"messages":[{"role":"user","content":[` +
		`{"type":"input_audio","input_audio":null}]}]}`
	if _, _, _, a := Detect([]byte(nullPayload)); a {
		t.Fatalf("audio: input_audio with null payload must not count, want false")
	}
}

// TestDetect_AudioModalitiesDiscriminates 守 audio 检测的输出支路: 顶层
// modalities 数组含 "audio" -> audio=true; 仅 ["text"] 或缺省 -> false。
// 变异: 删 detectAudio 里的 modalitiesHaveAudio 调用 (或令其恒 false) -> 第一个断言转红。
func TestDetect_AudioModalitiesDiscriminates(t *testing.T) {
	withAudioMode := `{"model":"gpt-4o-audio-preview","modalities":["text","audio"],` +
		`"messages":[{"role":"user","content":"say hello out loud"}]}`
	if _, _, _, a := Detect([]byte(withAudioMode)); !a {
		t.Fatalf("audio: modalities containing \"audio\", want audio=true")
	}

	textModeOnly := `{"model":"gpt-4o","modalities":["text"],` +
		`"messages":[{"role":"user","content":"hi"}]}`
	if _, _, _, a := Detect([]byte(textModeOnly)); a {
		t.Fatalf("audio: modalities [\"text\"] only is not an audio signal, want false")
	}
}

// TestDetect_PlainTextNoAudio 守回退兼容: 普通纯文本请求绝不 flag audio
// (不引入额外约束、不缩小账号集)。
// 变异: 让 detectAudio 默认返回 true / 漏掉空数组 guard -> 转红。
func TestDetect_PlainTextNoAudio(t *testing.T) {
	plain := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	if v, tl, j, a := Detect([]byte(plain)); a {
		t.Fatalf("audio: plain text request must NOT flag audio (back-compat), got (v=%v,tl=%v,j=%v,a=%v)", v, tl, j, a)
	}
}

// TestDetect_DefensiveNeverPanics 守防御性: 各种畸形 / null / 空 / 错类型 body
// 必须返回 (false,false,false,false) 且不 panic。新增 modalities / input_audio 的
// 错类型 fixture,保证 audio 支路同样不 panic、不误报。
// 变异: 去掉任一 ok-assert / type-guard -> panic -> 转红。
func TestDetect_DefensiveNeverPanics(t *testing.T) {
	cases := []string{
		``,                       // 空字节
		`null`,                   // JSON null 值
		`{`,                      // 被截断
		`not json at all`,        // 垃圾数据
		`{"messages":"bare"}`,    // messages 是字符串
		`{"messages":123}`,       // messages 是数字
		`{"messages":[123,"x"]}`, // message 元素类型错误
		`{"messages":[{"content":"plain string"}]}`,         // content 是字符串
		`{"messages":[{"content":42}]}`,                     // content 是数字
		`{"messages":[{"content":[1,2,3]}]}`,                // parts 是数字
		`{"messages":[{"content":["a","b"]}]}`,              // parts 是字符串
		`{"messages":[{"content":[{"type":123}]}]}`,         // part type 错误
		`{"tools":"a string not array"}`,                    // tools 类型错误
		`{"tools":{"k":"v"}}`,                               // tools 是对象
		`{"functions":42}`,                                  // functions 类型错误
		`{"response_format":"json"}`,                        // response_format 是字符串
		`{"response_format":[]}`,                            // response_format 是数组
		`{"text":"hi"}`,                                     // text 是字符串
		`{"input":42}`,                                      // input 类型错误
		`{"input":[{"image_url":{"url":42}}]}`,              // 嵌套 url 类型错误
		`{"messages":[{"content":[{"type":"image_url"}]}]}`, // image_url part 缺少载荷
		`{"deeply":{"nested":{"junk":[null,{},[]]}}}`,       // 无关垃圾
		`{"modalities":"audio"}`,                            // modalities 是裸字符串
		`{"modalities":{"audio":true}}`,                     // modalities 是对象
		`{"modalities":42}`,                                 // modalities 是数字
		`{"modalities":[1,2,3]}`,                            // modalities 元素非字符串
		`{"messages":[{"content":[{"type":"input_audio"}]}]}`, // input_audio part 缺少载荷
	}
	for _, body := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Detect panicked on %q: %v", body, r)
				}
			}()
			v, tl, j, a := Detect([]byte(body))
			if v || tl || j || a {
				t.Fatalf("Detect(%q) = (%v,%v,%v,%v); want all false for malformed/empty input", body, v, tl, j, a)
			}
		}()
	}
}
