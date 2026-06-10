// Package reqdecompress 透明解码入站请求体的 Content-Encoding。
//
// 缘由:Codex CLI 0.125+ 默认带 Content-Encoding: zstd 发请求体(delta-mine #8,
// 参照 sub2api 798fd673/40feb86b)。网关若不解压,下游 JSON 解析把压缩字节当损坏
// JSON 拒掉,Codex 系新版客户端整体打不通。本中间件在最外层透明解码 zstd/gzip/
// deflate/br,剥掉 Content-Encoding 头并修正 ContentLength,使下游各 handler 的
// 原始 io.ReadAll 读到明文。配解压上限防解压炸弹(小压缩体膨胀成 OOM),超限返 413
// (parity-or-better:sub2api 静默截断,HUAKAI 明确拒绝)。
package reqdecompress

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/respdecompress"
)

// DefaultMaxDecodedBytes 是解压后字节上限(防解压炸弹)。业务级更小的限额仍由
// 各 handler 自己的 MaxBytesReader 负责;这里只兜住"小压缩体膨胀成 OOM"。
const DefaultMaxDecodedBytes int64 = 64 << 20 // 64 MiB

// Middleware 返回一个透明解码入站请求体的 chi/net-http 中间件。
func Middleware(maxDecodedBytes int64) func(http.Handler) http.Handler {
	if maxDecodedBytes <= 0 {
		maxDecodedBytes = DefaultMaxDecodedBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
			// 无编码 / identity / 无 body / 不认的编码:原样放过(让下游决定)。
			if r.Body == nil || enc == "" || enc == "identity" || !respdecompress.Supported(enc) {
				next.ServeHTTP(w, r)
				return
			}
			decoded, err := respdecompress.Wrap(r.Body, enc)
			if err != nil {
				http.Error(w, `{"error":{"code":"invalid_content_encoding","message":"failed to initialize request body decoder"}}`, http.StatusBadRequest)
				return
			}
			// 解压炸弹防护:只读到上限+1 字节,超出即拒(不 OOM)。
			limited := io.LimitReader(decoded, maxDecodedBytes+1)
			buf, readErr := io.ReadAll(limited)
			_ = decoded.Close()
			if readErr != nil {
				http.Error(w, `{"error":{"code":"invalid_content_encoding","message":"failed to decode request body"}}`, http.StatusBadRequest)
				return
			}
			if int64(len(buf)) > maxDecodedBytes {
				http.Error(w, `{"error":{"code":"payload_too_large","message":"decoded request body exceeds limit"}}`, http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(buf))
			r.ContentLength = int64(len(buf))
			r.Header.Del("Content-Encoding")
			r.Header.Del("Content-Length")
			next.ServeHTTP(w, r)
		})
	}
}
