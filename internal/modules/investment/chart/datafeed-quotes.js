import { etMinuteOfDay, isOvernightET, isSchwabExtendedET } from './futu.js';

export function quoteNumber(value) {
    if (value === null || value === undefined || value === '') return undefined;
    const number = Number(value);
    return Number.isFinite(number) ? number : undefined;
}

function defined(values) {
    return Object.fromEntries(Object.entries(values).filter(([, value]) =>
        value !== undefined && value !== null && (typeof value !== 'number' || Number.isFinite(value))));
}

function change(price, baseline) {
    return price !== undefined && baseline > 0
        ? { ch: price - baseline, chp: (price / baseline - 1) * 100 }
        : {};
}

function extendedValues(price, regularPrice, tradeTime) {
    // A regular-hours print is not a pre/post-market trade. Missing trade times
    // stay missing instead of using the stream packet's delivery time.
    const minutes = tradeTime === undefined ? undefined : etMinuteOfDay(tradeTime);
    if (minutes !== undefined && (!isSchwabExtendedET(tradeTime) || (minutes >= 570 && minutes < 960))) {
        return { rtc: null, rch: null, rchp: null, rtc_time: null };
    }
    if (price === undefined || regularPrice === undefined) return {};
    const delta = change(price, regularPrice);
    return defined({ rtc: price, rch: delta.ch, rchp: delta.chp, rtc_time: tradeTime === undefined ? undefined : tradeTime / 1000 });
}

// Quotes have a different contract from the last traded price used to build
// bars: TV keeps the main quote and extended-session quote in separate fields.
export function schwabStreamQuoteValues(service, snapshot, barQuote) {
    const quote = defined(barQuote);
    const volumeField = service === 'LEVELONE_FOREX' ? '6' : '8';
    const closeField = service === 'LEVELONE_OPTIONS' ? '7'
        : service === 'LEVELONE_FUTURES' || service === 'LEVELONE_FUTURES_OPTIONS' ? '14' : '12';
    delete quote.volume;
    delete quote.prev_close_price;
    delete quote.rtc;
    delete quote.rch;
    delete quote.rchp;
    delete quote.rtc_time;
    delete quote.ch;
    delete quote.chp;
    Object.assign(quote, defined({ volume: quoteNumber(snapshot[volumeField]), prev_close_price: quoteNumber(snapshot[closeField]) }));
    if (service === 'LEVELONE_EQUITIES') {
        const last = quoteNumber(snapshot['3']) ?? quoteNumber(snapshot['33']);
        const regular = quoteNumber(snapshot['29']);
        const lp = regular ?? last;
        const daily = change(lp, quote.prev_close_price);
        Object.assign(quote, defined({
            lp, regular_price: regular,
            ch: quoteNumber(snapshot['31']) ?? daily.ch, chp: quoteNumber(snapshot['43']) ?? daily.chp,
        }));
        Object.assign(quote, extendedValues(last, regular, quoteNumber(snapshot['35'])));
    } else {
        const fields = service === 'LEVELONE_OPTIONS' ? ['45', '46'] : service === 'LEVELONE_FUTURES' ? ['19', '20'] : [];
        const daily = change(quote.lp, quote.prev_close_price);
        Object.assign(quote, defined({ ch: quoteNumber(snapshot[fields[0]]) ?? daily.ch, chp: quoteNumber(snapshot[fields[1]]) ?? daily.chp }));
    }
    return quote;
}

export function schwabRestQuoteValues(data) {
    const source = data?.quote ?? {}, regular = data?.regular ?? {};
    const last = quoteNumber(source.lastPrice) ?? quoteNumber(source.mark);
    const regularPrice = quoteNumber(regular.regularMarketLastPrice);
    const lp = regularPrice ?? last, previous = quoteNumber(source.closePrice);
    const daily = change(lp, previous);
    return defined({
        lp, regular_price: regularPrice, ch: quoteNumber(regular.regularMarketNetChange) ?? daily.ch,
        chp: quoteNumber(regular.regularMarketPercentChange) ?? daily.chp,
        ask: quoteNumber(source.askPrice), bid: quoteNumber(source.bidPrice),
        open_price: quoteNumber(source.openPrice), high_price: quoteNumber(source.highPrice), low_price: quoteNumber(source.lowPrice),
        prev_close_price: previous, volume: quoteNumber(source.totalVolume),
        description: data?.reference?.description, exchange: data?.reference?.exchangeName,
        ...(data?.assetMainType === 'EQUITY' ? extendedValues(last, regularPrice, quoteNumber(source.tradeTime)) : {}),
    });
}

const ET_WEEKDAY = new Intl.DateTimeFormat('en-US', { timeZone: 'America/New_York', weekday: 'short' });

function equitySession(time, overnightEnabled) {
    const weekday = ET_WEEKDAY.format(new Date(time)), minutes = etMinuteOfDay(time);
    const tradingWeekday = !['Sat', 'Sun'].includes(weekday);
    const nightWeekday = minutes >= 1200 ? ['Sun', 'Mon', 'Tue', 'Wed', 'Thu'].includes(weekday) : tradingWeekday;
    if (overnightEnabled && isOvernightET(time) && nightWeekday) return 'night';
    if (!tradingWeekday) return 'out_of_session';
    if (minutes >= 240 && minutes < 570) return 'pre_market';
    if (minutes >= 570 && minutes < 960) return 'market';
    if (minutes >= 960 && minutes < 1200) return 'post_market';
    return 'out_of_session';
}

export function mergeQuoteValues(schwab = {}, futu, { equity = false, overnightEnabled = false, now = Date.now() } = {}) {
    const { regular_price: regularPrice, ...quote } = defined(schwab);
    const daily = change(quote.lp, quote.prev_close_price);
    Object.assign(quote, defined({ ch: quote.ch ?? daily.ch, chp: quote.chp ?? daily.chp }));
    // Futu's current adapter supplies overnight values. They must not replace
    // Schwab's daily OHLC/volume or use overnight change as a daily change.
    if (equity) {
        const session = equitySession(now, overnightEnabled);
        const night = overnightEnabled && session === 'night' ? quoteNumber(futu?.lp) : undefined;
        Object.assign(quote, {
            rtc: quote.rtc ?? null, rch: quote.rch ?? null, rchp: quote.rchp ?? null, rtc_time: quote.rtc_time ?? null,
            ntc: night ?? null, nch: night === undefined ? null : quoteNumber(futu?.ch) ?? null,
            nchp: night === undefined ? null : quoteNumber(futu?.chp) ?? null,
            // OpenD's overnight quote currently has no trade timestamp.
            ntc_time: null, cs: session,
        });
        if (night !== undefined) {
            // Show the overnight trade as the main price; its change is relative
            // to the latest Schwab regular price, falling back to the previous close.
            // Daily OHLC/volume stay Schwab's.
            const regular = regularPrice > 0 ? regularPrice
                : quote.prev_close_price > 0 ? quote.prev_close_price : undefined;
            const delta = change(night, regular);
            quote.lp = night;
            quote.ch = delta.ch ?? null;
            quote.chp = delta.chp ?? null;
        }
    }
    return quote;
}

export function quoteData(symbol, values) {
    const available = ['lp', 'rtc', 'ntc', 'bid', 'ask'].some(field => Number.isFinite(values[field]));
    return available ? { s: 'ok', n: String(symbol), v: { ...values } }
        : { s: 'error', n: String(symbol), v: { errmsg: '暂无行情数据' } };
}
