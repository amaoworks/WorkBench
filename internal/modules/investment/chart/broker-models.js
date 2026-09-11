const ORDER_TYPES = { 1: 'LIMIT', 2: 'MARKET', 3: 'STOP', 4: 'STOP_LIMIT' };
const DURATIONS = { DAY: 'DAY', GTC: 'GOOD_TILL_CANCEL', GOOD_TILL_CANCEL: 'GOOD_TILL_CANCEL', FOK: 'FILL_OR_KILL', FILL_OR_KILL: 'FILL_OR_KILL', IOC: 'IMMEDIATE_OR_CANCEL', IMMEDIATE_OR_CANCEL: 'IMMEDIATE_OR_CANCEL' };

function positive(value, label) {
    const number = Number(value);
    if (!Number.isFinite(number) || number <= 0) throw new Error(`${label}必须大于零`);
    return number;
}

export function buildOrder(order, original) {
    if (original && (original.orderLegCollection?.length !== 1 || original.childOrderStrategies?.length || original.orderStrategyType !== 'SINGLE')) {
        throw new Error('复杂订单请在 Schwab 修改，不能转换成单腿订单');
    }
    if (original && !Object.values(ORDER_TYPES).includes(original.orderType)) {
        throw new Error('当前不支持修改此订单类型，请在 Schwab 操作');
    }
    const originalLeg = original?.orderLegCollection?.[0];
    const symbol = originalLeg?.instrument?.symbol || String(order.symbol || '').trim();
    if (!symbol) throw new Error('缺少交易品种');
    const assetType = originalLeg?.instrument?.assetType || (symbol.includes(' ') ? 'OPTION' : 'EQUITY');
    if (!['EQUITY', 'OPTION'].includes(assetType) || symbol.includes('/')) throw new Error('当前下单仅支持股票和股票期权');
    if (![1, -1].includes(order.side)) throw new Error('无效的买卖方向');
    const type = ORDER_TYPES[order.type];
    if (!type) throw new Error('不支持的订单类型');
    const durationType = order.duration?.type ?? original?.duration ?? 'DAY';
    const duration = DURATIONS[durationType];
    if (!duration || order.duration?.datetime) throw new Error('不支持的订单有效期');
    const session = String(order.customFields?.session ?? order.session ?? original?.session ?? 'NORMAL');
    if (!['NORMAL', 'AM', 'PM', 'SEAMLESS', 'EXTO'].includes(session)) throw new Error('不支持的交易时段');
    if (session !== 'NORMAL' && (type !== 'LIMIT' || assetType !== 'EQUITY' || !['DAY', 'GOOD_TILL_CANCEL'].includes(duration))) {
        throw new Error('扩展时段仅支持 DAY/GTC 股票限价单');
    }
    if (['STOP', 'STOP_LIMIT'].includes(type) && !['DAY', 'GOOD_TILL_CANCEL'].includes(duration)) throw new Error('止损单仅支持 DAY/GTC');
    if (order.stopLoss != null || order.takeProfit != null) throw new Error('当前不支持附加止盈止损订单');
    let instruction = originalLeg?.instruction;
    if (!instruction) {
        const buy = order.side === 1;
        if (assetType === 'OPTION') {
            const closing = order.isClose === true || order.isOpening === false;
            instruction = `${buy ? 'BUY' : 'SELL'}_TO_${closing ? 'CLOSE' : 'OPEN'}`;
        } else {
            instruction = order.isClose && buy ? 'BUY_TO_COVER' : buy ? 'BUY' : 'SELL';
        }
    }
    const payload = {
        orderStrategyType: 'SINGLE', session, duration, orderType: type,
        orderLegCollection: [{ instruction, instrument: { assetType, symbol }, quantity: positive(order.qty, '数量') }]
    };
    if (type === 'LIMIT' || type === 'STOP_LIMIT') payload.price = positive(order.limitPrice, '限价');
    if (type === 'STOP' || type === 'STOP_LIMIT') payload.stopPrice = positive(order.stopPrice, '止损价');
    return payload;
}

export function flattenOrders(orders) {
    return orders.flatMap(order => [order, ...flattenOrders(order.childOrderStrategies || [])]);
}

export function mapOrder(order) {
    const leg = order.orderLegCollection?.[0];
    if (!leg?.instrument?.symbol || order.orderId == null) throw new Error('Schwab 返回了缺少品种或编号的订单');
    const status = { CANCELED: 1, EXPIRED: 1, REPLACED: 1, FILLED: 2, REJECTED: 5, AWAITING_PARENT_ORDER: 3 }[order.status] ?? 6;
    const type = Number(Object.keys(ORDER_TYPES).find(key => ORDER_TYPES[key] === order.orderType));
    const fills = (order.orderActivityCollection || []).flatMap(activity => activity.executionLegs || []);
    const filledQty = fills.reduce((total, fill) => total + Number(fill.quantity || 0), 0);
    return {
        id: String(order.orderId), symbol: leg.instrument.symbol,
        type: type || 1, side: leg.instruction?.includes('BUY') ? 1 : -1,
        qty: Number(order.quantity ?? leg.quantity), status,
        filledQty: Number(order.filledQuantity || 0),
        ...(filledQty ? { avgPrice: fills.reduce((total, fill) => total + Number(fill.price) * Number(fill.quantity || 0), 0) / filledQty } : {}),
        ...(['LIMIT', 'STOP_LIMIT'].includes(order.orderType) ? { limitPrice: Number(order.price) } : {}),
        ...(['STOP', 'STOP_LIMIT'].includes(order.orderType) ? { stopPrice: Number(order.stopPrice) } : {}),
        duration: { type: order.duration === 'GOOD_TILL_CANCEL' ? 'GTC' : order.duration || 'DAY' },
        updateTime: Date.parse(order.closeTime || order.enteredTime) || Date.now(),
    };
}

export function mapPositions(positions) {
    return positions.flatMap(position => {
        const symbol = position.instrument?.symbol;
        if (!symbol) return [];
        return [1, -1].flatMap(side => {
            const qty = Number(side === 1 ? position.longQuantity : position.shortQuantity);
            if (!Number.isFinite(qty) || qty <= 0) return [];
            const avgPrice = Number((side === 1 ? position.longAveragePrice ?? position.averageLongPrice : position.shortAveragePrice ?? position.averageShortPrice) ?? position.averagePrice);
            if (!Number.isFinite(avgPrice)) return [];
            const pl = Number(side === 1 ? position.longOpenProfitLoss : position.shortOpenProfitLoss);
            const marketValue = Number(position.marketValue);
            const marketPrice = Number.isFinite(marketValue) ? marketValue / qty : undefined;
            const dailyPnL = Number(position.currentDayProfitLoss);
            return [{
                id: `${symbol}:${side}`, symbol, side, qty, avgPrice, currency: 'USD',
                direction: side === 1 ? '持有' : '卖空',
                type: position.instrument?.assetType,
                ...(Number.isFinite(pl) ? { pl } : {}),
                ...(Number.isFinite(marketPrice) ? { marketPrice } : {}),
                ...(Number.isFinite(dailyPnL) ? { dailyPnL } : {}),
            }];
        });
    });
}
