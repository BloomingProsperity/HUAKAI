// 一次性 smoke (不入正式构建, 目录前缀 "_" go 工具会忽略)。
// 跑 HUAKAI CodexSessionAdapter + transport.Factory (mimicry / standard)
// 真发到 chatgpt.com, 配 tcpdump 抓 TLS handshake 验证 HUAKAI 出站
// 指纹是否符合 builtin codex-cli.json 期望。
//
// 用法:
//
//	./_smoke-codex-tls --mode=standard
//	./_smoke-codex-tls --mode=mimicry
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	poai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

type codexAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func loadAuth() (codexAuth, error) {
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(home + "/.codex/auth.json")
	if err != nil {
		return codexAuth{}, err
	}
	var a codexAuth
	if err := json.Unmarshal(raw, &a); err != nil {
		return codexAuth{}, err
	}
	if a.Tokens.AccessToken == "" {
		return codexAuth{}, fmt.Errorf("auth.json missing tokens.access_token")
	}
	return a, nil
}

func buildClient(mode, codexTemplate string) (*http.Client, error) {
	reg := mimicry.NewTemplateRegistry()
	// 仅加载 codex-cli.json 单文件, 避开 _pending-backfill 子目录里的 mode 冲突。
	tmpl, err := mimicry.LoadFromCollectorOutput(codexTemplate)
	if err != nil {
		return nil, fmt.Errorf("load codex template %s: %w", codexTemplate, err)
	}
	if err := reg.Register(mimicry.TransportMode(transport.TransportModeMimicryChatGPT), tmpl); err != nil {
		return nil, fmt.Errorf("register codex template: %w", err)
	}
	f := transport.NewFactory(reg)
	var tm transport.TransportMode
	switch mode {
	case "standard":
		tm = transport.TransportModeStandard
	case "mimicry":
		tm = transport.TransportModeMimicryChatGPT
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	rt, err := f.For(transport.ProviderOpenAICodex, tm)
	if err != nil {
		return nil, fmt.Errorf("factory.For(openai_codex, %s): %w", tm, err)
	}
	return &http.Client{Transport: rt, Timeout: 30 * time.Second}, nil
}

func main() {
	mode := flag.String("mode", "mimicry", "standard|mimicry")
	codexTemplate := flag.String("template", "/home/codex/HUAKAI/tools/fingerprint-collector/templates/codex-cli.json", "absolute path to codex-cli.json builtin")
	flag.Parse()

	auth, err := loadAuth()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load auth:", err)
		os.Exit(1)
	}

	adapter := &poai.CodexSessionAdapter{}
	body := []byte(`{"model":"gpt-5","instructions":"Reply with exactly: HUAKAI smoke ping.","input":[{"type":"text","text":"ping"}],"stream":false,"max_output_tokens":16}`)
	in := provider.BuildInput{
		UpstreamModelID: "gpt-5",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: auth.Tokens.AccessToken,
			Extra: map[string]string{
				"user_agent":    "codex_cli_rs/0.128.0 (linux; x86_64)",
				"oai_device_id": "huakai-smoke-test",
			},
		},
		Account: provider.AccountInfo{
			AccountID: 1,
			Platform:  "openai_codex",
		},
	}
	req, err := adapter.BuildRequest(context.Background(), in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build request:", err)
		os.Exit(2)
	}
	// 验证 Codex CLI 0.128.0 使用的 /responses 路径，而不是旧的 /completions 路径。
	if strings.Contains(req.URL.Path, "completions") {
		req.URL.Path = strings.Replace(req.URL.Path, "/completions", "/responses", 1)
	}

	client, err := buildClient(*mode, *codexTemplate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build client:", err)
		os.Exit(3)
	}

	fmt.Printf("[mode=%s] sending to %s\n", *mode, req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client.Do:", err)
		os.Exit(4)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	fmt.Printf("[mode=%s] status=%d, body_first=%s\n", *mode, resp.StatusCode, truncate(string(rb), 400))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
