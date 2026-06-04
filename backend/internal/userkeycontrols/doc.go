// Package userkeycontrols contains session-scoped extensions for user-owned
// api_keys.
//
// This package never accepts, returns, logs, or queries bearer
// credential material. Ownership checks stay tied to the existing
// tenant_id/user_id gate.
package userkeycontrols
