package hermes

import (
	"strings"
	"testing"
)

func TestListConversationsByOwnerSQLHasTenantOwnerAndActiveFilters(t *testing.T) {
	// 回归:删掉 owner_user_id 或 deleted_at 过滤会泄露跨 owner 或已删除的会话行。
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
	// 回归:消息历史必须在 join 父会话时带上 owner 和已删除过滤，而不仅仅是 tenant/conversation id。
	for _, required := range []string{
		"INNER JOIN hermes_conversations c",
		"AND c.deleted_at IS NULL",
		"AND c.owner_user_id = $3::bigint",
		"m.content_ciphertext",
	} {
		if !strings.Contains(listMessagesByConversation, required) {
			t.Fatalf("ListMessagesByConversation SQL missing %q:\n%s", required, listMessagesByConversation)
		}
	}
}

func TestAppendMessageSQLRequiresActiveParentConversation(t *testing.T) {
	// 回归:DELETE 之后的流式补全绝不能往已软删除的会话里插入消息。
	for _, required := range []string{
		"content_ciphertext",
		"FROM hermes_conversations c",
		"c.deleted_at IS NULL",
	} {
		if !strings.Contains(appendMessage, required) {
			t.Fatalf("AppendMessage SQL missing %q:\n%s", required, appendMessage)
		}
	}
}

func TestUpdateConversationLastMessageAtSQLRequiresActiveConversation(t *testing.T) {
	// 回归:DELETE 之后的流式补全绝不能触碰已软删除会话的 last_message_at。
	if !strings.Contains(updateConversationLastMessageAt, "AND deleted_at IS NULL") {
		t.Fatalf("UpdateConversationLastMessageAt SQL missing deleted_at guard:\n%s", updateConversationLastMessageAt)
	}
}

func TestPurgeMessagesBeforeSQLHardDeletesExpiredRows(t *testing.T) {
	// 回归:保留期清理必须是对过期消息行的真删除，而不是父级软删除或空操作扫描。
	for _, required := range []string{
		"DELETE FROM hermes_messages",
		"created_at < $1::timestamptz",
		"RETURNING id",
	} {
		if !strings.Contains(purgeMessagesBefore, required) {
			t.Fatalf("PurgeMessagesBefore SQL missing %q:\n%s", required, purgeMessagesBefore)
		}
	}
}
