package mobilepush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"workbench/internal/contracts"
)

type APNsConfig struct {
	TeamID     string
	KeyID      string
	BundleID   string
	PrivateKey []byte
	Sandbox    bool
	BaseURL    string
	Client     *http.Client
}

type apnsProvider struct {
	teamID, keyID, bundleID string
	key                     *ecdsa.PrivateKey
	baseURL                 string
	client                  *http.Client
	now                     func() time.Time
	mu                      sync.Mutex
	jwt                     string
	jwtAt                   time.Time
}

func newAPNs(cfg APNsConfig) (*apnsProvider, error) {
	if cfg.TeamID == "" || cfg.KeyID == "" || cfg.BundleID == "" || len(cfg.PrivateKey) == 0 {
		return nil, errors.New("team ID, key ID, bundle ID, and private key are required")
	}
	block, _ := pem.Decode(cfg.PrivateKey)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not a PKCS#8 key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve.Params().Name != "P-256" {
		return nil, errors.New("private key must be ECDSA P-256")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.push.apple.com"
		if cfg.Sandbox {
			baseURL = "https://api.sandbox.push.apple.com"
		}
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &apnsProvider{teamID: cfg.TeamID, keyID: cfg.KeyID, bundleID: cfg.BundleID, key: key, baseURL: strings.TrimRight(baseURL, "/"), client: client, now: time.Now}, nil
}

func (p *apnsProvider) Send(ctx context.Context, note contracts.Notification, deviceToken string) (bool, error) {
	token, err := p.authorizationToken()
	if err != nil {
		return false, &deliveryError{message: "cannot sign APNs request", permanent: true}
	}
	payload, err := json.Marshal(map[string]any{
		"aps":            map[string]any{"alert": map[string]string{"title": truncateRunes(note.Title, 100), "body": truncateRunes(note.Content, 700)}, "sound": "default"},
		"notificationId": note.ID,
		"actionRoute":    note.ActionRoute,
	})
	if err != nil {
		return false, &deliveryError{message: "cannot encode APNs payload", permanent: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/3/device/"+url.PathEscape(deviceToken), bytes.NewReader(payload))
	if err != nil {
		return false, &deliveryError{message: "cannot create APNs request", permanent: true}
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("apns-topic", p.bundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return false, errors.New("cannot connect to APNs")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return false, nil
	}
	var result struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&result)
	switch result.Reason {
	case "BadDeviceToken", "DeviceTokenNotForTopic", "Unregistered":
		return true, nil
	case "ExpiredProviderToken":
		p.clearAuthorizationToken()
		return false, errors.New("APNs provider token expired")
	}
	if resp.StatusCode == http.StatusGone {
		return true, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return false, &deliveryError{message: "APNs delivery failed; retrying", retryAfter: responseRetryAfter(resp)}
	}
	return false, &deliveryError{message: "APNs rejected the notification", permanent: true}
}

func (p *apnsProvider) clearAuthorizationToken() {
	p.mu.Lock()
	p.jwt = ""
	p.jwtAt = time.Time{}
	p.mu.Unlock()
}

func (p *apnsProvider) authorizationToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now().UTC()
	if p.jwt != "" && now.Sub(p.jwtAt) < 50*time.Minute {
		return p.jwt, nil
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": p.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": p.teamID, "iat": now.Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := append(fixedBytes(r, 32), fixedBytes(s, 32)...)
	p.jwt = unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	p.jwtAt = now
	return p.jwt, nil
}

func fixedBytes(value *big.Int, size int) []byte {
	result := make([]byte, size)
	value.FillBytes(result)
	return result
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func responseRetryAfter(resp *http.Response) time.Duration {
	value := resp.Header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(min(seconds, int64(86400))) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(at))
	}
	return 0
}
