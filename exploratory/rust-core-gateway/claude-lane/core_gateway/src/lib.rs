// 库入口 — 将各模块暴露给集成测试和未来子 crate
// main.rs 是 binary 入口, 无法被外部引用; lib.rs 作为公共接口

pub mod config;
pub mod error;
pub mod request_id;
pub mod tracing_init;
