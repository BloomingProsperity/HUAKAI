//! HUAKAI W11-F §14b.2: minimal Certificate Compression (RFC 8879 / TLS ext
//! 27) advertisement for Chrome impersonation profiles (currently Gemini
//! Advanced 0.42.0 — per §13 capture cloudcode-pa.googleapis.com).
//!
//! The on-wire advertisement is what Chrome impersonation needs: ClientHello
//! ext 27 must list IANA cert-compression algorithm IDs in the same order
//! Chrome sends them. The actual decompression of server cert chains is a
//! separate concern — handled here as a stub that fails closed at the
//! handshake layer, with a `TODO §14c` to wire a real brotli decompressor
//! once Owner approves a runtime `brotli` crate dependency.
//!
//! Why a stub is acceptable scope for §14b.2:
//! - Chrome impersonation is judged on the ClientHello bytes (JA3, JA4,
//!   extension order). Server-to-client cert chain compression only
//!   matters if the server actually picks the algorithm.
//! - The wire-level acceptance test in `boring_wire.rs` runs an offline
//!   capture with a 3-second timeout that captures the ClientHello and
//!   discards the handshake outcome. Stub decompressor is sufficient.
//! - Real cloudcode-pa.googleapis.com production calls would need a real
//!   brotli decompressor (or the server would error). That's §14c.
//!
//! Clean-room note: this module is original HUAKAI code. It depends on the
//! public `CertificateCompressor` trait from the vendored boring crate
//! (Apache-2.0, mod.rs:4598) but does not copy any implementation —
//! `BrotliCompressor` in `vendor/boring/boring/src/ssl/test/cert_compressor.rs`
//! is dev-only test code; HUAKAI's stub here is a different shape (no
//! `brotli` crate dep, stub-only) implementing the same public trait.

#[cfg(feature = "mimicry-boring")]
use boring::ssl::{CertificateCompressionAlgorithm, CertificateCompressor};

/// HUAKAI stub brotli cert-compression advertiser. Implements the boring
/// `CertificateCompressor` trait so `SslContextBuilder::add_certificate_
/// compression_algorithm` accepts it, which causes boring to advertise
/// algorithm 2 (brotli) in the ClientHello cert_compression extension.
///
/// The decompress path always errors. Compression is not supported at all
/// (no client-side cert-chain compression makes sense for a CLIENT). The
/// `Send + Sync + 'static` requirements from the trait are satisfied
/// because this is a zero-sized type.
#[cfg(feature = "mimicry-boring")]
#[derive(Debug, Default, Clone, Copy)]
pub struct StubBrotliCompressor;

#[cfg(feature = "mimicry-boring")]
impl CertificateCompressor for StubBrotliCompressor {
    /// IANA "Certificate Compression Algorithms" registry, brotli = 2.
    /// boring exports the constant publicly as `BROTLI`; the bare tuple
    /// constructor is crate-private so we go through the named const.
    /// <https://www.iana.org/assignments/tls-parameters/tls-parameters.xhtml#tls-certificate-compression-algorithm-ids>
    const ALGORITHM: CertificateCompressionAlgorithm = CertificateCompressionAlgorithm::BROTLI;

    /// Clients don't compress their own cert chain (clients usually send no
    /// cert, and even when they do, RFC 8879 only describes the server side
    /// compressing for the client). `false` keeps boring from registering a
    /// compress callback.
    const CAN_COMPRESS: bool = false;

    /// `true` lets boring advertise algorithm 2 in the ClientHello. The
    /// default `decompress` impl in the trait returns an io::Error, which
    /// would cause the handshake to fail if a server actually picked this
    /// algorithm — that's expected for §14b.2 scope (offline wire test
    /// only). Real decompression is §14c.
    const CAN_DECOMPRESS: bool = true;

    // `compress` and `decompress` use the trait defaults: both return
    // `io::Error::other("not implemented")`. Boring won't call `compress`
    // because CAN_COMPRESS=false; it will only call `decompress` if a
    // server actually selects algorithm 2 in CertificateRequest, which the
    // offline wire test never reaches.
}
