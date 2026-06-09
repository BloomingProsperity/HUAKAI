package mimicry

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
)

func brResp(t *testing.T, body, enc string) *http.Response {
	t.Helper()
	var b bytes.Buffer
	if enc == "br" {
		w := brotli.NewWriter(&b)
		_, _ = w.Write([]byte(body))
		_ = w.Close()
	} else {
		b.WriteString(body)
	}
	h := http.Header{}
	if enc != "" {
		h.Set("Content-Encoding", enc)
	}
	h.Set("Content-Length", "999")
	return &http.Response{
		StatusCode:    200,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(b.Bytes())),
		ContentLength: 999,
	}
}

func TestDecodeMimicryResponse_DecodesBrAndStripsHeaders(t *testing.T) {
	const payload = "anti-ban AE chain decode test body"
	resp := decodeMimicryResponse(brResp(t, payload, "br"))
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// MUTATION GUARD: if decode is skipped, got is the raw brotli bytes != payload -> red.
	if string(got) != payload {
		t.Fatalf("decoded=%q want %q", string(got), payload)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding must be stripped after decode, got %q", resp.Header.Get("Content-Encoding"))
	}
	if resp.Header.Get("Content-Length") != "" || resp.ContentLength != -1 {
		t.Fatalf("Content-Length must be cleared after decode")
	}
	if !resp.Uncompressed {
		t.Fatal("Uncompressed must be true after decode")
	}
}

func TestDecodeMimicryResponse_PassthroughUnsupported(t *testing.T) {
	const payload = "plain body not compressed"
	resp := decodeMimicryResponse(brResp(t, payload, "")) // no Content-Encoding
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != payload {
		t.Fatalf("passthrough got=%q want %q", string(got), payload)
	}
	// nil-safe
	if decodeMimicryResponse(nil) != nil {
		t.Fatal("nil response must pass through")
	}
}
