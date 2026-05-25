package channelhealth

import (
	"context"
	"encoding/json"
	"fmt"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

func NewAlertDLQHandler(store Store) obsdlq.Handler {
	return func(ctx context.Context, ev obsdlq.OutboxEvent) error {
		if store == nil {
			return errorsStoreNotConfigured()
		}
		var alert Alert
		if err := json.Unmarshal(ev.Payload, &alert); err != nil {
			return fmt.Errorf("channelhealth: decode alert payload: %w", err)
		}
		if alert.Key.TenantID <= 0 {
			alert.Key.TenantID = ev.TenantID
		}
		if alert.Key.ChannelID == "" {
			alert.Key.ChannelID = alert.Key.StableChannelID()
		}
		return store.AppendAlert(ctx, alert)
	}
}

func errorsStoreNotConfigured() error {
	return fmt.Errorf("channelhealth: store not configured")
}
