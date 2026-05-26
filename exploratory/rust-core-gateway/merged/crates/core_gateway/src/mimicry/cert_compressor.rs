//! HUAKAI W11-F §14b.2 + §14c: Certificate Compression (RFC 8879 / TLS ext
//! 27) for Chrome impersonation profiles (Gemini CLI 0.42.0 — per §13
//! cloudcode-pa.googleapis.com capture).
//!
//! Two responsibilities:
//!
//! 1. **Advertise** algorithm IDs in the ClientHello ext 27 wire so the
//!    JA3/JA4 fingerprint matches Chrome's. Boring's `add_certificate_
//!    compression_algorithm` accepts any type implementing
//!    `CertificateCompressor` and uses its `ALGORITHM` const for the
//!    advertisement.
//! 2. **Decompress** the server's compressed certificate chain if the
//!    server picks our algorithm at handshake time. §14c (Owner-approved
//!    2026-05-26) adds real brotli decompression via the `brotli` crate
//!    (BSD-3-Clause / MIT, MIT-compatible) gated under `mimicry-boring`.
//!
//! Clean-room note: this module is original HUAKAI code. It depends on the
//! public `CertificateCompressor` trait from the vendored boring crate
//! (Apache-2.0, mod.rs:4598). The `decompress` body is one line calling
//! `brotli::BrotliDecompress` — a thin shim over the publicly-documented
//! crate API, not copied from any reference project. Sibling test code in
//! `vendor/boring/boring/src/ssl/test/cert_compressor.rs` uses the same
//! crate API the same way because there's only one sensible way to call it.

#[cfg(feature = "mimicry-boring")]
use boring::ssl::{CertificateCompressionAlgorithm, CertificateCompressor};

/// HUAKAI brotli cert-compression advertiser + decompressor for Chrome
/// impersonation. Implements boring's `CertificateCompressor` trait so
/// `SslContextBuilder::add_certificate_compression_algorithm` accepts it.
/// On the wire this advertises algorithm 2 (brotli) in the ClientHello
/// cert_compression extension. If the server picks brotli to compress its
/// certificate chain, `decompress` runs the real brotli decoder.
///
/// `Send + Sync + 'static` requirements from the trait are satisfied
/// because this is a zero-sized type.
#[cfg(feature = "mimicry-boring")]
#[derive(Debug, Default, Clone, Copy)]
pub struct BrotliCompressor;

#[cfg(feature = "mimicry-boring")]
impl CertificateCompressor for BrotliCompressor {
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

    /// `true` lets boring advertise algorithm 2 in the ClientHello AND
    /// register the real `decompress` callback below so a server that
    /// picks brotli successfully completes the handshake.
    const CAN_DECOMPRESS: bool = true;

    /// §14c: real brotli decompression. The `brotli` crate's
    /// `BrotliDecompress` consumes a `Read` and writes plaintext to a
    /// `Write`. We wrap `input` in a `Cursor` because boring hands us a
    /// borrowed byte slice, not a `Read`. Errors from the brotli decoder
    /// (truncated stream, invalid metadata, etc.) propagate through `?`
    /// and surface to the BoringSSL handshake layer which fails the
    /// connection safely. No panic on malformed input.
    fn decompress<W>(&self, input: &[u8], output: &mut W) -> std::io::Result<()>
    where
        W: std::io::Write,
    {
        brotli::BrotliDecompress(&mut std::io::Cursor::new(input), output)
    }
}
