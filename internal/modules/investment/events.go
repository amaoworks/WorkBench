package investment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"workbench/internal/contracts"
)

func (m *Module) priceAlert(ctx context.Context, event contracts.Event) error {
	if event.SchemaVersion != 1 {
		return errors.New("unsupported price event version")
	}
	var quote Quote
	if err := json.Unmarshal(event.Payload, &quote); err != nil {
		return err
	}
	if quote.ChangeBPS < 200 && quote.ChangeBPS > -200 {
		return nil
	}
	_, err := m.deps.Notifications.Create(ctx, contracts.NewNotification{SourceModule: "investment", Severity: contracts.NotificationInfo,
		Title: "模拟行情波动提醒", Content: fmt.Sprintf("%s（模拟数据）变动 %.2f%%", quote.Name, float64(quote.ChangeBPS)/100),
		ActionLabel: "查看模拟行情", ActionRoute: "/investment", SourceEventID: event.ID,
		IdempotencyKey: "investment:price:" + quote.Symbol + ":" + fmt.Sprint(quote.AsOf.UnixMilli())})
	return err
}
