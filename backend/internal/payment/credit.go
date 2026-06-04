package payment

// 旧 Slice-A 的 user_balances 直接入账实现已迁入 store_postgres.go:
// 新路径先写 payment_credits + billing_events(payment_credited), 再同步 legacy
// user_balances 缓存以兼容 balance hold/quota enforcement。
