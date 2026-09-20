package investment

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"workbench/internal/foundation/httpapi"
)

const futuKlineTimeout = 25 * time.Second

func (m *Module) getFutuKline(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	resolution := r.URL.Query().Get("resolution")
	if symbol == "" {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "缺少标的")
		return
	}
	if _, _, ok := futuKLTypes(resolution); !ok {
		httpapi.Write(w, http.StatusOK, map[string]any{"candles": []futuBar{}})
		return
	}
	from, to, err := parseKlineRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), futuKlineTimeout)
	defer cancel()
	bars, err := m.futu.kline(ctx, symbol, resolution, from, to)
	if err != nil {
		if errors.Is(err, errOverlayOff) {
			httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
			return
		}
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, map[string]any{"candles": bars})
}

func parseKlineRange(fromS, toS string) (fromMs, toMs int64, err error) {
	if fromS == "" && toS == "" {
		return 0, 0, nil
	}
	if fromS != "" {
		from, convErr := strconv.ParseInt(fromS, 10, 64)
		if convErr != nil {
			return 0, 0, errors.New("from 无效")
		}
		fromMs = from * 1000
	}
	if toS != "" {
		to, convErr := strconv.ParseInt(toS, 10, 64)
		if convErr != nil {
			return 0, 0, errors.New("to 无效")
		}
		toMs = to * 1000
	}
	return fromMs, toMs, nil
}

func (m *Module) serveFutuStream(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil || !rec.Enabled || !rec.OvernightEnabled {
		httpapi.Error(w, http.StatusConflict, "futu_disabled", "夜盘覆盖未启用")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &futuWSClient{conn: conn, send: make(chan []byte, 64)}
	if err := m.futu.addWS(r.Context(), client); err != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "OpenD unavailable"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	go client.writePump()
	defer m.futu.removeWS(client)
	conn.SetReadLimit(4096)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *Module) futuCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Command    string `json:"command"`
		Symbol     string `json:"symbol"`
		Resolution string `json:"resolution"`
	}
	if httpapi.Decode(w, r, &input, 16<<10) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的订阅命令")
		return
	}
	symbol := strings.TrimSpace(input.Symbol)
	command := strings.ToUpper(input.Command)
	if symbol == "" || (command != "ADD" && command != "UNSUB") {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "请使用 ADD 或 UNSUB 并提供标的")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var err error
	if command == "ADD" {
		err = m.futu.addSub(ctx, symbol, input.Resolution)
	} else {
		err = m.futu.releaseSub(ctx, symbol, input.Resolution)
	}
	if err != nil {
		if errors.Is(err, errOverlayOff) {
			httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
			return
		}
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"success": true})
}
