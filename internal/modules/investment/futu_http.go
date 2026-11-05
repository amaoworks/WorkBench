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
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication_required", "请登录")
		return
	}
	symbol, symbolErr := normalizeFutuSymbol(r.URL.Query().Get("symbol"))
	if symbolErr != nil {
		writeFutuError(w, symbolErr)
		return
	}
	session, sessionErr := futuSession(r.URL.Query().Get("session"))
	if sessionErr != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", sessionErr.Error())
		return
	}
	resolution := r.URL.Query().Get("resolution")
	if _, _, ok := futuKLTypes(resolution); !ok {
		httpapi.Write(w, http.StatusOK, map[string]any{"candles": []futuBar{}})
		return
	}
	if session != "regular" && !intradayFutuResolution(resolution) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "扩展时段仅支持分钟 K 线")
		return
	}
	from, to, err := parseKlineRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), futuKlineTimeout)
	defer cancel()
	var bars []futuBar
	if session == "night" {
		bars, err = m.futu.kline(ctx, symbol, resolution, from, to, r.URL.Query().Get("requireHistory") == "true")
	} else {
		bars, err = m.futu.mainKline(ctx, symbol, resolution, session, from, to, r.URL.Query().Get("requireHistory") == "true")
	}
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
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, 401, "authentication_required", "请登录")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != "" && mode != "main" {
		httpapi.Error(w, 400, "invalid_request", "无效的行情模式")
		return
	}
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途行情配置")
		return
	}
	if !rec.Enabled {
		writeFutuError(w, errFutuDisabled)
		return
	}
	if mode != "main" && !rec.OvernightEnabled {
		writeFutuError(w, errOverlayOff)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &futuWSClient{conn: conn, send: make(chan []byte, 64), main: mode == "main"}
	if err := m.futu.addWS(r.Context(), client); err != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "OpenD unavailable"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	go client.writePump()
	defer m.futu.removeWS(client)
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(futuPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(futuPongWait))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *Module) futuCommand(w http.ResponseWriter, r *http.Request) {
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, 401, "authentication_required", "请登录")
		return
	}
	var input struct {
		Command    string `json:"command"`
		Symbol     string `json:"symbol"`
		Resolution string `json:"resolution"`
		Generation string `json:"generation"`
		Owner      string `json:"owner"`
	}
	if httpapi.Decode(w, r, &input, 16<<10) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的订阅命令")
		return
	}
	symbol, symbolErr := normalizeFutuSymbol(input.Symbol)
	if symbolErr != nil {
		writeFutuError(w, symbolErr)
		return
	}
	command := strings.ToUpper(input.Command)
	if symbol == "" || (command != "ADD" && command != "UNSUB") {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "请使用 ADD 或 UNSUB 并提供标的")
		return
	}
	if input.Owner == "" {
		httpapi.Error(w, http.StatusConflict, "futu_client_required", "请重新连接富途行情")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	generation, applied, err := m.futu.browserCommand(ctx, command, input.Owner, input.Generation, symbol, input.Resolution)
	if err != nil {
		if errors.Is(err, errOverlayOff) {
			httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
			return
		}
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]any{"success": true, "applied": applied, "generation": generation, "owner": input.Owner})
}
