package hermes

import (
	"strings"
	"testing"
)

func TestListConversationsByOwnerSQLHasTenantOwnerActorAndActiveFilters(t *testing.T) {
	// 回归：同一租户共用服务主体，列表必须继续按真实管理员隔离。
	for _, required := range []string{
		"WHERE conversation.tenant_id = $1::bigint",
		"AND conversation.owner_user_id = $2::bigint",
		"AND conversation.actor_source = $3::text",
		"AND conversation.actor_id = $4::bigint",
		"AND conversation.deleted_at IS NULL",
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
		"AND c.actor_source = $4::text",
		"AND c.actor_id = $5::bigint",
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
		"c.owner_user_id = $8::bigint",
		"c.actor_source = $9::text",
		"c.actor_id = $10::bigint",
		"c.deleted_at IS NULL",
	} {
		if !strings.Contains(appendMessage, required) {
			t.Fatalf("AppendMessage SQL missing %q:\n%s", required, appendMessage)
		}
	}
}

func TestUpdateConversationLastMessageAtSQLRequiresActorScopedActiveConversation(t *testing.T) {
	// 回归：流式落库既不能触碰已删除会话，也不能改到同租户另一管理员的会话。
	for _, required := range []string{
		"owner_user_id = $4::bigint",
		"actor_source = $5::text",
		"actor_id = $6::bigint",
		"AND deleted_at IS NULL",
	} {
		if !strings.Contains(updateConversationLastMessageAt, required) {
			t.Fatalf("UpdateConversationLastMessageAt SQL missing %q:\n%s", required, updateConversationLastMessageAt)
		}
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
