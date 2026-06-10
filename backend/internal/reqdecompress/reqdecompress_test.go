package reqdecompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func zstdBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	_, _ = enc.Write(raw)
	_ = enc.Close()
	return buf.Bytes()
}

// 判别测试:Codex CLI 0.125+ 发的 zstd 压缩体必须被透明解码,下游读到明文 JSON。
// 此前网关零入站解码 → 压缩体被当损坏 JSON 拒掉,Codex 系客户端整体打不通。
// Mutation guard: 中间件不解码(直接 next)→ 下游读到压缩字节,断言红。
func TestMiddleware_DecodesZstdRequestBody(t *testing.T) {
	want := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	var seen string
	var seenEnc string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
		seenEnc = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(zstdBytes(t, []byte(want))))
	req.Header.Set("Content-Encoding", "zstd")
	rec := httptest.NewRecorder()
	Middleware(0)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if seen != want {
		t.Fatalf("下游未读到解码后明文\n got=%q\nwant=%q", seen, want)
	}
	if seenEnc != "" {
		t.Fatalf("Content-Encoding 头必须剥除,下游不应再见到: %q", seenEnc)
	}
}

// gzip 同样支持。
func TestMiddleware_DecodesGzip(t *testing.T) {
	want := `{"a":1}`
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(want))
	_ = zw.Close()

	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	Middleware(0)(next).ServeHTTP(httptest.NewRecorder(), req)
	if seen != want {
		t.Fatalf("gzip 未解码: got=%q", seen)
	}
}

// 无编码请求原样放过(不动 body)。
func TestMiddleware_PassesThroughPlain(t *testing.T) {
	want := `{"plain":true}`
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(want)))
	Middleware(0)(next).ServeHTTP(httptest.NewRecorder(), req)
	if seen != want {
		t.Fatalf("明文被改动: got=%q", seen)
	}
}

// 解压炸弹:超出解码上限必须 413,绝不 OOM/不放行。
// Mutation guard: 去掉 LimitReader/上限检查 → 这条不再 413。
func TestMiddleware_DecompressionBombRejected(t *testing.T) {
	// 10MB 全零压成很小的 zstd 体;上限设 1MB → 必 413
	bomb := zstdBytes(t, bytes.Repeat([]byte{0}, 10<<20))
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(bomb))
	req.Header.Set("Content-Encoding", "zstd")
	rec := httptest.NewRecorder()
	Middleware(1<<20)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("解压炸弹必须 413; got %d", rec.Code)
	}
	if called {
		t.Fatal("超限请求不得到达下游 handler")
	}
}
