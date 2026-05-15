/// profile 推导出的底层传输后端意图；这里只做选择，不执行 dispatch。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BackendIntent {
    /// L2-A4 接入的 OpenSSL exact adapter。
    OpenSslAdapter,
    /// 当前 hyper-rustls 产线后端。
    Rustls,
    /// profile 已知存在 gap，禁止进入任何 dispatch。
    KnownGapBlocked { reason: String },
    /// 模板未声明可用 TLS backend，或声明值当前没有对应实现。
    UnsupportedTemplate { reason: String },
}
