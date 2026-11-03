package mobilepush

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"workbench/internal/contracts"
)

const firebaseMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type FCMConfig struct {
	ProjectID   string
	ClientEmail string
	PrivateKey  []byte
	TokenURL    string
	BaseURL     string
	Client      *http.Client
}

type fcmProvider struct {
	projectID, clientEmail string
	key                    *rsa.PrivateKey
	tokenURL, baseURL      string
	client                 *http.Client
	now                    func() time.Time
	mu                     sync.Mutex
	accessToken            string
	accessExpiry           time.Time
}

func newFCM(cfg FCMConfig) (*fcmProvider, error) {
	if cfg.ProjectID == "" || cfg.ClientEmail == "" || len(cfg.PrivateKey) == 0 {
		return nil, errors.New("project ID, client email, and private key are required")
	}
	block, _ := pem.Decode(cfg.PrivateKey)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	var key *rsa.PrivateKey
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		key, _ = parsed.(*rsa.PrivateKey)
	} else if parsedPKCS1, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes); parseErr == nil {
		key = parsedPKCS1
	}
	if key == nil {
		return nil, errors.New("private key must be RSA")
	}
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://fcm.googleapis.com"
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &fcmProvider{projectID: cfg.ProjectID, clientEmail: cfg.ClientEmail, key: key, tokenURL: tokenURL, baseURL: strings.TrimRight(baseURL, "/"), client: client, now: time.Now}, nil
}

func (p *fcmProvider) Send(ctx context.Context, note contracts.Notification, deviceToken string) (bool, error) {
	accessToken, err := p.token(ctx)
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token":        deviceToken,
		"notification": map[string]string{"title": truncateRunes(note.Title, 100), "body": truncateRunes(note.Content, 700)},
		"data":         map[string]string{"notificationId": note.ID, "actionRoute": note.ActionRoute},
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/projects/"+url.PathEscape(p.projectID)+"/messages:send", bytes.NewReader(payload))
	if err != nil {
		return false, &deliveryError{message: "cannot create FCM request", permanent: true}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return false, errors.New("cannot connect to FCM")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	var result struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
	for _, detail := range result.Error.Details {
		if detail.ErrorCode == "UNREGISTERED" {
			return true, nil
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		p.clearToken()
		return false, errors.New("FCM access token was rejected")
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return false, &deliveryError{message: "FCM delivery failed; retrying", retryAfter: responseRetryAfter(resp)}
	}
	return false, &deliveryError{message: "FCM rejected the notification", permanent: true}
}

func (p *fcmProvider) token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now().UTC()
	if p.accessToken != "" && now.Add(time.Minute).Before(p.accessExpiry) {
		return p.accessToken, nil
	}
	assertion, err := p.signedAssertion(now)
	if err != nil {
		return "", &deliveryError{message: "cannot sign FCM OAuth assertion", permanent: true}
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", &deliveryError{message: "cannot create FCM OAuth request", permanent: true}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", errors.New("cannot connect to FCM OAuth")
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decodeErr != nil || result.AccessToken == "" || result.ExpiresIn <= 0 {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return "", &deliveryError{message: "FCM OAuth failed; retrying", retryAfter: responseRetryAfter(resp)}
		}
		return "", &deliveryError{message: "FCM OAuth credentials were rejected", permanent: true}
	}
	p.accessToken = result.AccessToken
	p.accessExpiry = now.Add(time.Duration(result.ExpiresIn) * time.Second)
	return p.accessToken, nil
}

func (p *fcmProvider) signedAssertion(now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": p.clientEmail, "aud": p.tokenURL,
		"scope": firebaseMessagingScope, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (p *fcmProvider) clearToken() {
	p.mu.Lock()
	p.accessToken = ""
	p.accessExpiry = time.Time{}
	p.mu.Unlock()
}
