package imageshttp

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildImageEditMultipart(t *testing.T, fields map[string]string, withImageFile bool) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if withImageFile {
		fw, err := w.CreateFormFile("image", "source.png")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = fw.Write([]byte("\\x89PNG\\r\\n fake png bytes"))
	}
	_ = w.Close()
	return w.FormDataContentType(), buf.Bytes()
}

// 判别测试:标准 OpenAI image edit 的 multipart/form-data 请求必须能过 validateRequest
// (此前一律 json.Unmarshal → 400 invalid_json,标准 SDK images.edit 全断)。
// Mutation guard: 去掉 Content-Type 分叉(走 JSON) → multipart 当损坏 JSON,ok=false 红。
func TestValidateRequest_MultipartImageEditParsed(t *testing.T) {
	ct, body := buildImageEditMultipart(t, map[string]string{
		"model":  "gpt-image-1",
		"prompt": "make it blue",
		"n":      "2",
		"size":   "1024x1024",
	}, true)

	r := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	gotBody, req, ok := validateRequest(rec, r, imageEndpointEdits)
	if !ok {
		t.Fatalf("multipart image edit 应通过校验; status=%d body=%s", rec.Code, rec.Body.String())
	}
	if req.Model != "gpt-image-1" || req.PromptText() != "make it blue" {
		t.Fatalf("multipart 字段解析错: %+v", req)
	}
	if req.Amount() != 2 || req.Size != "1024x1024" {
		t.Fatalf("n/size 解析错: n=%d size=%q", req.Amount(), req.Size)
	}
	if !req.hasImageReference() {
		t.Fatal("image 文件 part 应满足图片引用要求")
	}
	// 原始 multipart 字节必须原样返回(交下游 relaybody 改写),不被改成 JSON
	if !bytes.Equal(gotBody, body) {
		t.Fatal("multipart body 必须原样保留交下游")
	}
}

// variations(无 prompt)+ 图片文件:通过;缺图片文件:400。
func TestValidateRequest_MultipartVariationsAndMissingImage(t *testing.T) {
	ct, body := buildImageEditMultipart(t, map[string]string{"model": "dall-e-2", "size": "512x512"}, true)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/variations", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	if _, _, ok := validateRequest(httptest.NewRecorder(), r, imageEndpointVariations); !ok {
		t.Fatal("带图片文件的 variations 应通过")
	}

	ct2, body2 := buildImageEditMultipart(t, map[string]string{"model": "dall-e-2", "prompt": "x"}, false)
	r2 := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body2))
	r2.Header.Set("Content-Type", ct2)
	rec2 := httptest.NewRecorder()
	if _, _, ok := validateRequest(rec2, r2, imageEndpointEdits); ok {
		t.Fatal("缺图片文件的 edits 必须 400")
	}
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("缺图片应 400; got %d", rec2.Code)
	}
}

// JSON 路径不受影响(回归守卫)。
func TestValidateRequest_JSONStillWorks(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		bytes.NewReader([]byte(`{"model":"dall-e-3","prompt":"a cat","size":"1024x1024"}`)))
	r.Header.Set("Content-Type", "application/json")
	if _, req, ok := validateRequest(httptest.NewRecorder(), r, imageEndpointGenerations); !ok || req.Model != "dall-e-3" {
		t.Fatalf("JSON 路径回归: ok=%v model=%q", ok, req.Model)
	}
}
