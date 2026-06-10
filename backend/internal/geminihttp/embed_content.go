package geminihttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// embed_content.go — Gemini 原生 embeddings 入站动作(audit native-protocol-gaps):
// POST /v1beta/models/{model}:embedContent 与 :batchEmbedContents 此前掉进
// unknown_gemini_action 404,Gemini SDK 的 embeddings 客户端直接断。
//
// 实现方式:HTTP 层翻译包装复用既有 embeddings 管线(internal/embeddingshttp,
// 完整 auth/配额/计费/结算)——把 Gemini 形请求翻成 OpenAI 形 {model,input},
// 内部调用 embeddings handler,再把 OpenAI 形响应翻回 Gemini 形
// {"embedding":{"values":[...]}} / {"embeddings":[...]}。上游侧由运营把 Gemini
// embedding 模型的 channel base_url 指向 Google 的 OpenAI 兼容 embeddings 端点
// (generativelanguage.googleapis.com/v1beta/openai)即可全链路打通。

const (
	ActionEmbedContent       = "embedContent"
	ActionBatchEmbedContents = "batchEmbedContents"
)

type geminiContentParts struct {
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

func (c geminiContentParts) joinedText() string {
	var b strings.Builder
	for _, p := range c.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// bufferedResponseWriter 捕获内部 handler 的输出(生产代码不引 httptest)。
type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: http.Header{}, status: http.StatusOK}
}

func (b *bufferedResponseWriter) Header() http.Header         { return b.header }
func (b *bufferedResponseWriter) WriteHeader(code int)        { b.status = code }
func (b *bufferedResponseWriter) Write(p []byte) (int, error) { return b.body.Write(p) }

// serveGeminiEmbed 处理 embedContent / batchEmbedContents。
func serveGeminiEmbed(w http.ResponseWriter, r *http.Request, model string, embeddings http.Handler, batch bool) {
	if embeddings == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini embeddings dependency unset")
		return
	}
	body, ok := readRequestBody(w, r)
	if !ok {
		return
	}

	var inputs []string
	if batch {
		var req struct {
			Requests []struct {
				Model   string             `json:"model"`
				Content geminiContentParts `json:"content"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Requests) == 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "batchEmbedContents requires non-empty requests[]")
			return
		}
		for _, item := range req.Requests {
			// 单次内部调用只能一个计费模型:per-request model 覆盖若与 path 模型
			// 不一致则明确拒绝,绝不静默换模型计费。Gemini 语义里 model 形如
			// "models/<id>",比较时剥前缀。
			if m := strings.TrimPrefix(strings.TrimSpace(item.Model), "models/"); m != "" && m != model {
				writeJSONError(w, http.StatusBadRequest, "invalid_request", "batchEmbedContents per-request model must match the path model")
				return
			}
			inputs = append(inputs, item.Content.joinedText())
		}
	} else {
		var req struct {
			Content geminiContentParts `json:"content"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Content.Parts) == 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "embedContent requires content.parts")
			return
		}
		inputs = []string{req.Content.joinedText()}
	}

	oaBody, err := json.Marshal(map[string]any{"model": model, "input": inputs})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request translation failed")
		return
	}
	inner := r.Clone(r.Context())
	inner.Body = io.NopCloser(bytes.NewReader(oaBody))
	inner.ContentLength = int64(len(oaBody))
	inner.Header = r.Header.Clone()
	inner.Header.Set("Content-Type", "application/json")

	rec := newBufferedResponseWriter()
	embeddings.ServeHTTP(rec, inner)
	if rec.status != http.StatusOK {
		// 错误原样透传(auth/配额/计费错误码与 /v1/embeddings 一致)
		for k, vs := range rec.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
		return
	}

	var oaResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &oaResp); err != nil || len(oaResp.Data) == 0 {
		writeJSONError(w, http.StatusBadGateway, "upstream_response_invalid", "embeddings upstream response unparseable")
		return
	}
	// 保序:按 index 归位(OpenAI 形携带 index)
	values := make([][]float64, len(oaResp.Data))
	for i, d := range oaResp.Data {
		idx := d.Index
		if idx < 0 || idx >= len(values) {
			idx = i
		}
		values[idx] = d.Embedding
	}

	w.Header().Set("Content-Type", "application/json")
	if batch {
		out := make([]map[string]any, 0, len(values))
		for _, v := range values {
			out = append(out, map[string]any{"values": v})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"embedding": map[string]any{"values": values[0]}})
}
