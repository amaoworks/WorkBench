package investment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (m *Module) streamerCredentials(ctx context.Context) (streamerInfo, string, uint64, error) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	if err := m.refreshAccessTokenLocked(ctx); err != nil {
		return streamerInfo{}, "", 0, err
	}
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	token := rec.AccessToken
	if token == "" {
		return streamerInfo{}, "", 0, errors.New("尚未连接 Schwab")
	}
	m.streamer.mu.Lock()
	generation := m.streamer.generation
	m.streamer.mu.Unlock()
	if rec.StreamerInfo != "" {
		var cached streamerInfo
		if json.Unmarshal([]byte(rec.StreamerInfo), &cached) == nil && cached.StreamerSocketURL != "" {
			return cached, token, generation, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.schwabAPI, "/")+"/trader/v1/userPreference", nil)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return streamerInfo{}, "", 0, errors.New(trimErrorBody(body))
	}
	var parsed struct {
		StreamerInfo []streamerInfo `json:"streamerInfo"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return streamerInfo{}, "", 0, err
	}
	if len(parsed.StreamerInfo) == 0 || parsed.StreamerInfo[0].StreamerSocketURL == "" {
		return streamerInfo{}, "", 0, errors.New("userPreference 未返回 streamerInfo")
	}
	info := parsed.StreamerInfo[0]
	raw, _ := json.Marshal(info)
	rec.StreamerInfo = string(raw)
	if err := m.storeSchwab(ctx, rec); err != nil {
		return streamerInfo{}, "", 0, err
	}
	return info, token, generation, nil
}
