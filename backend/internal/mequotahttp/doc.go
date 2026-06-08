// Package mequotahttp exposes the authenticated user's read-only quota status.
//
// v1 returns only cost_usd quota windows because the underlying store query is
// explicitly metric-filtered. Requests, tokens, and concurrency dimensions stay
// out of scope until they get their own read-only query and projection.
package mequotahttp
