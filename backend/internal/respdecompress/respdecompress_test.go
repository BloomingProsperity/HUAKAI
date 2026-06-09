package respdecompress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const payload = "the quick brown fox jumps over the lazy dog -- 0123456789 -- anti-ban AE chain"

func gzipBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(payload))
	_ = w.Close()
	return b.Bytes()
}

func deflateBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.DefaultCompression)
	_, _ = w.Write([]byte(payload))
	_ = w.Close()
	return b.Bytes()
}

func brotliBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := brotli.NewWriter(&b)
	_, _ = w.Write([]byte(payload))
	_ = w.Close()
	return b.Bytes()
}

func zstdBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w, _ := zstd.NewWriter(&b)
	_, _ = w.Write([]byte(payload))
	_ = w.Close()
	return b.Bytes()
}

func TestWrapDecodesEachEncoding(t *testing.T) {
	cases := []struct {
		enc  string
		data []byte
	}{
		{"gzip", gzipBytes(t)},
		{"deflate", deflateBytes(t)},
		{"br", brotliBytes(t)},
		{"zstd", zstdBytes(t)},
	}
	for _, c := range cases {
		rc, err := Wrap(io.NopCloser(bytes.NewReader(c.data)), c.enc)
		if err != nil {
			t.Fatalf("%s: Wrap err: %v", c.enc, err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("%s: read err: %v", c.enc, err)
		}
		// MUTATION GUARD: if Wrap returns the raw compressed body instead of a
		// decoder for this encoding, got != payload -> red.
		if string(got) != payload {
			t.Fatalf("%s: decoded=%q want %q", c.enc, string(got), payload)
		}
	}
}

func TestWrapPassthroughUnknownAndIdentity(t *testing.T) {
	for _, enc := range []string{"", "identity", "compress", "weird"} {
		raw := []byte("raw-not-compressed")
		rc, err := Wrap(io.NopCloser(bytes.NewReader(raw)), enc)
		if err != nil {
			t.Fatalf("%q: unexpected err: %v", enc, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(got) != string(raw) {
			t.Fatalf("%q: passthrough got=%q want %q", enc, string(got), string(raw))
		}
		if Supported(enc) {
			t.Fatalf("%q: Supported should be false", enc)
		}
	}
	for _, enc := range []string{"gzip", "br", "zstd", "deflate"} {
		if !Supported(enc) {
			t.Fatalf("%q: Supported should be true", enc)
		}
	}
}
