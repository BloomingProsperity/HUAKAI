// Package billingdsl parses and evaluates tiered billing expressions.
//
// CMB invariants: this package does not read credentials, does not log
// credentials, does not touch a database, and does not expose HTTP handlers.
package billingdsl
