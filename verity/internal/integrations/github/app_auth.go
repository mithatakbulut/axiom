package github

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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AppTokenSource discovers the installation for one repository and caches its
// repository-scoped installation token until shortly before expiration.
type AppTokenSource struct {
	BaseURL, AppID, Owner, Repository string
	PrivateKeyPEM                     []byte
	HTTP                              *http.Client
	Now                               func() time.Time

	mu             sync.Mutex
	token          string
	expires        time.Time
	installationID int64
}

func (s *AppTokenSource) AccessToken(ctx context.Context) (string, error) {
	return s.AccessTokenForInstallation(ctx, 0)
}

// AccessTokenForInstallation binds the token to a signed webhook installation
// ID when the event carries one.
func (s *AppTokenSource) AccessTokenForInstallation(ctx context.Context, expectedInstallationID int64) (string, error) {
	if s == nil || s.AppID == "" || s.Owner == "" || s.Repository == "" || len(s.PrivateKeyPEM) == 0 {
		return "", errors.New("GitHub App credentials and repository scope are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Add(5*time.Minute).Before(s.expires) {
		if expectedInstallationID > 0 && s.installationID != expectedInstallationID {
			return "", errors.New("GitHub webhook installation does not match the configured repository installation")
		}
		return s.token, nil
	}

	key, err := parseAppKey(s.PrivateKeyPEM)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(s.AppID, key, now)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	var installation struct {
		ID int64 `json:"id"`
	}
	path := "/repos/" + url.PathEscape(s.Owner) + "/" + url.PathEscape(s.Repository) + "/installation"
	if err = s.appRequest(ctx, client, base+path, jwt, nil, &installation); err != nil {
		return "", err
	}
	if installation.ID <= 0 {
		return "", errors.New("GitHub App installation response has no valid id")
	}
	if expectedInstallationID > 0 && installation.ID != expectedInstallationID {
		return "", errors.New("GitHub webhook installation does not match the configured repository installation")
	}

	var response struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path = "/app/installations/" + strconv.FormatInt(installation.ID, 10) + "/access_tokens"
	body, _ := json.Marshal(map[string]any{"repositories": []string{s.Repository}})
	if err = s.appRequest(ctx, client, base+path, jwt, bytes.NewReader(body), &response); err != nil {
		return "", err
	}
	if response.Token == "" || response.ExpiresAt.IsZero() || !response.ExpiresAt.After(now.Add(time.Minute)) {
		return "", errors.New("GitHub App returned an invalid or near-expiry installation token")
	}
	s.token, s.expires, s.installationID = response.Token, response.ExpiresAt, installation.ID
	return s.token, nil
}

func (s *AppTokenSource) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *AppTokenSource) appRequest(ctx context.Context, client *http.Client, endpoint, jwt string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, body)
	if strings.Contains(endpoint, "/access_tokens") {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	}
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub App authentication endpoint returned %s: %s", resp.Status, compact(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func parseAppKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub App private key is not valid PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("GitHub App private key is not a supported RSA key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App private key must be RSA")
	}
	return key, nil
}

func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	issued := now.Add(-time.Minute).Unix()
	expires := now.Add(9 * time.Minute).Unix()
	claims, err := json.Marshal(map[string]any{"iat": issued, "exp": expires, "iss": appID})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + enc.EncodeToString(signature), nil
}
