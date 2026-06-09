// Package respdecompress decodes an HTTP response body according to its
// Content-Encoding. It exists so the anti-ban mimicry egress can send a
// browser-like Accept-Encoding (gzip, deflate, br, zstd) and still read the
// response: Go's transport only auto-decodes gzip, and only when IT chose the
// Accept-Encoding. Once we set Accept-Encoding ourselves, decoding is on us.
//
// Fail-safe: an unknown or empty encoding, or a decoder construction error,
// returns the original body unchanged so a response is never broken.
package respdecompress

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// BrowserAcceptEncoding is the Accept-Encoding a modern Chrome/Firefox sends.
const BrowserAcceptEncoding = "gzip, deflate, br, zstd"

// Wrap returns a ReadCloser that decodes body per the Content-Encoding token.
// Supported: gzip, deflate, br, zstd. Anything else (incl. empty/identity)
// returns body unchanged. On a decoder-construction error the original body is
// returned with the error, so callers may choose to keep streaming the raw body.
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

// Supported reports whether Wrap will actively decode the encoding (i.e. it is
// one of gzip/deflate/br/zstd). Used by callers to decide whether to strip the
// Content-Encoding/Content-Length headers after wrapping.
func Supported(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip", "deflate", "br", "zstd":
		return true
	default:
		return false
	}
}

// wrapped reads decoded bytes from r and closes both the decoder (if it is a
// Closer) and the underlying network body.
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
