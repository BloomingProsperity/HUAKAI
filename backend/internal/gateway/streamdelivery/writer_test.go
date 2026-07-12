package streamdelivery

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

type scriptedResponseWriter struct {
	header  http.Header
	written int
	err     error
	flushes int
}

func (w *scriptedResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*scriptedResponseWriter) WriteHeader(int) {}

func (w *scriptedResponseWriter) Write(body []byte) (int, error) {
	n := w.written
	if n < 0 || n > len(body) {
		n = len(body)
	}
	return n, w.err
}

func (w *scriptedResponseWriter) Flush() { w.flushes++ }

// TestWriteBusinessAndFlush_OnlyCompleteErrorFreeFrameIsDelivered 守住客户端交付证据：
// 只有整帧且无错误的 Write 才算已交付，短写或完整长度带错都不得触发结算资格。
func TestWriteBusinessAndFlush_OnlyCompleteErrorFreeFrameIsDelivered(t *testing.T) {
	sentinel := errors.New("write failed")
	body := []byte("data: business\n\n")
	tests := []struct {
		name          string
		written       int
		err           error
		wantDelivered bool
		wantErr       error
		wantFlushes   int
	}{
		{name: "零写带错", written: 0, err: io.ErrClosedPipe, wantErr: io.ErrClosedPipe},
		{name: "短写无底层错误", written: 3, wantErr: io.ErrShortWrite},
		{name: "整帧带错", written: -1, err: sentinel, wantErr: sentinel},
		{name: "整帧无错", written: -1, wantDelivered: true, wantFlushes: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &scriptedResponseWriter{written: tc.written, err: tc.err}
			delivered, err := WriteBusinessAndFlush(w, body)
			if delivered != tc.wantDelivered {
				t.Fatalf("delivered=%v want %v", delivered, tc.wantDelivered)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if w.flushes != tc.wantFlushes {
				t.Fatalf("flushes=%d want %d", w.flushes, tc.wantFlushes)
			}
		})
	}
}
