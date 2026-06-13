package bodyfeatures

import "testing"

// TestDetect_OpenAIChatVisionDiscriminates 守 vision 检测: 一个带 image_url
// content part 的 OpenAI Chat body -> vision=true; 仅差那一个 part 改回 text 的
// body -> vision=false。两个 fixture 只差 image part。
// mutation: 删 partIsVision 里 case "image_url" -> 第一个断言转红。
func TestDetect_OpenAIChatVisionDiscriminates(t *testing.T) {
	withImage := `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"describe this"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}]}`
	if v, _, _ := Detect([]byte(withImage)); !v {
		t.Fatalf("vision: image_url part present, want true")
	}

	textOnly := `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"describe this"}]}]}`
	if v, _, _ := Detect([]byte(textOnly)); v {
		t.Fatalf("vision: text-only parts, want false")
	}
}

// TestDetect_VisionEmptyImageGuard 守空图误报: data URI base64 payload 为空时不算
// 真图 (镜像 sub2api 的 empty-image guard)。
// mutation: 去掉 isEmptyDataURI 判定 -> 空图被当真图 -> 转红。
func TestDetect_VisionEmptyImageGuard(t *testing.T) {
	emptyDataURI := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,"}}]}]}`
	if v, _, _ := Detect([]byte(emptyDataURI)); v {
		t.Fatalf("vision: empty base64 data URI must not count as a real image")
	}
	realDataURI := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`
	if v, _, _ := Detect([]byte(realDataURI)); !v {
		t.Fatalf("vision: non-empty base64 data URI must count as a real image")
	}
}

// TestDetect_ToolsDiscriminates 守 tools 检测: 非空 tools[] -> tools=true;
// 仅 legacy functions[] -> tools=true (覆盖 OpenAI 旧字段回退); 空 tools:[] -> false。
// mutation: 把 nonEmptyArray 换成 present (空数组算有) -> 空数组用例转红。
func TestDetect_ToolsDiscriminates(t *testing.T) {
	withTools := `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`
	if _, tl, _ := Detect([]byte(withTools)); !tl {
		t.Fatalf("tools: non-empty tools[], want true")
	}

	legacyFunctions := `{"messages":[{"role":"user","content":"hi"}],"functions":[{"name":"get_weather"}]}`
	if _, tl, _ := Detect([]byte(legacyFunctions)); !tl {
		t.Fatalf("tools: legacy functions[] only, want true")
	}

	emptyTools := `{"messages":[{"role":"user","content":"hi"}],"tools":[]}`
	if _, tl, _ := Detect([]byte(emptyTools)); tl {
		t.Fatalf("tools: empty tools[] is not a signal, want false")
	}
}

// TestDetect_JSONDiscriminates 守 json 检测: json_schema / json_object -> json=true;
// type:text 或缺省 -> false。
// mutation: 把 formatTypeIsJSON 改成 "存在 response_format 即 true" -> type:text 用例转红。
func TestDetect_JSONDiscriminates(t *testing.T) {
	jsonSchema := `{"messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{}}}}`
	if _, _, j := Detect([]byte(jsonSchema)); !j {
		t.Fatalf("json: response_format json_schema, want true")
	}
	jsonObject := `{"messages":[],"response_format":{"type":"json_object"}}`
	if _, _, j := Detect([]byte(jsonObject)); !j {
		t.Fatalf("json: response_format json_object, want true")
	}
	textFormat := `{"messages":[],"response_format":{"type":"text"}}`
	if _, _, j := Detect([]byte(textFormat)); j {
		t.Fatalf("json: response_format type=text is not structured output, want false")
	}
	none := `{"messages":[{"role":"user","content":"hi"}]}`
	if _, _, j := Detect([]byte(none)); j {
		t.Fatalf("json: no response_format, want false")
	}
}

// TestDetect_ResponsesTextFormatJSON 守 OpenAI Responses 把 json 嵌在 text.format 下的形状。
// mutation: 删 detectJSON 里的 text.format 分支 -> 转红。
func TestDetect_ResponsesTextFormatJSON(t *testing.T) {
	body := `{"input":"hi","text":{"format":{"type":"json_schema","name":"x","schema":{}}}}`
	if _, _, j := Detect([]byte(body)); !j {
		t.Fatalf("json: Responses text.format json_schema, want true")
	}
	textFmt := `{"input":"hi","text":{"format":{"type":"text"}}}`
	if _, _, j := Detect([]byte(textFmt)); j {
		t.Fatalf("json: Responses text.format type=text, want false")
	}
}

// TestDetect_AnthropicShape 守经 NewMessagesHandler 进来的 Anthropic 形状:
// content block type=image + 顶层 tools[] (带 input_schema)。
// mutation: 删 partIsVision 里 case "image" -> vision 转红; 删 tools 检测 -> tools 转红。
func TestDetect_AnthropicShape(t *testing.T) {
	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}],` +
		`"tools":[{"name":"get_weather","input_schema":{"type":"object"}}]}`
	v, tl, _ := Detect([]byte(body))
	if !v {
		t.Fatalf("vision: Anthropic image block, want true")
	}
	if !tl {
		t.Fatalf("tools: Anthropic top-level tools[], want true")
	}
}

// TestDetect_ResponsesInputImage 守 OpenAI Responses input_image part: top-level
// input 数组里出现 input_image -> vision=true; 仅 input_text -> false。
// mutation: 删 partIsVision 里 case "input_image" -> 转红。
func TestDetect_ResponsesInputImage(t *testing.T) {
	withImage := `{"input":[{"type":"input_image","image_url":"https://example.com/cat.png"}]}`
	if v, _, _ := Detect([]byte(withImage)); !v {
		t.Fatalf("vision: Responses input_image part, want true")
	}
	textOnly := `{"input":[{"type":"input_text","text":"hi"}]}`
	if v, _, _ := Detect([]byte(textOnly)); v {
		t.Fatalf("vision: Responses input_text only, want false")
	}
}

// TestDetect_DefensiveNeverPanics 守防御性: 各种畸形 / null / 空 / 错类型 body
// 必须返回 (false,false,false) 且不 panic。
// mutation: 去掉任一 ok-assert / type-guard -> panic -> 转红。
func TestDetect_DefensiveNeverPanics(t *testing.T) {
	cases := []string{
		``,                       // empty bytes
		`null`,                   // JSON null
		`{`,                      // truncated
		`not json at all`,        // garbage
		`{"messages":"bare"}`,    // messages as a string
		`{"messages":123}`,       // messages as a number
		`{"messages":[123,"x"]}`, // message elements wrong type
		`{"messages":[{"content":"plain string"}]}`,      // content string
		`{"messages":[{"content":42}]}`,                  // content number
		`{"messages":[{"content":[1,2,3]}]}`,             // parts as numbers
		`{"messages":[{"content":["a","b"]}]}`,           // parts as strings
		`{"messages":[{"content":[{"type":123}]}]}`,      // part type wrong
		`{"tools":"a string not array"}`,                 // tools wrong type
		`{"tools":{"k":"v"}}`,                            // tools as object
		`{"functions":42}`,                               // functions wrong type
		`{"response_format":"json"}`,                     // response_format string
		`{"response_format":[]}`,                         // response_format array
		`{"text":"hi"}`,                                  // text as string
		`{"input":42}`,                                   // input wrong type
		`{"input":[{"image_url":{"url":42}}]}`,           // nested url wrong type
		`{"messages":[{"content":[{"type":"image_url"}]}]}`, // image_url part missing payload
		`{"deeply":{"nested":{"junk":[null,{},[]]}}}`,    // unrelated junk
	}
	for _, body := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Detect panicked on %q: %v", body, r)
				}
			}()
			v, tl, j := Detect([]byte(body))
			if v || tl || j {
				t.Fatalf("Detect(%q) = (%v,%v,%v); want all false for malformed/empty input", body, v, tl, j)
			}
		}()
	}
}
