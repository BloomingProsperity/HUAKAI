// Package audit 负责面向用户的 CostReceipt 派生、签名与退款 worker 协调，
// 对齐 F-AUDIT-001 规格中的审计收据工作流。
//
// 职责边界：audit 消费 auditledger API 生成可展示的 receipt；
// auditledger 则提供 F-TRUST-001 规格下的 append-only 账本 schema、
// writer、reader、Merkle、公钥 registry 与 signer 基础设施。
package audit
