package mobilepush

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func TestAPNsSignsRequestAndClassifiesInvalidToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var requestCount int
	var authorization []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/3/device/device-token" || r.Header.Get("apns-topic") != "com.example.app" || r.Header.Get("apns-push-type") != "alert" {
			t.Errorf("unexpected APNs request: %s, headers = %v", r.URL.Path, r.Header)
		}
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "bearer ")
		authorization = append(authorization, jwt)
		if err := verifyES256(jwt, &key.PublicKey); err != nil {
			t.Errorf("APNs JWT: %v", err)
		}
		if requestCount == 2 {
			w.WriteHeader(http.StatusGone)
			_, _ = io.WriteString(w, `{"reason":"Unregistered"}`)
			return
		}
		if requestCount == 3 {
			w.Header().Set("Retry-After", "9")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if requestCount == 4 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"reason":"ExpiredProviderToken"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	p, err := newAPNs(APNsConfig{TeamID: "TEAM", KeyID: "KEY", BundleID: "com.example.app", PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	note := contracts.Notification{ID: "note", Title: "Title", Content: "Body"}
	invalid, err := p.Send(t.Context(), note, "device-token")
	if err != nil || invalid {
		t.Fatalf("first send invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), note, "device-token")
	if err != nil || !invalid {
		t.Fatalf("second send invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), note, "device-token")
	var retry contracts.RetryAfterError
	if invalid || !errors.As(err, &retry) || retry.RetryAfter() != 9*time.Second {
		t.Fatalf("rate limited send invalid/error/retry = %v/%v/%v", invalid, err, retry)
	}
	invalid, err = p.Send(t.Context(), note, "device-token")
	if invalid || err == nil {
		t.Fatalf("expired provider token invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), note, "device-token")
	if invalid || err != nil || authorization[3] == authorization[4] {
		t.Fatalf("refreshed provider token invalid/error/tokens = %v/%v/%v", invalid, err, authorization)
	}
}

func TestFCMSignsOAuthAssertionAndClassifiesUnregistered(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	var sendCount int
	var tokenCount int
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCount++
			body, _ := io.ReadAll(r.Body)
			values, parseErr := url.ParseQuery(string(body))
			if parseErr != nil || values.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("OAuth form = %q, err = %v", body, parseErr)
			}
			if err := verifyRS256(values.Get("assertion"), &key.PublicKey, server.URL+"/token"); err != nil {
				t.Errorf("OAuth assertion: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-token-" + strconv.Itoa(tokenCount), "expires_in": 3600})
		case "/v1/projects/project/messages:send":
			sendCount++
			expectedAuthorization := "Bearer access-token-1"
			if sendCount == 5 {
				expectedAuthorization = "Bearer access-token-2"
			}
			if r.Header.Get("Authorization") != expectedAuthorization {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			if sendCount == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"status":"NOT_FOUND"}}`)
				return
			}
			if sendCount == 2 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`)
				return
			}
			if sendCount == 3 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"status":"INVALID_ARGUMENT"}}`)
				return
			}
			if sendCount == 4 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"message":"upstream-secret"}}`)
				return
			}
			if sendCount == 5 {
				w.WriteHeader(http.StatusOK)
				return
			}
			t.Fatal("unexpected FCM send")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := newFCM(FCMConfig{ProjectID: "project", ClientEmail: "push@example.test", PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), TokenURL: server.URL + "/token", BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := p.Send(t.Context(), contracts.Notification{ID: "note", Title: "Title", Content: "Body"}, "device-token")
	var permanent contracts.PermanentDeliveryError
	if invalid || !errors.As(err, &permanent) || !permanent.Permanent() {
		t.Fatalf("generic not found invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), contracts.Notification{ID: "note", Title: "Title", Content: "Body"}, "device-token")
	if err != nil || !invalid {
		t.Fatalf("unregistered invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), contracts.Notification{ID: "note", Title: "Title", Content: "Body"}, "device-token")
	if invalid || !errors.As(err, &permanent) || !permanent.Permanent() {
		t.Fatalf("invalid argument invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), contracts.Notification{ID: "note", Title: "Title", Content: "Body"}, "device-token")
	if invalid || err == nil || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("rejected access token invalid/error = %v/%v", invalid, err)
	}
	invalid, err = p.Send(t.Context(), contracts.Notification{ID: "note", Title: "Title", Content: "Body"}, "device-token")
	if invalid || err != nil || tokenCount != 2 {
		t.Fatalf("refreshed access token invalid/error/token requests = %v/%v/%d", invalid, err, tokenCount)
	}
}

func verifyRS256(token string, key *rsa.PublicKey, audience string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return io.ErrUnexpectedEOF
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return err
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims struct {
		Issuer   string `json:"iss"`
		Subject  string `json:"sub"`
		Audience string `json:"aud"`
		Scope    string `json:"scope"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return err
	}
	if claims.Issuer != "push@example.test" || claims.Subject != "" || claims.Audience != audience || claims.Scope != firebaseMessagingScope || claims.Expires <= time.Now().Unix() {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func verifyES256(token string, key *ecdsa.PublicKey) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return io.ErrUnexpectedEOF
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return io.ErrUnexpectedEOF
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
