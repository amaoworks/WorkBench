package investment

import (
	"context"
	"errors"
	"fmt"
	"time"

	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

func (g *futuGateway) kline(ctx context.Context, symbol, resolution string, fromMs, toMs int64) ([]futuBar, error) {
	klType, subType, ok := futuKLTypes(resolution)
	if !ok {
		return []futuBar{}, nil
	}
	client, err := g.ensureOverlay(ctx)
	if err != nil {
		return nil, err
	}
	if err := g.hold(ctx, client, symbol, subTypeBasic, true); err != nil {
		return nil, err
	}
	if err := g.hold(ctx, client, symbol, subType, true); err != nil {
		return nil, err
	}
	g.release(ctx, client, symbol, subType)
	g.release(ctx, client, symbol, subTypeBasic)

	bars, err := client.getKL(ctx, symbol, klType, 1000)
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
		bars, err = client.getKL(ctx, symbol, klType, 1000)
		if err != nil {
			return nil, err
		}
	}
	if err := g.storeBars(ctx, symbol, resolution, bars); err != nil {
		return nil, err
	}
	if err := g.maybeHistory(ctx, client, symbol, resolution, klType, fromMs); err != nil {
		g.module.logger.Warn("富途牛牛history fallback skipped", "errorType", fmt.Sprintf("%T", err))
	}
	return g.loadBars(ctx, symbol, resolution, fromMs, toMs)
}

func (g *futuGateway) maybeHistory(ctx context.Context, client *opendClient, symbol, resolution string, klType int, fromMs int64) error {
	if fromMs <= 0 {
		return nil
	}
	oldest, err := g.module.queries.OldestFutuBarTime(ctx, investmentsqlc.OldestFutuBarTimeParams{Symbol: symbol, Resolution: resolution})
	if err == nil && oldest <= fromMs {
		return nil
	}
	remain, used, err := client.historyQuota(ctx)
	if err != nil {
		return err
	}
	if remain <= 0 {
		if _, ok := used[symbol]; !ok {
			return errors.New("历史 K 线额度不足")
		}
	}
	begin := time.UnixMilli(fromMs).In(nyZone).Format("2006-01-02")
	end := time.Now().In(nyZone).Format("2006-01-02")
	hist, err := client.requestHistoryKL(ctx, symbol, klType, begin, end)
	if err != nil {
		return err
	}
	return g.storeBars(ctx, symbol, resolution, hist)
}

func (g *futuGateway) storeBars(ctx context.Context, symbol, resolution string, bars []futuBar) error {
	for _, bar := range bars {
		if err := g.module.queries.UpsertFutuBar(ctx, investmentsqlc.UpsertFutuBarParams{
			Symbol: symbol, Resolution: resolution, TimeMs: bar.Time, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (g *futuGateway) loadBars(ctx context.Context, symbol, resolution string, fromMs, toMs int64) ([]futuBar, error) {
	if toMs == 0 {
		toMs = time.Now().UnixMilli() + int64(time.Hour/time.Millisecond)
	}
	rows, err := g.module.queries.ListFutuBars(ctx, investmentsqlc.ListFutuBarsParams{
		Symbol: symbol, Resolution: resolution, TimeMs: fromMs, TimeMs_2: toMs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]futuBar, 0, len(rows))
	for _, row := range rows {
		out = append(out, futuBar{Time: row.TimeMs, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return out, nil
}
