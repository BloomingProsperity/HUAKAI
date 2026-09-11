package chatpipe

import (
	"io"
	"net/http"
	"testing"
)

func TestWriteFullReportsPartialAndZeroWrites(t *testing.T) {
	partial := &countWriter{limit: 3, err: io.ErrClosedPipe}
	n, fully, err := WriteFull(partial, []byte("abcdef"))
	if fully || n != 3 || err != io.ErrClosedPipe {
		t.Fatalf("partial written/fully/err=%d/%v/%v", n, fully, err)
	}
	zero := &countWriter{limit: 0, err: io.ErrClosedPipe}
	n, fully, err = WriteFull(zero, []byte("abcdef"))
	if fully || n != 0 || err != io.ErrClosedPipe {
		t.Fatalf("zero written/fully/err=%d/%v/%v", n, fully, err)
	}
	ok := &countWriter{limit: -1}
	n, fully, err = WriteFull(ok, []byte("abcdef"))
	if !fully || n != 6 || err != nil {
		t.Fatalf("full written/fully/err=%d/%v/%v", n, fully, err)
	}
}

type countWriter struct {
	http.ResponseWriter
	header http.Header
	limit  int
	err    error
}

func (w *countWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *countWriter) WriteHeader(int) {}
func (w *countWriter) Write(p []byte) (int, error) {
	n := w.limit
	if n < 0 || n > len(p) {
		n = len(p)
	}
	return n, w.err
}
