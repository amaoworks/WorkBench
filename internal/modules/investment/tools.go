package investment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"workbench/internal/contracts"
)

func decodeInvestmentTool(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("工具参数无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("工具参数无效")
	}
	return nil
}

func (m *Module) positionsTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var args struct {
		AccountHash *string `json:"accountHash"`
	}
	if err := decodeInvestmentTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	accountHash := ""
	if args.AccountHash != nil {
		accountHash = strings.TrimSpace(*args.AccountHash)
		if accountHash == "" {
			return contracts.ToolResult{}, errors.New("账户标识不能为空")
		}
	}
	response := m.positions(ctx)
	if args.AccountHash == nil || response.Status != "ready" {
		return marshalInvestmentTool(response)
	}
	response.Accounts = filterAccounts(response.Accounts, accountHash)
	response.Items = filterPositions(response.Items, accountHash)
	if len(response.Accounts) == 0 {
		return contracts.ToolResult{}, errors.New("投资账户不存在")
	}
	return marshalInvestmentTool(response)
}

func filterAccounts(items []portfolioAccount, accountHash string) []portfolioAccount {
	out := make([]portfolioAccount, 0, 1)
	for _, item := range items {
		if item.Hash == accountHash {
			out = append(out, item)
		}
	}
	return out
}

func filterPositions(items []portfolioPosition, accountHash string) []portfolioPosition {
	out := make([]portfolioPosition, 0, len(items))
	for _, item := range items {
		if item.AccountHash == accountHash {
			out = append(out, item)
		}
	}
	return out
}

func (m *Module) ordersTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var args struct {
		Days        int     `json:"days"`
		Status      string  `json:"status"`
		TimeZone    string  `json:"timeZone"`
		AccountHash *string `json:"accountHash"`
	}
	if err := decodeInvestmentTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	accountHash := ""
	if args.AccountHash != nil {
		accountHash = strings.TrimSpace(*args.AccountHash)
		if accountHash == "" {
			return contracts.ToolResult{}, errors.New("账户标识不能为空")
		}
	}
	response, err := m.orders(ctx, args.Days, args.Status, args.TimeZone)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	if args.AccountHash == nil || response.Status != "ready" {
		return marshalInvestmentTool(response)
	}
	filtered := make([]portfolioOrder, 0, len(response.Items))
	for _, item := range response.Items {
		if item.AccountHash == accountHash {
			filtered = append(filtered, item)
		}
	}
	response.Items = filtered
	return marshalInvestmentTool(response)
}

func (m *Module) listMonitorsTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var args struct{}
	if err := decodeInvestmentTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	result, err := m.priceMonitor(ctx)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	return marshalInvestmentTool(result)
}

func (m *Module) createMonitorTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var args priceRuleInput
	if err := decodeInvestmentTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	result, _, err := m.savePriceRuleValue(ctx, "", args, true)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	return marshalInvestmentTool(result)
}

func marshalInvestmentTool(value any) (contracts.ToolResult, error) {
	body, err := json.Marshal(value)
	return contracts.ToolResult{Content: body}, err
}
