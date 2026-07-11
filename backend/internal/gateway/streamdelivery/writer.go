// 包 streamdelivery 提供流式响应的完整写入与业务交付判定。
package streamdelivery

import (
	"io"
	"net/http"
)

// WriteAndFlush 完整写出一帧并立即刷新。
func WriteAndFlush(w http.ResponseWriter, body []byte) error {
	written, err := w.Write(body)
	if err != nil {
		return err
	}
	if written != len(body) {
		return io.ErrShortWrite
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// WriteBusinessAndFlush 仅在本次业务帧完整且无错写入时报告已交付。
func WriteBusinessAndFlush(w http.ResponseWriter, body []byte) (bool, error) {
	written, err := w.Write(body)
	if err != nil {
		return false, err
	}
	if written != len(body) {
		return false, io.ErrShortWrite
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return true, nil
}
