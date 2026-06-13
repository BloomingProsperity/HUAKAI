package hermeschat

import (
	"testing"
	"time"
)

func TestSessionBindingExpiryIsFailClosed(t *testing.T) {
	// Regression (FAIL-CLOSED): an expired binding must be treated as absent so a
	// leaked request_id cannot be replayed after the session window. Mutation: if
	// Lookup ignored ExpiresAt, the stale binding would still authorize. We bind
	// with an expiry, advance the clock past it, and assert Lookup misses.
	now := time.Unix(1700000000, 0).UTC()
	clock := func() time.Time { return now }
	b := NewSessionBindings(clock)

	exp := now.Add(2 * time.Minute)
	b.Bind("req-exp", SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 9, Role: "platform_admin", ExpiresAt: exp})

	if _, ok := b.Lookup("req-exp"); !ok {
		t.Fatalf("binding missing before expiry")
	}
	// Advance past expiry.
	now = exp.Add(time.Second)
	if _, ok := b.Lookup("req-exp"); ok {
		t.Fatalf("expired binding still resolved — replay window not closed")
	}
}

func TestSessionBindingReleaseRemovesBinding(t *testing.T) {
	// Regression: Release must drop the binding so it cannot outlive its session
	// even before expiry. Mutation: if Release were a no-op, a finished session's
	// request_id would remain usable until expiry.
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	b.Bind("req-rel", SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 9, Role: "platform_admin", ExpiresAt: clock().Add(time.Minute)})
	b.Release("req-rel")
	if _, ok := b.Lookup("req-rel"); ok {
		t.Fatalf("binding survived Release")
	}
}

func TestSessionBindingBlankRequestIDNeverMatches(t *testing.T) {
	// Regression: a blank request_id must never bind or match — defense in depth so
	// a blank-keyed lookup cannot collide with a blank-keyed bind.
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	b.Bind("", SessionOperator{TenantID: 7, Role: "platform_admin", ExpiresAt: clock().Add(time.Minute)})
	if _, ok := b.Lookup(""); ok {
		t.Fatalf("blank request_id matched a binding")
	}
}
