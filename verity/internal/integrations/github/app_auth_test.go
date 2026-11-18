package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppTokenSourceDiscoversScopesAndCachesInstallationToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var discovery, issue int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.Count(auth, ".") != 2 {
			t.Errorf("expected JWT bearer auth")
		}
		parts := strings.Split(auth, ".")
		var claims struct {
			Issuer  string `json:"iss"`
			Issued  int64  `json:"iat"`
			Expires int64  `json:"exp"`
		}
		claimsData, _ := base64.RawURLEncoding.DecodeString(parts[1])
		if err := json.Unmarshal(claimsData, &claims); err != nil {
			t.Fatal(err)
		}
		if claims.Issuer != "1234" || claims.Expires-claims.Issued > 10*60 {
			t.Errorf("invalid JWT claims: %+v", claims)
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig) != nil {
			t.Error("JWT signature did not verify")
		}
		switch r.URL.Path {
		case "/repos/acme/widget/installation":
			discovery++
			return authResponse(`{"id":77}`), nil
		case "/app/installations/77/access_tokens":
			issue++
			if r.Method != http.MethodPost {
				t.Errorf("token issue method %s", r.Method)
			}
			var body struct {
				Repositories []string `json:"repositories"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Repositories) != 1 || body.Repositories[0] != "widget" {
				t.Errorf("unexpected token scope: %+v", body)
			}
			data, _ := json.Marshal(map[string]any{"token": "installation-token", "expires_at": time.Now().Add(time.Hour).UTC()})
			return authResponse(string(data)), nil
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: r}, nil
		}
	})
	clock := time.Now().UTC()
	source := &AppTokenSource{BaseURL: "https://api.example.test", AppID: "1234", Owner: "acme", Repository: "widget", PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509MarshalPKCS1(key)}), HTTP: &http.Client{Transport: transport}, Now: func() time.Time { return clock }}
	for i := 0; i < 2; i++ {
		token, err := source.AccessToken(context.Background())
		if err != nil || token != "installation-token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	if discovery != 1 || issue != 1 {
		t.Fatalf("discovery=%d issue=%d", discovery, issue)
	}
	if _, err := source.AccessTokenForInstallation(context.Background(), 77); err != nil {
		t.Fatalf("matching installation rejected: %v", err)
	}
	if _, err := source.AccessTokenForInstallation(context.Background(), 78); err == nil {
		t.Fatal("mismatched webhook installation accepted")
	}
	clock = clock.Add(56 * time.Minute)
	if _, err := source.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if discovery != 2 || issue != 2 {
		t.Fatalf("expired token was not refreshed: discovery=%d issue=%d", discovery, issue)
	}
}

func authResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func x509MarshalPKCS1(key *rsa.PrivateKey) []byte { return x509.MarshalPKCS1PrivateKey(key) }

func TestAppJWTBackdatesIssueTime(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000000000, 0).UTC()
	jwt, err := appJWT("app", key, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Issued  int64 `json:"iat"`
		Expires int64 `json:"exp"`
	}
	if err = json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Issued != now.Add(-time.Minute).Unix() || claims.Expires > now.Add(10*time.Minute).Unix() {
		t.Fatalf("unexpected claims %v", claims)
	}
}
