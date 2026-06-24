package relaybody

import (
	"bytes"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 守:JSON 体别名重映射时,出站 model 改成上游真实 id。Mutation: 不改写 -> "my-alias" -> 红。
func TestRewriteModel_JSONRewritesModelToUpstream(t *testing.T) {
	body := []byte(`{"model":"my-alias","input":"hi","voice":"alloy"}`)
	out, ct, isMulti := RewriteModel(body, "application/json", "tts-1-real")
	if isMulti || ct != "application/json" {
		t.Fatalf("isMulti=%v ct=%q", isMulti, ct)
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["model"] != "tts-1-real" {
		t.Fatalf("model=%v want tts-1-real", obj["model"])
	}
	if obj["input"] != "hi" {
		t.Fatalf("other fields lost: %v", obj)
	}
}

// 守:multipart 重映射时 model 字段改成上游 id,文件与其它字段保留。Mutation: 不改写 -> "whisper-alias" -> 红。
func TestRewriteModel_MultipartRewritesModelKeepsFile(t *testing.T) {
	var in bytes.Buffer
	w := multipart.NewWriter(&in)
	_ = w.WriteField("model", "whisper-alias")
	fw, _ := w.CreateFormFile("file", "clip.wav")
	_, _ = fw.Write([]byte("RIFFfake-audio"))
	_ = w.WriteField("response_format", "json")
	_ = w.Close()
	out, ct, isMulti := RewriteModel(in.Bytes(), w.FormDataContentType(), "whisper-1-real")
	if !isMulti {
		t.Fatal("multipart not detected")
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("bad rewritten content-type: %q", ct)
	}
	r := multipart.NewReader(bytes.NewReader(out), params["boundary"])
	gotModel, gotFile, gotFormat := "", "", ""
	for {
		p, e := r.NextPart()
		if e != nil {
			break
		}
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(p)
		switch p.FormName() {
		case "model":
			gotModel = b.String()
		case "file":
			gotFile = b.String()
		case "response_format":
			gotFormat = b.String()
		}
	}
	if gotModel != "whisper-1-real" {
		t.Fatalf("model=%q want whisper-1-real", gotModel)
	}
	if gotFile != "RIFFfake-audio" {
		t.Fatalf("file corrupted: %q", gotFile)
	}
	if gotFormat != "json" {
		t.Fatalf("other field lost: %q", gotFormat)
	}
}

func TestRewriteModel_NoModelOrEmptyUpstreamUnchanged(t *testing.T) {
	body := []byte(`{"input":"hi"}`)
	if out, _, _ := RewriteModel(body, "application/json", "x"); !bytes.Equal(out, body) {
		t.Fatalf("no-model JSON must be unchanged, got %s", out)
	}
	if out, _, _ := RewriteModel([]byte(`{"model":"a"}`), "application/json", ""); !strings.Contains(string(out), `"a"`) {
		t.Fatal("empty upstream must leave body unchanged")
	}
}

// 守:model 已是上游 id 时逐字转发(JSON 不重排、multipart 不重编码、保留原 boundary)。
// Mutation: 去掉 same-model 短路改无条件改写 -> JSON 重排/multipart 新 boundary -> 红。
func TestRewriteModel_SameModelForwardsVerbatim(t *testing.T) {
	jb := []byte(`{"model":"whisper-1","language":"en"}`)
	if out, ct, isMulti := RewriteModel(jb, "application/json", "whisper-1"); isMulti || ct != "application/json" || !bytes.Equal(out, jb) {
		t.Fatalf("JSON same-model must be verbatim: out=%s ct=%q isMulti=%v", out, ct, isMulti)
	}
	var in bytes.Buffer
	w := multipart.NewWriter(&in)
	_ = w.WriteField("model", "whisper-1")
	fw, _ := w.CreateFormFile("file", "c.wav")
	_, _ = fw.Write([]byte("data"))
	_ = w.Close()
	mb := in.Bytes()
	if out, ct, isMulti := RewriteModel(mb, w.FormDataContentType(), "whisper-1"); isMulti || ct != w.FormDataContentType() || !bytes.Equal(out, mb) {
		t.Fatalf("multipart same-model must be verbatim (original boundary): isMulti=%v", isMulti)
	}
}

func TestConfigureRequestBodyLimit(t *testing.T) {
	t.Cleanup(func() { requestBodyLimitBytes = defaultRequestBodyLimitBytes })

	ConfigureRequestBodyLimit(7 << 20)
	if got := RequestBodyLimit(); got != 7<<20 {
		t.Fatalf("RequestBodyLimit()=%d,want %d", got, int64(7<<20))
	}
	ConfigureRequestBodyLimit(0)
	if got := RequestBodyLimit(); got != 7<<20 {
		t.Fatalf("ConfigureRequestBodyLimit(0) 不应清零,got %d", got)
	}
}

func TestReadLimitedRequestBodyHonorsConfiguredLimit(t *testing.T) {
	reqOver := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(strings.Repeat("x", 20)))
	if _, err := ReadLimitedRequestBody(httptest.NewRecorder(), reqOver, 10); err == nil {
		t.Fatal("超过传入上限的 body 应返回 error")
	}

	reqUnder := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(strings.Repeat("x", 8)))
	body, err := ReadLimitedRequestBody(httptest.NewRecorder(), reqUnder, 10)
	if err != nil {
		t.Fatalf("未超过传入上限的 body 不应报错:%v", err)
	}
	if len(body) != 8 {
		t.Fatalf("body len=%d,want 8", len(body))
	}
}
