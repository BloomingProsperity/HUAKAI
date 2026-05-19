# 2026-05-17 Wave R-4: L3 F-FP-001 Device Fingerprint Rust Impl — Claude

| 字段 | 内容 |
|---|---|
| Spec 锚 | docs/specs/device-fingerprint-binding.md |
| 前置 | R-3-A-fix (boring fork) 闭环, byte-level wire 4 vendor PASS |
| 闭环目标 | Rust core_gateway 加 device fingerprint binding 层 — 每 client 真模拟稳定 device fingerprint (Canvas / WebGL / fonts / audio context / TZ / locale / WebRTC IP), 防 anti-bot detect "脚本伪装" |
| 派工 | Claude plan (反代敏感); codex 实施 Rust 代码 |
| 估时 | 8-12 hr codex (4 sub-phase) |

---

## Sub-phase 拆分

### R-4-A (2-3 hr): 新 mimicry/fingerprint_profile.rs + DeviceFingerprint struct

- `DeviceFingerprint` struct: canvas_hash / webgl_vendor / webgl_renderer / fonts_set / audio_fingerprint / timezone / locale / screen_resolution / hardware_concurrency / device_memory / webrtc_local_ip
- per-vendor profile JSON 加 `device` field (Anthropic / Codex CLI / Kiro / Gemini / 新 3 vendor — 跟 R-3-B 协同)
- HUAKAI 自写 schema, 不读 fingerprintjs / browser-fingerprint-aggregator / undetected-chromedriver 等参考 lib source

### R-4-B (2 hr): mimicry/http_profile.rs 扩展 + 注入 device-related headers

- sec-ch-ua-full-version-list (UA Client Hints)
- sec-ch-ua-platform / sec-ch-ua-platform-version
- sec-ch-ua-arch / sec-ch-ua-bitness / sec-ch-ua-wow64
- Accept-Language (从 profile.device.locale 派生)
- 注入到 R-2-B-3 build_http_client_with_profile 出的 client

### R-4-C (2-3 hr): 客户端 fingerprint stability binding

- 每 client (per HUAKAI request) 绑定一个稳定 DeviceFingerprint (跨多 request stable, 同一 session 永不变)
- 用 HUAKAI request_id → fingerprint hash seed → deterministic select (从 profile.device 池里取一)
- 防 fingerprint rotation 太频繁 (anti-bot 识别成"脚本")

### R-4-D (1-2 hr): 单测 + e2e mock

- assert headers 命中 (sec-ch-ua-* 真出)
- assert 同 request_id 多次返同 fingerprint (stability)
- assert 跨 request_id fingerprint 不同 (variation)

---

## Risks

| 编号 | 类型 | 严重度 | 描述 | Mitigation |
|---|---|---|---|---|
| R-FP-001 | algorithm | MED | 各 vendor 真采样 device fingerprint 数据缺 (需 Owner 本机 fingerprint-collector 跑 Anthropic claude.ai/Codex CLI device 信息) | 默认用 commonly-leaked Chrome 137 desktop linux 模板; Owner R-3-B 同步采样真 device |
| R-FP-002 | maint | MED | sec-ch-ua-* header 跟 Chrome upgrade 同步 drift | 半年 review profile.device, 检 chrome-platform-status |

## Verify Gate

- cargo test --features mimicry-boring --lib mimicry::fingerprint PASS
- AT-FP-001-001..006 (新, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

## 不动

- frontend / Go / LICENSE / 计费
- R-2-B / R-3-A-fix vendor/boring/ 子树
- R-3 dispatch 已写 (本 wave 加新 module, 不改 dispatch)

Plan: Claude Opus 4.7 直写, 反代敏感 spec
UTC: 2026-05-17T~13:00:00Z
