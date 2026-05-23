package credentialstore

const (
	CredentialEventCreated          = "credential_created"
	CredentialEventRotated          = "credential_rotated"
	CredentialEventDeleted          = "credential_deleted"
	CredentialEventResolved         = "credential_resolved"
	CredentialEventRefreshSucceeded = "credential_refresh_succeeded"
	CredentialEventRefreshFailed    = "credential_refresh_failed"

	CredentialEventStateActivated = "credential_state_activated"
	CredentialEventStateDisabled  = "credential_state_disabled"
	CredentialEventStateRevoked   = "credential_state_revoked"
	CredentialEventStateAttention = "credential_state_attention"
	CredentialEventStateChanged   = "credential_state_changed"
)

// actionForStateTransition 将旧的单一 credential_disabled 审计拆成状态动作事件。
// payload 仍记录 old_state/new_state, 让 operator 同时能按事件分类检索并复核迁移前后状态。
func actionForStateTransition(_, newState string) string {
	switch Normalize(newState) {
	case StateActive:
		return CredentialEventStateActivated
	case "disabled", StateTempUnschedulable:
		return CredentialEventStateDisabled
	case StateRevoked:
		return CredentialEventStateRevoked
	case StateOperatorAttention:
		return CredentialEventStateAttention
	default:
		return CredentialEventStateChanged
	}
}
