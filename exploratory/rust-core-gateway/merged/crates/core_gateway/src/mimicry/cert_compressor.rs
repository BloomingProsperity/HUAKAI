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

#[cfg(all(test, feature = "mimicry-boring"))]
mod tests {
    use super::*;
    use boring::ssl::CertificateCompressor;

    /// W11-F §14c S1-4 (Codex review 2026-05-27): mutation-resistant unit
    /// test for `BrotliCompressor::decompress`. Verifies real brotli decode
    /// works on a known cert-chain-shaped payload.
    ///
    /// The test is **discriminating**: replacing the decompress body with
    /// `Ok(())` (silent no-op), or with `output.write_all(input)?; Ok(())`
    /// (raw passthrough — what a placeholder/stub would do), turns this
    /// test red because:
    /// - `Ok(())` produces empty output → assertion `decompressed.len() ==
    ///   plaintext.len()` fails.
    /// - passthrough produces compressed bytes as "decompressed" → the
    ///   payload mismatch assertion fails.
    ///
    /// Why a cert-chain-shaped payload (PEM-ish bytes with `0x30 0x82`
    /// DER header) instead of a tiny `b"hello"`: brotli has a minimum
    /// frame overhead and we want the test to exercise a payload size
    /// that's a closer analog to what a TLS server actually sends for a
    /// real cert chain (a few hundred bytes minimum). Using a contrived
    /// large-ish plaintext also exercises the decoder's streaming behavior
    /// rather than the trivial single-byte path.
    #[test]
    fn brotli_compressor_decompress_round_trip() {
        use brotli::enc::backward_references::BrotliEncoderParams;
        use std::io::Cursor;

        // ~440 bytes of "cert-chain-shaped" content: DER SEQUENCE header,
        // repeating subject DN bytes, varying so the encoder has real
        // work to do. We use a deterministic synthetic payload, not a
        // real cert, so the test is hermetic.
        let plaintext: Vec<u8> = {
            let mut v = Vec::with_capacity(440);
            v.extend_from_slice(&[0x30, 0x82, 0x01, 0xb0]); // SEQUENCE, len=432
            for i in 0..432u16 {
                // alternating block to ensure brotli isn't trivially
                // shrinking everything to constants.
                v.push((i as u8) ^ 0xa5);
            }
            v
        };

        // Compress with the brotli crate's encoder (same crate the
        // decompressor uses) so we know the round trip is closed.
        let mut compressed = Vec::new();
        let params = BrotliEncoderParams::default();
        brotli::BrotliCompress(
            &mut Cursor::new(&plaintext),
            &mut compressed,
            &params,
        )
        .expect("brotli encoder should compress a 440-byte synthetic payload");
        assert!(
            !compressed.is_empty(),
            "brotli encoder produced no output for non-empty input"
        );

        // Decompress with HUAKAI's CertificateCompressor impl.
        let mut decompressed = Vec::new();
        let compressor = BrotliCompressor;
        compressor
            .decompress(&compressed, &mut decompressed)
            .expect("BrotliCompressor::decompress should round-trip a brotli payload");

        // Discriminating assertions (length + byte equality).
        assert_eq!(
            decompressed.len(),
            plaintext.len(),
            "decompressed length must match plaintext; stub `Ok(())` returns empty"
        );
        assert_eq!(
            decompressed, plaintext,
            "decompressed bytes must equal plaintext; passthrough stub returns compressed bytes"
        );
    }

    /// W11-F §14c S1-4 (Codex review 2026-05-27): malformed brotli input
    /// must NOT panic and must surface as an `io::Error` so the BoringSSL
    /// handshake layer can fail the connection cleanly. This locks the
    /// "no panic on malformed input" promise the decompress doc comment
    /// makes — without this test, a future change that swapped
    /// `brotli::BrotliDecompress` for something that panics on bad input
    /// would slip through.
    #[test]
    fn brotli_compressor_decompress_rejects_malformed_input() {
        let garbage = [0xff_u8; 32]; // not a valid brotli stream
        let mut sink = Vec::new();
        let compressor = BrotliCompressor;
        let result = compressor.decompress(&garbage, &mut sink);
        assert!(
            result.is_err(),
            "decompress must reject malformed input; got Ok with sink.len()={}",
            sink.len()
        );
    }
}
