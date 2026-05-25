//! W11-F F-1.d.1 (Owner-approved synthesis 2026-05-25): ProxyEngine
//! transport boundary. Behaviour-preserving wrapper around the existing
//! `GatewayHttpClient` (hyper-util Client) that ProxyEngine can hold
//! polymorphically.
//!
//! Why a wrapper now (synthesis §4.4 + C-2 winner)
//! ----------------------------------------------
//! F-1.e (HTTP/2 fork outbound client) will add a second transport variant
//! that drives the MIT `0x676e67/http2` fork directly over Boring TLS,
//! emitting a profile-specific SETTINGS / HEADERS sequence. Today everything
//! goes through hyper-util's default h2/h1 client. To avoid a hot-path
//! `if mimicry-http2-fork && profile.something` branch in `relay.rs` per
//! request, ProxyEngine stores a `GatewayTransport` and dispatches once at
//! call site. F-1.d.1 introduces the wrapper with a single `Hyper` variant
//! (no behaviour change); F-1.e adds the fork variant.
//!
//! Backwards compatibility
//! -----------------------
//! - All 6 `ProxyEngine::new*` constructors accept `GatewayHttpClient` and
//!   wrap internally — no caller signature changes.
//! - `ProxyEngine::http_client()` still returns `&GatewayHttpClient` via
//!   [`GatewayTransport::as_hyper`]; the only consumer (`GatewayState::http_client`
//!   at `lib.rs:165`) keeps working.
//! - The response body type at the public call site (`self.transport.request(...)`)
//!   remains `Response<hyper::body::Incoming>`; F-1.d.2 will widen it to a
//!   boxed body abstraction so the fork variant can plug in.
//!
//! Mutation discriminator (CLAUDE.md #14)
//! --------------------------------------
//! - Replacing `self.transport.request(req)` at the call site with the
//!   pre-refactor `self.client.request(req)` causes a compile error (field
//!   no longer exists); the compiler is the test that catches the mutation
//!   for this commit.
//! - If `GatewayTransport::request` ever silently returns Ok with an empty
//!   response body, the existing `proxy_engine::relay::tests::*` idle /
//!   downstream-write tests detect missing frames and go red (the body
//!   pump never sees data).

use axum::body::Body;
use bytes::Bytes;
use http::{Request, Response};
use http_body_util::BodyExt;
use http_body_util::combinators::BoxBody;

use super::http_client::GatewayHttpClient;

/// W11-F F-1.d.2: boxed error carrier so the boxed response body can be
/// returned uniformly across transport variants without leaking each
/// variant's concrete error type. `hyper_util::client::legacy::Error` is
/// re-boxed at the variant boundary into this trait-object alias.
pub type BoxBodyError = Box<dyn std::error::Error + Send + Sync + 'static>;

/// W11-F F-1.d.2: erased response body type that downstream relay code
/// consumes. Boxes the Hyper variant's `hyper::body::Incoming` here so
/// future F-1.e fork-h2 variant can also fit this alias.
pub type GatewayResponseBody = BoxBody<Bytes, BoxBodyError>;

/// W11-F F-1.d.1: outbound HTTP transport abstraction. Today carries only
/// the existing `hyper-util` client; F-1.e will add a `ForkH2` variant for
/// the MIT http2 fork outbound path.
#[derive(Clone)]
pub enum GatewayTransport {
    /// Backwards-compatible hyper-util Client wrapping the existing
    /// `GatewayHttpConnector` (`hyper-rustls` or `BoringTlsConnector`).
    Hyper(GatewayHttpClient),
}

// W11-F F-1.f: Debug required by Result::expect_err in tests that hold a
// `Result<GatewayTransport, _>`. Hand-rolled because GatewayHttpClient's
// inner connector (BoringTlsConnector / hyper-rustls HttpsConnector) does
// not always implement Debug uniformly across features, and we don't want
// to leak client internals into log/panic output anyway.
impl std::fmt::Debug for GatewayTransport {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Hyper(_) => f.debug_tuple("GatewayTransport::Hyper").field(&"<client>").finish(),
        }
    }
}

impl GatewayTransport {
    /// Wrap a pre-built hyper-util client. Used by [`super::build_transport`]
    /// and by `ProxyEngine` constructors during the F-1.d.1 backward-compat
    /// rewrap.
    pub fn from_hyper(client: GatewayHttpClient) -> Self {
        Self::Hyper(client)
    }

    /// Backward-compat accessor for [`ProxyEngine::http_client`]. Returns
    /// the inner hyper client when the transport is the `Hyper` variant.
    /// Panics if the transport is any other variant — this is intentional:
    /// once F-1.e lands the fork variant, this accessor must be migrated
    /// to a body-abstracted accessor (or removed). Panicking here makes
    /// the migration loud rather than silent.
    pub fn as_hyper(&self) -> &GatewayHttpClient {
        match self {
            Self::Hyper(c) => c,
        }
    }

    /// Drive an outbound request through whichever transport variant is
    /// active. Returns an unwaited Future so that `tokio::time::timeout`
    /// can wrap the entire request — the existing call site at
    /// `proxy_engine::mod::ProxyEngine::forward_endpoint` uses
    /// `time::timeout(upstream_response_timeout, self.transport.request(req))`.
    ///
    /// W11-F F-1.d.2: response body is BOXED into [`GatewayResponseBody`] at
    /// the variant boundary. The Hyper variant maps `hyper::body::Incoming`
    /// via `BodyExt::map_err` + `.boxed()` so downstream relay code sees a
    /// uniform body type across variants. F-1.e fork-h2 variant will perform
    /// the same boxing on `http2::client::RecvStream`.
    pub async fn request(
        &self,
        req: Request<Body>,
    ) -> Result<Response<GatewayResponseBody>, hyper_util::client::legacy::Error> {
        match self {
            Self::Hyper(client) => {
                let response = client.request(req).await?;
                let (parts, body) = response.into_parts();
                let boxed: GatewayResponseBody = body
                    .map_err(|err| Box::new(err) as BoxBodyError)
                    .boxed();
                Ok(Response::from_parts(parts, boxed))
            }
        }
    }
}
