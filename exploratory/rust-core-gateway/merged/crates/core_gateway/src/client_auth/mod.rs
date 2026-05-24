//! W11-A D-1b Phase 1 (2026-05-24): client authentication boundary.
//!
//! 责任 (Codex round 1 P1 finding 2026-05-24 fix: 子模块化保 src/ root ≤ 20):
//! - `credential` 子模块: 从 HTTP headers 派生 `ClientCredential` (Bearer / x-api-key),
//!   产 canonical proto value + SHA-256 fingerprint (A4 acceptance gate).
//! - `manual_first` 子模块: Phase 1 静态 hash → tenant 兜底 (Owner §7-J 默认仅 mock/
//!   staging/internal-smoke, 生产模式启动时若 enabled=true 必 fail-fast)。
//!
//! 不持身份权威 (β scheme, synthesis §7-H Owner 2026-05-23 已决):
//! - Rust 永不读 `x-tenant-id` header (A3 acceptance gate, mutation-tested in
//!   `account_planner.rs`).
//! - 凭据 opaque 转 control plane; 由 Go control plane 派生权威 tenant (Phase 2 上线后)。
//!
//! Phase 演进 (synthesis §4.5):
//! - Phase 1: Rust 写 client_credential 字段, Go 控制面忽略 (本 commit)。
//! - Phase 2: Go 控制面消费 + 双写对账 (待 Owner 启动 Go 线 spec)。
//! - Phase 3: Manual First 永久下线; 旧 tenant_id 路径删除。

mod credential;
mod manual_first;

pub use credential::{
    ClientCredential, ClientCredentialError, ClientCredentialFingerprint, ClientCredentialKind,
};
pub use manual_first::{
    ManualFirstConfig, ManualFirstEntry, ManualFirstError, ManualFirstResolver,
};

/// RouteIdentity: 把 listener 解析出的客户端凭据 + Manual First 派生 tenant 一起
/// 透给 account_planner.build_route_query 写入 RouteQueryRequest。
///
/// 不含 raw `x-tenant-id` header (A3 acceptance gate: 永不被信任)。
///
/// `client_credential = None` 表示 anonymous (dev/test 模式默认允许, 见 D-11):
/// listener 在 `require_credential=false` 时遇到无 Authorization/x-api-key 头会构造此变体,
/// `as_route_proto_value()` 在 None 时返回空字符串, 与 Phase 1 `String::new()` 兜底等价。
#[derive(Clone)]
pub struct RouteIdentity {
    /// 已解析的客户端凭据 (Authorization: Bearer ... 或 x-api-key); None = anonymous。
    pub client_credential: Option<ClientCredential>,
    /// Manual First resolver 命中的 tenant (Phase 1 兜底)。
    /// `None` 表示 Manual First flag OFF 或 map 未命中 → tenant_id 写空, 强制 Go 派生。
    pub manual_first_tenant_id: Option<String>,
}

impl RouteIdentity {
    /// 把 client_credential 渲染成 RouteQueryRequest.client_credential (proto string 字段)。
    /// `Some(cred)` → `"bearer:<token>"` / `"x-api-key:<key>"`;
    /// `None` → 空字符串 (Phase 1 anonymous tenant)。
    pub fn client_credential_proto_value(&self) -> String {
        self.client_credential
            .as_ref()
            .map(|c| c.as_route_proto_value())
            .unwrap_or_default()
    }
}

impl std::fmt::Debug for RouteIdentity {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        // A4 acceptance gate: raw credential 永不入 Debug 渲染 (ClientCredential::Debug
        // 已 override 为 fingerprint-only, 此处 forward)。
        f.debug_struct("RouteIdentity")
            .field("client_credential", &self.client_credential)
            .field("manual_first_tenant_id", &self.manual_first_tenant_id)
            .finish()
    }
}
