package hermes

import (
	"strings"
	"testing"
)

func TestListConversationsByOwnerSQLHasTenantOwnerAndActiveFilters(t *testing.T) {
	// Regression: deleting owner_user_id or deleted_at filters leaks cross-owner or deleted conversation rows.
	for _, required := range []string{
		"WHERE tenant_id = $1::bigint",
		"AND owner_user_id = $2::bigint",
		"AND deleted_at IS NULL",
	} {
		if !strings.Contains(listConversationsByOwner, required) {
			t.Fatalf("ListConversationsByOwner SQL missing %q:\n%s", required, listConversationsByOwner)
		}
	}
}

func TestListMessagesByConversationSQLHasOwnerAndActiveConversationJoin(t *testing.T) {
	// Regression: message history must join the parent conversation with owner and deleted filters, not only tenant/conversation ids.
	for _, required := range []string{
		"INNER JOIN hermes_conversations c",
		"AND c.deleted_at IS NULL",
		"AND c.owner_user_id = $3::bigint",
	} {
		if !strings.Contains(listMessagesByConversation, required) {
			t.Fatalf("ListMessagesByConversation SQL missing %q:\n%s", required, listMessagesByConversation)
		}
	}
}

func TestAppendMessageSQLRequiresActiveParentConversation(t *testing.T) {
	// Regression: stream completion after DELETE must not insert messages into a soft-deleted conversation.
	for _, required := range []string{
		"FROM hermes_conversations c",
		"c.deleted_at IS NULL",
	} {
		if !strings.Contains(appendMessage, required) {
			t.Fatalf("AppendMessage SQL missing %q:\n%s", required, appendMessage)
		}
	}
}

func TestUpdateConversationLastMessageAtSQLRequiresActiveConversation(t *testing.T) {
	// Regression: stream completion after DELETE must not touch last_message_at on a soft-deleted conversation.
	if !strings.Contains(updateConversationLastMessageAt, "AND deleted_at IS NULL") {
		t.Fatalf("UpdateConversationLastMessageAt SQL missing deleted_at guard:\n%s", updateConversationLastMessageAt)
	}
}
