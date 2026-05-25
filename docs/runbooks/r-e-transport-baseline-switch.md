# R-E Transport Baseline Switch Runbook

1. Personal / single-host deploy uses `transport_baseline = "uds"` and points `uds_socket_path` at the local Go control-plane socket.
2. SaaS / multi-host deploy uses `transport_baseline = "mtls"` with `HUAKAI_CONTROL_PLANE_ENDPOINT=https://host:port`.
3. For mTLS, provide client cert chain, client key, and CA cert file paths before startup; this lane does not generate or rotate certs.
4. Switch by changing the baseline config/env and restarting the Rust data-plane process.
5. If startup rejects the baseline or required cert paths, fix config first; do not fall back silently.
6. Keep UDS as the default until R-SEC-002 cert lifecycle and rollout gates are implemented.
