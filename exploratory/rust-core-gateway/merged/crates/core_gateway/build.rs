fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../../proto/route.proto");

    let protoc = protoc_bin_vendored::protoc_bin_path()?;
    let mut prost_config = tonic_build::Config::new();
    prost_config.protoc_executable(protoc);

    tonic_build::configure()
        .build_client(true)
        .build_server(true)
        .skip_debug(".huakai.route.v1.RoutePlan")
        .skip_debug(".huakai.route.v1.UpstreamAuthMaterial")
        .skip_debug(".huakai.route.v1.AttemptReportRequest")
        // W11-A D-1b Phase 1: RouteQueryRequest 现含 client_credential (raw secret).
        // 必须手写 Debug 在 route_proto/redacting_debug.rs 渲染为 fingerprint, 不能
        // 让默认 derive(Debug) 打到 log/span/trace 把 raw credential 泄露 (A4 acceptance gate).
        .skip_debug(".huakai.route.v1.RouteQueryRequest")
        .bytes([
            ".huakai.route.v1.RoutePlan.acquisition_token",
            ".huakai.route.v1.UpstreamAuthMaterial.material",
            ".huakai.route.v1.AttemptReportRequest.acquisition_token",
        ])
        .compile_protos_with_config(prost_config, &["../../proto/route.proto"], &["../../proto"])?;

    Ok(())
}
