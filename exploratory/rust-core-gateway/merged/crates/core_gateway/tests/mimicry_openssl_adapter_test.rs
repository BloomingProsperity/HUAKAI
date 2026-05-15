#![cfg(feature = "mimicry-openssl")]

// L2-A4 smoke: 只验证 OpenSSL client adapter 能完成本地 TLS 握手。

use std::{
    net::{SocketAddr, TcpListener, TcpStream as StdTcpStream},
    thread,
    time::{Duration, Instant},
};

use core_gateway::mimicry::openssl_adapter::OpenSslMimicryAdapter;
use openssl::{
    asn1::Asn1Time,
    bn::{BigNum, MsbOption},
    hash::MessageDigest,
    nid::Nid,
    pkey::{PKey, Private},
    rsa::Rsa,
    ssl::{SslAcceptor, SslMethod},
    x509::{
        X509, X509Builder, X509Name,
        extension::{BasicConstraints, ExtendedKeyUsage, KeyUsage, SubjectAlternativeName},
    },
};

const TEST_TIMEOUT: Duration = Duration::from_secs(10);

#[tokio::test]
async fn smoke_test_openssl_adapter_connects_to_local_tls_server() {
    let server = spawn_local_tls_server();
    let adapter = OpenSslMimicryAdapter::new_with_extra_trust_anchor(server.ca_certificate)
        .expect("OpenSSL adapter context 应能创建并加载测试 CA");

    let tls_stream = tokio::time::timeout(TEST_TIMEOUT, adapter.connect(server.addr, "localhost"))
        .await
        .expect("OpenSSL client handshake 不应超时")
        .expect("OpenSSL client 应能完成本地 TLS 握手");

    drop(tls_stream);
    server
        .join
        .join()
        .expect("TLS test server thread 不应 panic")
        .expect("TLS test server 应完成一次握手");
}

struct LocalTlsServer {
    addr: SocketAddr,
    ca_certificate: X509,
    join: thread::JoinHandle<Result<(), String>>,
}

fn spawn_local_tls_server() -> LocalTlsServer {
    let listener = TcpListener::bind("127.0.0.1:0").expect("本地 TLS listener 应能绑定");
    let addr = listener.local_addr().expect("本地 TLS listener 地址应存在");
    let certs = localhost_test_cert_chain().expect("本地 TLS 测试证书链应能创建");
    let ca_certificate = certs.ca_certificate.clone();

    let join = thread::spawn(move || {
        listener
            .set_nonblocking(true)
            .map_err(|error| error.to_string())?;
        let tcp_stream = accept_with_timeout(&listener)?;
        tcp_stream
            .set_read_timeout(Some(TEST_TIMEOUT))
            .map_err(|error| error.to_string())?;
        tcp_stream
            .set_write_timeout(Some(TEST_TIMEOUT))
            .map_err(|error| error.to_string())?;

        let acceptor = build_test_acceptor(&certs.server_private_key, &certs.server_certificate)?;
        let _tls_stream = acceptor
            .accept(tcp_stream)
            .map_err(|error| error.to_string())?;
        Ok(())
    });

    LocalTlsServer {
        addr,
        ca_certificate,
        join,
    }
}

fn accept_with_timeout(listener: &TcpListener) -> Result<StdTcpStream, String> {
    let started = Instant::now();
    loop {
        match listener.accept() {
            Ok((stream, _)) => return Ok(stream),
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                if started.elapsed() >= TEST_TIMEOUT {
                    return Err("TLS test server accept timed out".to_owned());
                }
                thread::sleep(Duration::from_millis(10));
            }
            Err(error) => return Err(error.to_string()),
        }
    }
}

fn build_test_acceptor(
    private_key: &PKey<Private>,
    certificate: &X509,
) -> Result<SslAcceptor, String> {
    let mut builder =
        SslAcceptor::mozilla_intermediate(SslMethod::tls()).map_err(|error| error.to_string())?;
    builder
        .set_private_key(private_key)
        .map_err(|error| error.to_string())?;
    builder
        .set_certificate(certificate)
        .map_err(|error| error.to_string())?;
    builder
        .check_private_key()
        .map_err(|error| error.to_string())?;
    Ok(builder.build())
}

struct TestCertChain {
    ca_certificate: X509,
    server_private_key: PKey<Private>,
    server_certificate: X509,
}

fn localhost_test_cert_chain() -> Result<TestCertChain, String> {
    let (ca_private_key, ca_certificate) = test_ca_cert()?;
    let server_private_key =
        PKey::from_rsa(Rsa::generate(2048).map_err(|error| error.to_string())?)
            .map_err(|error| error.to_string())?;

    let name = x509_name("localhost")?;
    let mut certificate = X509::builder().map_err(|error| error.to_string())?;
    certificate
        .set_version(2)
        .map_err(|error| error.to_string())?;
    certificate
        .set_subject_name(&name)
        .map_err(|error| error.to_string())?;
    certificate
        .set_issuer_name(ca_certificate.subject_name())
        .map_err(|error| error.to_string())?;
    set_validity_and_serial(&mut certificate)?;
    certificate
        .set_pubkey(&server_private_key)
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            BasicConstraints::new()
                .critical()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            KeyUsage::new()
                .critical()
                .digital_signature()
                .key_encipherment()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            ExtendedKeyUsage::new()
                .server_auth()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    let subject_alternative_name = SubjectAlternativeName::new()
        .dns("localhost")
        .build(&certificate.x509v3_context(Some(&ca_certificate), None))
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(subject_alternative_name)
        .map_err(|error| error.to_string())?;
    certificate
        .sign(&ca_private_key, MessageDigest::sha256())
        .map_err(|error| error.to_string())?;

    Ok(TestCertChain {
        ca_certificate,
        server_private_key,
        server_certificate: certificate.build(),
    })
}

fn test_ca_cert() -> Result<(PKey<Private>, X509), String> {
    let private_key = PKey::from_rsa(Rsa::generate(2048).map_err(|error| error.to_string())?)
        .map_err(|error| error.to_string())?;

    let name = x509_name("HUAKAI local TLS test CA")?;
    let mut certificate = X509::builder().map_err(|error| error.to_string())?;
    certificate
        .set_version(2)
        .map_err(|error| error.to_string())?;
    certificate
        .set_subject_name(&name)
        .map_err(|error| error.to_string())?;
    certificate
        .set_issuer_name(&name)
        .map_err(|error| error.to_string())?;
    set_validity_and_serial(&mut certificate)?;
    certificate
        .set_pubkey(&private_key)
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            BasicConstraints::new()
                .critical()
                .ca()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            KeyUsage::new()
                .critical()
                .key_cert_sign()
                .crl_sign()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .sign(&private_key, MessageDigest::sha256())
        .map_err(|error| error.to_string())?;

    Ok((private_key, certificate.build()))
}

fn x509_name(common_name: &str) -> Result<X509Name, String> {
    let mut name = X509Name::builder().map_err(|error| error.to_string())?;
    name.append_entry_by_nid(Nid::COMMONNAME, common_name)
        .map_err(|error| error.to_string())?;
    Ok(name.build())
}

fn set_validity_and_serial(certificate: &mut X509Builder) -> Result<(), String> {
    let not_before = Asn1Time::days_from_now(0).map_err(|error| error.to_string())?;
    certificate
        .set_not_before(&not_before)
        .map_err(|error| error.to_string())?;

    let not_after = Asn1Time::days_from_now(1).map_err(|error| error.to_string())?;
    certificate
        .set_not_after(&not_after)
        .map_err(|error| error.to_string())?;

    let mut serial = BigNum::new().map_err(|error| error.to_string())?;
    serial
        .rand(128, MsbOption::MAYBE_ZERO, false)
        .map_err(|error| error.to_string())?;
    let serial_asn1 = serial
        .to_asn1_integer()
        .map_err(|error| error.to_string())?;
    certificate
        .set_serial_number(&serial_asn1)
        .map_err(|error| error.to_string())
}
