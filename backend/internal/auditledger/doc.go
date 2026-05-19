// Package auditledger 提供 append-only 账本基础设施，覆盖 entry、Merkle、
// 公钥 registry、signer、schema、writer 与 reader。
//
// 职责边界：auditledger 暴露 F-TRUST-001 规格下的账本 API；
// audit 包消费这些 API 派生面向用户的 CostReceipt 与相关 receipt。
package auditledger
