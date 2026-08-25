package grok

// This file contains the xAI account authentication used by the Grok
// subscription endpoint. It intentionally has no API-key path.

import (
	"context"
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
	Issuer        = "https://auth.x.ai"
	ClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	Scopes        = "openid profile email offline_access grok-cli:access api:access"
	ClientVersion = "0.2.99"
)

type Token struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token,omitempty"`
	ExpiresIn    float64 `json:"expires_in,omitempty"`
	ExpiresAt    float64 `json:"expires_at,omitempty"`
	Issuer       string  `json:"issuer,omitempty"`
	ClientID     string  `json:"client_id,omitempty"`
	Source       string  `json:"source,omitempty"`
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) Load() (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.Path, err)
	}
	var token Token
	if err := json.Unmarshal(b, &token); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.Path, err)
	}
	return &token, nil
}

func (s *Store) Save(token *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token.Issuer == "" {
		token.Issuer = Issuer
	}
	if token.ClientID == "" {
		token.ClientID = ClientID
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = float64(time.Now().Unix()) + token.ExpiresIn
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(token, "", "  ")
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

func (s *Store) Clear() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Manager loads, imports, refreshes, and stores the xAI account session.
type Manager struct {
	Store       *Store
	HTTPClient  *http.Client
	LegacyPath  string
	GrokPath    string
	dashboards  map[string]*DashboardClient
	dashboardMu sync.Mutex
	mu          sync.Mutex
}

func NewManager(path string) *Manager {
	home, _ := os.UserHomeDir()
	if path == "" {
		path = filepath.Join(home, ".config", "grok-proxy", "auth.json")
	}
	return &Manager{
		Store:      &Store{Path: path},
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		LegacyPath: filepath.Join(home, ".config", "grok-subscription-client", "auth.json"),
		GrokPath:   filepath.Join(home, ".grok", "auth.json"),
		dashboards: make(map[string]*DashboardClient),
	}
}

// HasSession reports whether the configured store contains a saved account
// session. Requests use AccessToken so refresh and legacy imports still happen
// at request time.
func (m *Manager) HasSession() bool {
	token, err := m.Store.Load()
	if err == nil && token != nil && token.AccessToken != "" {
		return true
	}
	if _, err := os.Stat(m.GrokPath); err == nil {
		return true
	}
	return false
}

// AccessToken implements backend.TokenSource. It returns a refreshed account
// token when necessary and never exposes a static API-key configuration.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, err := m.Store.Load()
	if err != nil {
		return "", err
	}
	if token == nil {
		token, err = m.importLegacy()
		if err != nil {
			return "", err
		}
	}
	if token == nil || token.AccessToken == "" {
		return "", errors.New("not signed in; use the dashboard to sign in with xAI")
	}
	if token.ExpiresAt > 0 && token.ExpiresAt <= float64(time.Now().Add(5*time.Minute).Unix()) {
		if token.RefreshToken == "" {
			return "", errors.New("xAI session expired; use the dashboard to sign in again")
		}
		if err := m.refresh(ctx, token); err != nil {
			return "", err
		}
		if err := m.Store.Save(token); err != nil {
			return "", err
		}
	}
	return token.AccessToken, nil
}

func (m *Manager) importLegacy() (*Token, error) {
	if m.LegacyPath != "" {
		if b, err := os.ReadFile(m.LegacyPath); err == nil {
			var token Token
			if json.Unmarshal(b, &token) == nil && token.AccessToken != "" {
				if err := m.Store.Save(&token); err != nil {
					return nil, err
				}
				return &token, nil
			}
		}
	}
	if m.GrokPath != "" {
		if _, err := os.Stat(m.GrokPath); err == nil {
			token, err := ImportGrok(m.GrokPath)
			if err != nil {
				return nil, err
			}
			if err := m.Store.Save(token); err != nil {
				return nil, err
			}
			return token, nil
		}
	}
	return nil, nil
}

func (m *Manager) refresh(ctx context.Context, token *Token) error {
	issuer := token.Issuer
	if issuer == "" {
		issuer = Issuer
	}
	d, err := Discover(ctx, m.client(), issuer)
	if err != nil {
		return err
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "client_id": {first(token.ClientID, ClientID)}}
	var fresh Token
	if err := doForm(ctx, m.client(), d.TokenEndpoint, values, &fresh); err != nil {
		return err
	}
	if fresh.AccessToken == "" {
		return errors.New("xAI token response omitted access_token")
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = token.RefreshToken
	}
	fresh.Issuer, fresh.ClientID = issuer, first(token.ClientID, ClientID)
	*token = fresh
	return nil
}

func (m *Manager) client() *http.Client {
	if m.HTTPClient != nil {
		return m.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Dashboard returns a billing client pinned to a base URL. Tests use this to
// point xAI account calls at a local server.
func (m *Manager) Dashboard(baseURL string) *DashboardClient {
	baseURL = strings.TrimRight(baseURL, "/")
	m.dashboardMu.Lock()
	defer m.dashboardMu.Unlock()
	if client, ok := m.dashboards[baseURL]; ok {
		return client
	}
	if m.dashboards == nil {
		m.dashboards = make(map[string]*DashboardClient)
	}
	client := NewDashboardClient(baseURL, m.HTTPClient)
	m.dashboards[baseURL] = client
	return client
}

// Usage fetches account identity and current credit usage. It refreshes the
// account token when required and returns a generic error for dashboard API
// failures; no token material is included.
func (m *Manager) Usage(ctx context.Context, baseURL string) (Usage, error) {
	accessToken, err := m.AccessToken(ctx)
	if err != nil {
		return Usage{}, err
	}
	dashboard := m.Dashboard(baseURL)
	account, accountErr := dashboard.Account(ctx, accessToken)
	if accountErr != nil || account.UserID == "" {
		if accountErr != nil && !errors.Is(accountErr, context.Canceled) && !errors.Is(accountErr, context.DeadlineExceeded) {
			m.mu.Lock()
			token, loadErr := m.Store.Load()
			m.mu.Unlock()
			if loadErr == nil && token != nil {
				if parsed, parseErr := AccountFromToken(token.AccessToken); parseErr == nil && parsed.UserID != "" {
					account = parsed
					accountErr = nil
				}
			}
		}
		if accountErr != nil {
			return Usage{}, accountErr
		}
		if account.UserID == "" {
			return Usage{}, errors.New("account service did not return a user ID")
		}
	}
	billing, err := dashboard.Billing(ctx, accessToken, account.UserID)
	if err != nil {
		return Usage{}, err
	}
	return Usage{Account: account, Billing: billing}, nil
}

type Discovery struct {
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

type DeviceAuthorization struct {
	DeviceCode              string  `json:"device_code"`
	UserCode                string  `json:"user_code"`
	VerificationURI         string  `json:"verification_uri"`
	VerificationURIComplete string  `json:"verification_uri_complete"`
	ExpiresIn               float64 `json:"expires_in"`
	Interval                float64 `json:"interval"`
}

func Discover(ctx context.Context, client *http.Client, issuer string) (Discovery, error) {
	var d Discovery
	if err := doJSON(ctx, client, http.MethodGet, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", nil, &d); err != nil {
		return d, err
	}
	if d.AuthorizationEndpoint == "" || d.DeviceAuthorizationEndpoint == "" || d.TokenEndpoint == "" {
		return d, errors.New("xAI OIDC discovery is missing required endpoints")
	}
	return d, nil
}

// LoginDevice performs the xAI device flow and writes the resulting session.
// The caller owns the browser-facing UI and receives progress messages.
func (m *Manager) LoginDevice(ctx context.Context, announce func(string)) error {
	d, err := Discover(ctx, m.client(), Issuer)
	if err != nil {
		return err
	}
	var device DeviceAuthorization
	if err := doForm(ctx, m.client(), d.DeviceAuthorizationEndpoint, url.Values{"client_id": {ClientID}, "scope": {Scopes}}, &device); err != nil {
		return err
	}
	if device.DeviceCode == "" {
		return errors.New("xAI device authorization omitted device_code")
	}
	announce("Open: " + first(device.VerificationURIComplete, device.VerificationURI))
	if device.UserCode != "" {
		announce("Code: " + device.UserCode)
	}
	interval := time.Duration(device.Interval * float64(time.Second))
	if interval < time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn * float64(time.Second)))
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		var token Token
		err := doForm(ctx, m.client(), d.TokenEndpoint, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {device.DeviceCode}, "client_id": {ClientID}}, &token)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				continue
			}
			if strings.Contains(err.Error(), "slow_down") {
				interval += 5 * time.Second
				continue
			}
			return err
		}
		token.Issuer, token.ClientID = Issuer, ClientID
		return m.Store.Save(&token)
	}
	return errors.New("xAI device authorization expired")
}

// ImportGrok imports the session format used by the Grok CLI.
func ImportGrok(path string) (*Token, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	var best Token
	var bestTime time.Time
	for _, raw := range root {
		var candidate struct {
			Key          string `json:"key"`
			RefreshToken string `json:"refresh_token"`
			ExpiresAt    string `json:"expires_at"`
			Issuer       string `json:"oidc_issuer"`
			ClientID     string `json:"oidc_client_id"`
		}
		if json.Unmarshal(raw, &candidate) != nil || candidate.Key == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339, candidate.ExpiresAt)
		if best.AccessToken == "" || t.After(bestTime) {
			best = Token{AccessToken: candidate.Key, RefreshToken: candidate.RefreshToken, Issuer: first(candidate.Issuer, Issuer), ClientID: first(candidate.ClientID, ClientID)}
			bestTime = t
		}
	}
	if best.AccessToken == "" {
		return nil, fmt.Errorf("no session token found in %s", path)
	}
	if !bestTime.IsZero() {
		best.ExpiresAt = float64(bestTime.Unix())
	}
	best.Source = path
	return &best, nil
}

func doForm(ctx context.Context, client *http.Client, endpoint string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return execute(client, req, out)
}
func doJSON(ctx context.Context, client *http.Client, method, endpoint string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	return execute(client, req, out)
}
func execute(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, req.URL.Redacted(), strings.TrimSpace(string(b)))
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
func first(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
