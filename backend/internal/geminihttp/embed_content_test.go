package geminihttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stub embeddings 管线:记录收到的 OpenAI 形请求体,回固定 OpenAI 形响应。
type stubEmbeddingsHandler struct {
	gotBody []byte
	status  int
	resp    string
}

func (s *stubEmbeddingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.gotBody, _ = io.ReadAll(r.Body)
	if s.status == 0 {
		s.status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	_, _ = w.Write([]byte(s.resp))
}

func newEmbedTestHandler(stub *stubEmbeddingsHandler) http.Handler {
	return NewGenerateContentHandler(Deps{Embeddings: stub})
}

// 判别测试:POST /v1beta/models/{model}:embedContent 此前掉 unknown_gemini_action
// 404(Gemini SDK embeddings 客户端直接断)。现在必须:① 把 content.parts 文本翻成
// OpenAI 形 {model,input} 交给 embeddings 管线(完整计费);② 把 OpenAI 形响应翻回
// Gemini 形 {"embedding":{"values":[...]}}。
// Mutation guard: 翻译方向/形状错 → 对应断言红。
func TestGeminiEmbedContent_TranslatesRoundTrip(t *testing.T) {
	stub := &stubEmbeddingsHandler{resp: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}]}`}
	h := newEmbedTestHandler(stub)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-embedding-001:embedContent",
		bytes.NewReader([]byte(`{"content":{"parts":[{"text":"hello "},{"text":"world"}]}}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// ① 入站翻译:parts 拼接成单条 input,model 取 path
	var got struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.Unmarshal(stub.gotBody, &got); err != nil {
		t.Fatalf("内部请求体非 OpenAI 形: %v %s", err, stub.gotBody)
	}
	if got.Model != "gemini-embedding-001" || len(got.Input) != 1 || got.Input[0] != "hello world" {
		t.Fatalf("入站翻译错: %+v", got)
	}
	// ② 出站翻译:Gemini 单条形状
	var out struct {
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Embedding.Values) != 3 || out.Embedding.Values[1] != 0.2 {
		t.Fatalf("出站翻译错: %v %s", err, rec.Body.String())
	}
}

// batch:requests[] → input[],响应按 index 保序翻回 embeddings[]。
func TestGeminiBatchEmbedContents_OrderPreserved(t *testing.T) {
	// 故意乱序返回 index 1,0 → 输出必须按 index 归位
	stub := &stubEmbeddingsHandler{resp: `{"data":[{"index":1,"embedding":[9.9]},{"index":0,"embedding":[1.1]}]}`}
	h := newEmbedTestHandler(stub)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-embedding-001:batchEmbedContents",
		bytes.NewReader([]byte(`{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}]}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Input []string `json:"input"`
	}
	_ = json.Unmarshal(stub.gotBody, &got)
	if len(got.Input) != 2 || got.Input[0] != "a" || got.Input[1] != "b" {
		t.Fatalf("batch 入站翻译错: %+v", got)
	}
	var out struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Embeddings) != 2 {
		t.Fatalf("batch 出站形状错: %v %s", err, rec.Body.String())
	}
	if out.Embeddings[0].Values[0] != 1.1 || out.Embeddings[1].Values[0] != 9.9 {
		t.Fatalf("乱序响应未按 index 归位: %s", rec.Body.String())
	}
}

// per-request model 与 path 不一致 → 400 明确拒绝(绝不静默换模型计费)。
func TestGeminiBatchEmbed_ModelMismatchRejected(t *testing.T) {
	stub := &stubEmbeddingsHandler{resp: `{}`}
	h := newEmbedTestHandler(stub)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-embedding-001:batchEmbedContents",
		bytes.NewReader([]byte(`{"requests":[{"model":"models/other-model","content":{"parts":[{"text":"a"}]}}]}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("model 不匹配必须 400; got %d %s", rec.Code, rec.Body.String())
	}
	if stub.gotBody != nil {
		t.Fatal("不匹配请求不得到达计费管线")
	}
}

// 内部管线错误(如 402 余额不足)原样透传,错误码与 /v1/embeddings 一致。
func TestGeminiEmbed_ErrorPassthrough(t *testing.T) {
	stub := &stubEmbeddingsHandler{status: http.StatusPaymentRequired, resp: `{"error":{"code":"insufficient_balance"}}`}
	h := newEmbedTestHandler(stub)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-embedding-001:embedContent",
		bytes.NewReader([]byte(`{"content":{"parts":[{"text":"x"}]}}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "insufficient_balance") {
		t.Fatalf("错误未原样透传: %d %s", rec.Code, rec.Body.String())
	}
}
