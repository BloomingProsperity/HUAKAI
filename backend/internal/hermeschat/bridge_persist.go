package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func (b *Bridge) persistDone(ctx context.Context, prepared PreparedRequest, state *streamState, doneData []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("persist hermes message panic: %v", recovered)
		}
	}()
	conversationID := prepared.ConversationID
	if conversationID <= 0 {
		return fmt.Errorf("%w: conversation_id is required", hermes.ErrInvalidInput)
	}
	content, err := json.Marshal(map[string]string{"text": state.assistantText.String()})
	if err != nil {
		return err
	}
	now := b.now().UTC()
	completedAt := pgtype.Timestamptz{Time: now, Valid: true}
	err = b.tx.RunHermesTx(ctx, func(store hermes.Store) error {
		_, err := store.AppendMessage(ctx, dbhermes.AppendMessageParams{
			TenantID: prepared.TenantID, ConversationID: conversationID, Role: "assistant",
			Content: content, TokenCount: totalTokensFromDone(doneData), CompletedAt: completedAt,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: conversation is not active", hermes.ErrGone)
			}
			return fmt.Errorf("append hermes message: %w", err)
		}
		rows, err := store.UpdateConversationLastMessageAt(ctx, dbhermes.UpdateConversationLastMessageAtParams{
			Ts: completedAt, ID: conversationID, TenantID: prepared.TenantID,
		})
		if err != nil {
			return fmt.Errorf("touch hermes conversation: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: conversation is not active", hermes.ErrGone)
		}
		return b.recordMessageAudit(ctx, store, prepared, conversationID, now)
	})
	if err != nil {
		return err
	}
	return nil
}
