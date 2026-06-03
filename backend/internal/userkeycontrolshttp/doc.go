// Package userkeycontrolshttp exposes session-scoped API key control endpoints.
//
// Routes must be mounted inside auth.SessionMiddleware. Handlers still
// fail closed when the session identity is absent so accidental public
// mounting cannot reach the service with zero tenant/user values.
package userkeycontrolshttp
