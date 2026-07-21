package audiohttp

import (
	"bytes"
	"context"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// remapAudioRegistry 把请求别名解析到一个不同的上游 ProviderModelID（模拟别名重映射）。
type remapAudioRegistry struct{ upstream string }

func (r remapAudioRegistry) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "audio/" + model,
		ProviderModelID:  r.upstream,
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"audio", audioTranscriptionCapability},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

// 守 P1（端到端）：别名重映射时，上游真正收到的 multipart "model" 字段必须是解析后的上游 id，
// 不是客户端别名，且文件保留。请求用 whisper-1（命中 req.Model 定价候选过定价），registry 把它
// 重映射到不同上游 id whisper-1-upstream。Mutation: dispatchAndSettle 不调 relaybody.RewriteModel
// -> 上游 body 里还是 "whisper-1" -> 本断言红。
func TestAudioTranscriptions_RewritesModelToUpstreamOnRemap(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{status: 200, body: `{"text":"ok"}`})
	env.deps.Registry = remapAudioRegistry{upstream: "whisper-1-upstream"}
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(8000, 8000), map[string]string{"model": "whisper-1"})
	rec := env.invokeMultipart(t, body, contentType)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mt, params, err := mime.ParseMediaType(env.transport.contentType)
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("upstream content-type not multipart: %q", env.transport.contentType)
	}
	r := multipart.NewReader(strings.NewReader(env.transport.body), params["boundary"])
	gotModel, gotFile := "", false
	for {
		p, e := r.NextPart()
		if e != nil {
			break
		}
		if p.FormName() == "model" {
			b := new(bytes.Buffer)
			_, _ = b.ReadFrom(p)
			gotModel = b.String()
		}
		if p.FormName() == "file" {
			gotFile = true
		}
	}
	if gotModel != "whisper-1-upstream" {
		t.Fatalf("upstream model=%q want whisper-1-upstream (client alias not rewritten to upstream id)", gotModel)
	}
	if !gotFile {
		t.Fatal("file part lost during model rewrite")
	}
}
