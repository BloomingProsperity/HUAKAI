# 2026-05-17 Wave R-6: L5 F-NET-001 Outbound IP Pool Rust Impl — Claude

| 字段 | 内容 |
|---|---|
| Spec 锚 | docs/specs/outbound-ip-pool.md |
| 前置 | R-5 pacing 闭环 (IP pool 跟 pacing 联动: 同 IP 短时多 request burst 触 IP 封) |
| 闭环目标 | Rust core_gateway 加 outbound IP pool 层 — 每 vendor request 选 IP (residential proxy / VPN / 多 NIC), 防 anti-bot 识别"同 IP 多 account" |
| 派工 | Claude plan (反代敏感); codex 实施 |
| 估时 | 8-12 hr codex (4 sub-phase) |

---

## Sub-phase 拆分

### R-6-A (2 hr): IpPoolConfig + provider abstraction

- IpPoolConfig: pool_type (residential / datacenter / direct), provider (e.g. brightdata / soax / smartproxy / multi-nic-local), rotation_policy (per-request / per-session / sticky-N-min)
- Trait `IpProvider`: `next_ip() -> SocketAddr` + `health_check()`
- 默认 impl: DirectIp (no proxy), MultiNicLocal (local NIC rotation per Linux SO_BINDTODEVICE)

### R-6-B (2-3 hr): per-vendor IP affinity policy

- per-Account / per-session IP 绑定 (避 hot-rotation 触 IP-based detect)
- 复用 R-3-A-fix sticky-session pattern (F-POOL-AFFINITY-001)
- IP unhealthy (timeout / 403) → demote + rotation; 不 silent fail

### R-6-C (2-3 hr): proxy provider impl (brightdata / smartproxy)

- Brightdata 协议: HTTP/SOCKS5 + auth header (HUAKAI 不读 brightdata SDK source, 按公开 protocol doc 自写)
- HUAKAI rotation IP 池 selector (跟 R-NEW-001 pasr 类似 score+blend)
- credential 安全: AES-GCM at rest (复用 F-AUTH-005 KeyProvider)

### R-6-D (2-3 hr): 单测 + 集成

- assert IP rotation 触发 (per policy)
- assert sticky-N-min 期间 IP 不变
- assert unhealthy IP 自动 demote

---

## Risks

| 编号 | 类型 | 严重度 | 描述 | Mitigation |
|---|---|---|---|---|
| R-NET-001 | cost | HIGH | residential proxy provider 按 GB 计费, 真启用后 cost 飙升 | per-tenant feature flag + 月度配额 + alert |
| R-NET-002 | legal | MED | 部分 proxy 在某地区违反 ToS | Owner 国家合规审核 + Personal Edition 禁用 SaaS proxy |

## Verify Gate

- cargo test --features mimicry-boring --lib mimicry::ip_pool PASS
- AT-NET-001-001..008

## 不动

- frontend / Go / LICENSE / 计费 (本 wave 不计 IP-based 计费, 后续 F-BILL 加)
- R-2-B / R-3-A-fix vendor/boring/

Plan: Claude Opus 4.7 直写, 反代敏感 spec
UTC: 2026-05-17T~13:10:00Z
