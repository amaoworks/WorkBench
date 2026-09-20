// Symbol classification and TradingView trading-session descriptions.

export function identifyAssetType(symbolName) {
    if (!symbolName) return "EQUITIES";

    const cleanSymbol = String(symbolName).trim();

    if (cleanSymbol.startsWith("./")) {
        return "FUTURES_OPTIONS";    // 期货期权
    } else if (cleanSymbol.startsWith("/")) {
        return "FUTURES";            // 期货
    } else if (cleanSymbol.includes("/")) {
        return "FOREX";              // 外汇
    }
    // Option symbols include a space before the contract specification.
    else if (cleanSymbol.includes(" ")) {
        return "OPTIONS";            // 期权: GOOGL 260812P00345000
    }
    else {
        return "EQUITIES";           // 正股: AAPL, GOOGL
    }
}

export function getAssetSubsessions(assetType, overlayEnabled = false) {
    switch (assetType) {
        case "EQUITIES":
        case "OPTIONS": {
            const sessions = [
                { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1600" },
                { "description": "Extended Trading Hours", "id": "extended", "session": "0400-2000" },
                { "description": "Premarket", "id": "premarket", "session": "0400-0930" },
                { "description": "Postmarket", "id": "postmarket", "session": "1600-2000" },
            ];
            if (overlayEnabled && assetType === "EQUITIES") {
                sessions.push(
                    { "description": "Night", "id": "night", "session": "2000-0400" },
                    { "description": "24h", "id": "24h", "session": "2000-2000" }
                );
            }
            return sessions;
        }

        case "FUTURES":
        case "FUTURES_OPTIONS":
            // 期货与期货期权：通常收盘较晚，或有几乎全天交易的时段 (以16:15收盘为例)
            return [
                { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1615" },
                { "description": "Extended Trading Hours", "id": "extended", "session": "1800-1700" },
                { "description": "Premarket", "id": "premarket", "session": "1800-0930" },
                { "description": "Postmarket", "id": "postmarket", "session": "1615-1700" },
            ];

        case "FOREX":
            // 外汇：通常是 24/5 全天候交易
            return [
                { "description": "Regular Trading Hours", "id": "regular", "session": "0000-0000" },
                { "description": "24h", "id": "24h", "session": "0000-0000" }
            ];

        default:
            // 兜底时间
            return [
                { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1600" }
            ];
    }
}
