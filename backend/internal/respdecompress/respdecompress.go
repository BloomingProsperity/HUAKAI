// 包 respdecompress 根据 HTTP 响应体的 Content-Encoding 对其解码。
// 它的存在是为了让反封禁的拟真出口可以发送类浏览器的
// Accept-Encoding(gzip, deflate, br, zstd)并仍能读取响应:
// Go 的 transport 只会自动解码 gzip,且仅当 Accept-Encoding 是它自己选的时候。
// 一旦我们自己设置 Accept-Encoding,解码就由我们负责。
//
// 故障安全:未知或为空的编码,或解码器构造出错时,
// 原样返回未改动的 body,这样响应永远不会被破坏。
package respdecompress

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// BrowserAcceptEncoding 是现代 Chrome/Firefox 发送的 Accept-Encoding。
const BrowserAcceptEncoding = "gzip, deflate, br, zstd"

// Wrap 返回一个 ReadCloser,按 Content-Encoding 标记对 body 解码。
// 支持:gzip、deflate、br、zstd。其它任何编码(含空/identity)
// 原样返回 body。解码器构造出错时,原始 body 会连同 error 一起返回,
// 这样调用方可以选择继续流式传输原始 body。
func Wrap(body io.ReadCloser, encoding string) (io.ReadCloser, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(body)
		if err != nil {
			return body, err
		}
		return &wrapped{r: zr, underlying: body}, nil
	case "deflate":
		return &wrapped{r: flate.NewReader(body), underlying: body}, nil
	case "br":
		return &wrapped{r: brotli.NewReader(body), underlying: body}, nil
	case "zstd":
		zr, err := zstd.NewReader(body)
		if err != nil {
			return body, err
		}
		return &wrapped{r: zr.IOReadCloser(), underlying: body}, nil
	default:
		return body, nil
	}
}

// Supported 报告 Wrap 是否会主动解码该编码(即它是
// gzip/deflate/br/zstd 之一)。调用方据此决定包装后是否
// 剥除 Content-Encoding/Content-Length 头。
func Supported(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip", "deflate", "br", "zstd":
		return true
	default:
		return false
	}
}

// wrapped 从 r 读取解码后的字节,并同时关闭解码器(若它是
// Closer)与底层网络 body。
type wrapped struct {
	r          io.Reader
	underlying io.Closer
}

func (w *wrapped) Read(p []byte) (int, error) { return w.r.Read(p) }

func (w *wrapped) Close() error {
	if c, ok := w.r.(io.Closer); ok {
		_ = c.Close()
	}
	return w.underlying.Close()
}
