package minimaxcode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultOAuthBaseURL = "https://account.minimax.io"
	clientID            = "mcode-public"
	scope               = "agent.default"
	audience            = "agent-backend"
	deviceGrant         = "urn:ietf:params:oauth:grant-type:device_code"
)

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) Load() (*Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read MiniMax Code credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		return nil, fmt.Errorf("decode MiniMax Code credentials: %w", err)
	}
	return &credentials, nil
}

func (s *Store) Save(credentials *Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".minimax-code-auth-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

type Manager struct {
	Store           *Store
	OAuthBaseURL    string
	AccountBaseURL  string
	PlatformBaseURL string
	HTTP            *http.Client
	mu              sync.Mutex
	claimMu         sync.Mutex
}

func NewManager(path string) *Manager {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "llm-proxy", "minimax-code-auth.json")
	}
	return &Manager{
		Store:        &Store{Path: path},
		OAuthBaseURL: defaultOAuthBaseURL,
		HTTP:         &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *Manager) HasSession() bool {
	credentials, err := m.Store.Load()
	return err == nil && credentials != nil && credentials.AccessToken != ""
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.Store.Load()
	if err != nil {
		return "", err
	}
	if credentials == nil || credentials.AccessToken == "" {
		return "", errors.New("MiniMax Code account is not signed in; use the dashboard to sign in")
	}
	if credentials.ExpiresAt > 0 && credentials.ExpiresAt <= time.Now().Add(5*time.Minute).Unix() {
		if credentials.RefreshToken == "" {
			return "", errors.New("MiniMax Code session expired; sign in again")
		}
		response, err := m.postForm(ctx, "/oauth2/token", url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {credentials.RefreshToken},
			"client_id":     {clientID},
			"scope":         {scope},
			"audience":      {audience},
		})
		if err != nil {
			return "", fmt.Errorf("refresh MiniMax Code session: %w", err)
		}
		updated, err := credentialsFromToken(response, credentials.RefreshToken)
		if err != nil {
			return "", err
		}
		if err := m.Store.Save(updated); err != nil {
			return "", err
		}
		credentials = updated
	}
	return credentials.AccessToken, nil
}

type deviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURL         string `json:"verification_url"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	ExpiredIn               int64  `json:"expired_in"`
	Interval                int    `json:"interval"`
}

// LoginDevice follows the public PKCE device flow used by MiniMax Code 0.5.5.
func (m *Manager) LoginDevice(ctx context.Context, announce func(string)) error {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challenge := sha256.Sum256([]byte(verifier))
	response, err := m.postForm(ctx, "/oauth2/device/code", url.Values{
		"client_id":             {clientID},
		"scope":                 {scope},
		"audience":              {audience},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	})
	if err != nil {
		return fmt.Errorf("request MiniMax Code device code: %w", err)
	}
	var device deviceAuthorization
	if err := json.Unmarshal(response, &device); err != nil {
		return err
	}
	if device.VerificationURI == "" {
		device.VerificationURI = device.VerificationURL
	}
	legacyDevice := device.DeviceCode == "" && device.UserCode != "" && device.ExpiredIn > time.Now().UnixMilli()
	if device.DeviceCode == "" && legacyDevice {
		device.DeviceCode = device.UserCode
		device.ExpiresIn = int(time.Until(time.UnixMilli(device.ExpiredIn)).Seconds())
		device.Interval /= 1000
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.VerificationURI == "" || device.ExpiresIn <= 0 {
		return errors.New("MiniMax Code device authorization response is incomplete")
	}
	link := device.VerificationURIComplete
	if link == "" {
		link = device.VerificationURI
	}
	announce("Open: " + link)
	announce("Code: " + device.UserCode)
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		pollParams := url.Values{
			"grant_type":    {deviceGrant},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		}
		if legacyDevice {
			pollParams.Set("user_code", device.UserCode)
		} else {
			pollParams.Set("device_code", device.DeviceCode)
		}
		response, err := m.postForm(ctx, "/oauth2/token", pollParams)
		if err == nil {
			var status struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(response, &status)
			switch status.Status {
			case "pending":
				err = oauthError("authorization_pending")
			case "slow_down":
				err = oauthError("slow_down")
			case "denied", "access_denied", "expired", "expired_token":
				return fmt.Errorf("MiniMax Code sign-in: %s", status.Status)
			default:
				credentials, decodeErr := credentialsFromToken(response, "")
				if decodeErr != nil {
					return decodeErr
				}
				return m.Store.Save(credentials)
			}
		}
		if err != oauthError("authorization_pending") && err != oauthError("slow_down") {
			return err
		}
		if err == oauthError("slow_down") {
			interval += 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return errors.New("MiniMax Code device authorization expired")
}

type oauthError string

func (e oauthError) Error() string { return string(e) }

func credentialsFromToken(data []byte, oldRefresh string) (*Credentials, error) {
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 || !strings.EqualFold(token.TokenType, "bearer") {
		return nil, errors.New("MiniMax Code token response is incomplete")
	}
	if token.RefreshToken == "" {
		token.RefreshToken = oldRefresh
	}
	return &Credentials{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()}, nil
}

func (m *Manager) postForm(ctx context.Context, path string, values url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.OAuthBaseURL, "/")+path, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &failure)
	if failure.Error != "" {
		return nil, oauthError(failure.Error)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MiniMax Code OAuth returned HTTP %d", resp.StatusCode)
	}
	return data, nil
}
