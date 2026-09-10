package investment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

func (m *Module) overview(w http.ResponseWriter, r *http.Request) {
	value, err := m.readOverview(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "investment_failed", "无法读取模拟行情")
		return
	}
	httpapi.Write(w, 200, value)
}

func (m *Module) syncHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_request", "请提交空 JSON 对象")
		return
	}
	if err := m.Sync(r.Context()); err != nil {
		httpapi.Error(w, 502, "sync_failed", "模拟行情同步失败")
		return
	}
	m.overview(w, r)
}

func (m *Module) summarize(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_request", "请提交空 JSON 对象")
		return
	}
	overview, err := m.readOverview(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "investment_failed", "无法读取模拟行情")
		return
	}
	if len(overview.Items) == 0 {
		httpapi.Error(w, 409, "quotes_required", "请先同步模拟行情")
		return
	}
	raw, _ := json.Marshal(overview.Items)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	text, err := m.deps.AI.GenerateText(ctx, contracts.TextRequest{Profile: "default", Instruction: "用中文简要描述以下虚构模拟行情的数值变化，明确说明数据为模拟。仅描述给定数据，不给投资建议，不执行任何操作。", Input: string(raw)})
	if errors.Is(err, contracts.ErrAIUnavailable) {
		httpapi.Error(w, 503, "ai_unavailable", "AI 未启用，请在设置中配置；行情浏览与同步仍可使用")
		return
	}
	if err != nil {
		httpapi.Error(w, 502, "summary_failed", "AI 摘要生成失败，请稍后重试")
		return
	}
	summary := Summary{Content: text, CreatedAt: time.Now().UTC()}
	if err := m.queries.SaveSummary(r.Context(), investmentsqlc.SaveSummaryParams{Content: text, CreatedAt: summary.CreatedAt.UnixMilli()}); err != nil {
		httpapi.Error(w, 500, "summary_failed", "无法保存摘要")
		return
	}
	httpapi.Write(w, 200, summary)
}
