package config

import "testing"

func TestLoadIncludesTransportSidecarSocket(t *testing.T) {
	t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
	t.Setenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET", "/tmp/huakai-tls-sidecar.sock")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TransportSidecarSocket != "/tmp/huakai-tls-sidecar.sock" {
		t.Fatalf("TransportSidecarSocket=%q want env value", cfg.TransportSidecarSocket)
	}
}
