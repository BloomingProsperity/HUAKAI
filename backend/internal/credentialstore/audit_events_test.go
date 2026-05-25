package credentialstore

import "testing"

func TestActionForStateTransitionDiscriminatesLifecycleActions(t *testing.T) {
	activated := actionForStateTransition(StateRevoked, StateActive)
	revoked := actionForStateTransition(StateActive, StateRevoked)
	if activated != CredentialEventStateActivated {
		t.Fatalf("revoked -> active event=%q want %q", activated, CredentialEventStateActivated)
	}
	if revoked != CredentialEventStateRevoked {
		t.Fatalf("active -> revoked event=%q want %q", revoked, CredentialEventStateRevoked)
	}
	if activated == revoked {
		t.Fatal("revoked -> active 与 active -> revoked 必须产生不同事件,否则固定 credential_disabled 的回归不会变红")
	}
}

func TestActionForStateTransitionMapsDisabledAttentionAndFallback(t *testing.T) {
	cases := []struct {
		name     string
		oldState string
		newState string
		want     string
	}{
		{name: "disabled literal", oldState: StateActive, newState: "disabled", want: CredentialEventStateDisabled},
		{name: "temp unschedulable represents disabled", oldState: StateActive, newState: StateTempUnschedulable, want: CredentialEventStateDisabled},
		{name: "operator attention", oldState: StateActive, newState: StateOperatorAttention, want: CredentialEventStateAttention},
		{name: "fallback state", oldState: StateActive, newState: StateNeedsRotation, want: CredentialEventStateChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionForStateTransition(tc.oldState, tc.newState); got != tc.want {
				t.Fatalf("actionForStateTransition(%q, %q)=%q want %q", tc.oldState, tc.newState, got, tc.want)
			}
		})
	}
}
