package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

type wallosCurrency struct {
	ID   json.Number `json:"id"`
	Code string      `json:"code"`
	Rate json.Number `json:"rate"`
}

func fetchWallosCurrencies(ctx context.Context, c wallosConfig) ([]wallosCurrency, error) {
	data, err := wallosRequest(ctx, c, "/api/currencies/get_currencies.php")
	if err != nil {
		return nil, err
	}
	var body struct {
		Success    bool             `json:"success"`
		Currencies []wallosCurrency `json:"currencies"`
	}
	if json.Unmarshal(data, &body) != nil || !body.Success || body.Currencies == nil {
		return nil, errors.New("Wallos 货币响应无效，无法读取币种和汇率")
	}
	return body.Currencies, nil
}

func wallosDecimal(n json.Number) (*big.Rat, bool) {
	// Bound remote numeric input before arbitrary precision arithmetic.
	if len(n) == 0 || len(n) > 64 || strings.ContainsAny(string(n), "eE/") {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(string(n))
	return r, ok && r.Sign() >= 0
}

func wallosAmount(sub wallosSubscription, currencies []wallosCurrency) string {
	price, ok := wallosDecimal(sub.Price)
	if !ok {
		return "金额未提供 -> 人民币金额无法换算"
	}
	var source, cny *wallosCurrency
	for i := range currencies {
		if currencies[i].ID == sub.CurrencyID {
			source = &currencies[i]
		}
		if strings.EqualFold(currencies[i].Code, "CNY") {
			cny = &currencies[i]
		}
	}
	if source == nil || strings.TrimSpace(source.Code) == "" {
		return price.FloatString(2) + "（币种未知） -> 人民币金额无法换算"
	}
	original := strings.ToUpper(source.Code) + " " + price.FloatString(2)
	if strings.EqualFold(source.Code, "CNY") {
		return original + " -> ￥" + price.FloatString(2)
	}
	if cny != nil {
		from, validFrom := wallosDecimal(source.Rate)
		to, validTo := wallosDecimal(cny.Rate)
		if validFrom && validTo && from.Sign() > 0 && to.Sign() > 0 {
			// Wallos stores units of each currency per unit of the user's main
			// currency. Cross conversion works even when the main currency isn't CNY.
			converted := new(big.Rat).Mul(price, to)
			converted.Quo(converted, from)
			return original + " -> ￥" + converted.FloatString(2)
		}
	}
	return original + " -> 人民币金额无法换算（Wallos 缺少有效汇率）"
}

func wallosDescription(sub wallosSubscription, timezone string) string {
	category := strings.TrimSpace(sub.Category)
	if category == "" || category == "No category" {
		category = "未分类"
	}
	paymentMethod := strings.TrimSpace(sub.PaymentMethod)
	if paymentMethod == "" || paymentMethod == "Unknown payment method" {
		paymentMethod = "未设置"
	}
	amount := sub.Amount
	if amount == "" {
		amount = wallosAmount(sub, nil)
	}
	return fmt.Sprintf("分类: %s\n支付方式: %s\n付款日期: %s（%s）\n金额: %s\n请及时付款！", category, paymentMethod, sub.NextPayment, timezone, amount)
}
