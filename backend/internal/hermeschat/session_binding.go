package hermeschat

import (
	"sync"
	"time"
)

// session_binding.go solves the core safety problem of the conversational
// read-only tool loop (WAVE H3b): the runner's internal_token carries only
// tenant|user|request_id|exp — NOT the operator's admin role or token id. The
// RBAC role floor and the operator-attribution audit row both need those.
//
// Rather than widening the internal_token (which would change the runner
// contract + the Python verifier and enlarge the trust surface), the gateway
// keeps an in-process binding keyed by the request_id that is ALREADY inside the
// internal_token. PrepareRequest mints the request_id and the matching
// internal_token in the same step; startChat binds the operator identity to that
// request_id BEFORE the runner is invoked. When the runner calls the internal
// read-only tool-execute endpoint, it presents the internal_token (proving it is
// the runner for THIS session); the endpoint verifies the token, extracts the
// request_id, and resolves the bound operator identity here.
//
// The token thus authenticates the SESSION; the binding supplies the OPERATOR's
// role + scope. The binding is fail-closed: no binding (or an expired one) means
// the internal call is rejected — a tool can never run without a resolved
// operator identity, and therefore can never exceed the operator's role floor or
// tenant scope.

// SessionOperator is the operator identity bound to one chat session. It mirrors
// the admin actor the H3 HTTP path derives from the admin token, so the internal
// tool-execute path enforces the SAME role floor + tenant scope + audit
// attribution as the explicit operator-driven endpoint.
type SessionOperator struct {
	// TenantID is the session's scope-checked tenant (the tenant the operator was
	// already authorized for by the H1 middleware's CanIssueForTenant). Every
	// conversational tool call is pinned to this tenant — a tool can never read
	// another tenant's data through the session.
	TenantID int64
	// ActorUserID is the tenant user whose Hermes ops context the operator acts
	// within (the ?as_user_id from the H1 middleware). Recorded as the tool-call
	// actor_user_id so existing tenant-isolation semantics carry over.
	ActorUserID int64
	// AdminActorTokenID is the operator's admin_tokens row id, recorded as the
	// tool-call admin_actor_token_id for operator attribution. 0 means no admin
	// actor was bound (the binding is then rejected — the conversational path is
	// admin-only).
	AdminActorTokenID int64
	// Role is the operator's admin role (platform_admin / tenant_operator). It is
	// the RBAC role floor checked against each tool's RequiredRole. An empty role
	// fails every tool (roleRank 0).
	Role string
	// ExpiresAt bounds the binding's lifetime so a leaked request_id cannot be
	// replayed indefinitely. Set to the internal_token's expiry so the binding and
	// the token expire together.
	ExpiresAt time.Time
}

// SessionBindings is an in-process, request_id-keyed store of operator
// identities for active chat sessions. It is concurrency-safe and self-pruning
// on lookup/insert. A single gateway process owns both the chat request that
// creates the binding and the internal tool-execute callback that reads it
// (the runner calls back to the SAME gateway via internal_base_url), so an
// in-memory store is sufficient and avoids persisting operator identity.
type SessionBindings struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]SessionOperator
}

// NewSessionBindings builds an empty binding store. now defaults to UTC wall
// clock; tests inject a fixed clock.
func NewSessionBindings(now func() time.Time) *SessionBindings {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SessionBindings{now: now, m: make(map[string]SessionOperator)}
}

// Bind records the operator identity for a session request_id. It overwrites any
// prior binding for the same request_id (request_ids are unique per chat start,
// so a collision means a re-prepared request — last write wins). A blank
// request_id is ignored (the caller validated it upstream; this is defense in
// depth so a blank key can never match a blank-keyed lookup).
func (s *SessionBindings) Bind(requestID string, op SessionOperator) {
	if s == nil || requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.m[requestID] = op
}

// Lookup returns the operator bound to requestID and whether it was present AND
// unexpired. An expired binding is treated as absent (and removed) so a stale
// session can never authorize a tool. A blank request_id never matches.
func (s *SessionBindings) Lookup(requestID string) (SessionOperator, bool) {
	if s == nil || requestID == "" {
		return SessionOperator{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.m[requestID]
	if !ok {
		return SessionOperator{}, false
	}
	if !op.ExpiresAt.IsZero() && !s.now().UTC().Before(op.ExpiresAt.UTC()) {
		delete(s.m, requestID)
		return SessionOperator{}, false
	}
	return op, true
}

// Release removes the binding for requestID. startChat calls it when the stream
// finishes so a binding does not outlive its session even before expiry.
func (s *SessionBindings) Release(requestID string) {
	if s == nil || requestID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, requestID)
}

// pruneLocked drops expired bindings. Called under the lock on insert so the map
// cannot grow without bound from sessions whose Release was never reached (e.g.
// a dropped connection). O(n) but n is the count of concurrently-active chats.
func (s *SessionBindings) pruneLocked() {
	now := s.now().UTC()
	for k, op := range s.m {
		if !op.ExpiresAt.IsZero() && !now.Before(op.ExpiresAt.UTC()) {
			delete(s.m, k)
		}
	}
}
