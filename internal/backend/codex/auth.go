package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Issuer   = "https://auth.openai.com"
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

// Credentials are the ChatGPT account credentials required by the Codex
// subscription endpoint. AccountID selects the workspace whose Codex
// entitlement is used for the request.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) Load() (*Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(b, &credentials); err != nil {
		return nil, fmt.Errorf("decode Codex credentials: %w", err)
	}
	return &credentials, nil
}

func (s *Store) Save(credentials *Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(s.Path, 0600)
}

// Manager performs device-code login and refreshes the saved ChatGPT session.
type Manager struct {
	Store      *Store
	Issuer     string
	ClientID   string
	HTTPClient *http.Client
	mu         sync.Mutex
}

func NewManager(path string) *Manager {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "llm-proxy", "codex-auth.json")
	}
	return &Manager{
		Store:      &Store{Path: path},
		Issuer:     Issuer,
		ClientID:   ClientID,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *Manager) HasSession() bool {
	credentials, err := m.Store.Load()
	return err == nil && credentials != nil && credentials.AccessToken != "" && credentials.AccountID != ""
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	credentials, err := m.Credentials(ctx)
	return credentials.AccessToken, err
}

func (m *Manager) Credentials(ctx context.Context) (Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.Store.Load()
	if err != nil {
		return Credentials{}, err
	}
	if credentials == nil || credentials.AccessToken == "" {
		return Credentials{}, errors.New("not signed in to codex; use the dashboard to sign in with ChatGPT")
	}
	if credentials.ExpiresAt == 0 {
		credentials.ExpiresAt, _ = jwtExpiration(credentials.AccessToken)
	}
	if credentials.ExpiresAt > 0 && credentials.ExpiresAt <= time.Now().Add(5*time.Minute).Unix() {
		if credentials.RefreshToken == "" {
			return Credentials{}, errors.New("codex session expired; sign in again from the dashboard")
		}
		if err := m.refresh(ctx, credentials); err != nil {
			return Credentials{}, err
		}
		if err := m.Store.Save(credentials); err != nil {
			return Credentials{}, err
		}
	}
	if credentials.AccountID == "" {
		credentials.AccountID, _ = jwtAccountID(credentials.IDToken)
	}
	if credentials.AccountID == "" {
		return Credentials{}, errors.New("codex session has no ChatGPT account ID; sign in again")
	}
	return *credentials, nil
}

type deviceCodeResponse struct {
	DeviceAuthID string         `json:"device_auth_id"`
	UserCode     string         `json:"user_code"`
	Interval     flexibleNumber `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

type flexibleNumber float64

func (n *flexibleNumber) UnmarshalJSON(data []byte) error {
	var number json.Number
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		number = json.Number(value)
	} else {
		number = json.Number(string(data))
	}
	value, err := strconv.ParseFloat(string(number), 64)
	*n = flexibleNumber(value)
	return err
}

// LoginDevice performs the same device-code flow used by the Codex CLI. The
// callback receives browser-safe progress strings consumed by the dashboard.
func (m *Manager) LoginDevice(ctx context.Context, announce func(string)) error {
	issuer := strings.TrimRight(m.Issuer, "/")
	var device deviceCodeResponse
	if err := m.doJSON(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": m.ClientID}, &device); err != nil {
		return fmt.Errorf("request Codex device code: %w", err)
	}
	if device.DeviceAuthID == "" || device.UserCode == "" {
		return errors.New("codex device authorization omitted the device or user code")
	}
	announce("Open: " + issuer + "/codex/device")
	announce("Code: " + device.UserCode)
	announce("Only continue if you started this sign-in from llm-proxy.")

	interval := time.Duration(float64(device.Interval) * float64(time.Second))
	if interval < 0 {
		interval = 0
	}
	deadline := time.Now().Add(15 * time.Minute)
	var authorization deviceTokenResponse
	for time.Now().Before(deadline) {
		status, err := m.doJSONStatus(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/token", map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		}, &authorization)
		if err == nil {
			break
		}
		if status == http.StatusForbidden || status == http.StatusNotFound {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
			continue
		}
		return fmt.Errorf("poll Codex device authorization: %w", err)
	}
	if authorization.AuthorizationCode == "" || authorization.CodeVerifier == "" {
		return errors.New("codex device authorization expired")
	}

	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authorization.AuthorizationCode},
		"redirect_uri":  {issuer + "/deviceauth/callback"},
		"client_id":     {m.ClientID},
		"code_verifier": {authorization.CodeVerifier},
	}
	var tokens tokenResponse
	if err := m.doForm(ctx, issuer+"/oauth/token", values, &tokens); err != nil {
		return fmt.Errorf("exchange Codex device code: %w", err)
	}
	credentials, err := credentialsFromTokens(tokens, "")
	if err != nil {
		return err
	}
	return m.Store.Save(credentials)
}

func (m *Manager) refresh(ctx context.Context, credentials *Credentials) error {
	var tokens tokenResponse
	status, err := m.doJSONStatus(ctx, http.MethodPost, strings.TrimRight(m.Issuer, "/")+"/oauth/token", map[string]string{
		"client_id":     m.ClientID,
		"grant_type":    "refresh_token",
		"refresh_token": credentials.RefreshToken,
	}, &tokens)
	if err != nil {
		return fmt.Errorf("refresh Codex session (HTTP %d): %w", status, err)
	}
	fresh, err := credentialsFromTokens(tokens, credentials.AccountID)
	if err != nil {
		return err
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = credentials.RefreshToken
	}
	*credentials = *fresh
	return nil
}

func credentialsFromTokens(tokens tokenResponse, fallbackAccountID string) (*Credentials, error) {
	if tokens.AccessToken == "" {
		return nil, errors.New("OpenAI token response omitted access_token")
	}
	accountID, _ := jwtAccountID(tokens.IDToken)
	if accountID == "" {
		accountID = fallbackAccountID
	}
	expiresAt, _ := jwtExpiration(tokens.AccessToken)
	return &Credentials{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken, AccountID: accountID, ExpiresAt: expiresAt}, nil
}

func jwtAccountID(token string) (string, error) {
	claims, err := jwtClaims(token)
	if err != nil {
		return "", err
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID, nil
}

func jwtExpiration(token string) (int64, error) {
	claims, err := jwtClaims(token)
	if err != nil {
		return 0, err
	}
	switch value := claims["exp"].(type) {
	case float64:
		return int64(value), nil
	case json.Number:
		return value.Int64()
	default:
		return 0, nil
	}
}

func jwtClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (m *Manager) doJSON(ctx context.Context, method, endpoint string, body, out any) error {
	_, err := m.doJSONStatus(ctx, method, endpoint, body, out)
	return err
}

func (m *Manager) doJSONStatus(ctx context.Context, method, endpoint string, body, out any) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return m.execute(req, out)
}

func (m *Manager) doForm(ctx context.Context, endpoint string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err = m.execute(req, out)
	return err
}

func (m *Manager) execute(req *http.Request, out any) (int, error) {
	client := m.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
