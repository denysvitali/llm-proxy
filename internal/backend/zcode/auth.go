package zcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	// Issuer is the ZCode web service that owns the browser OAuth flow.
	Issuer = "https://zcode.z.ai"

	defaultPollInterval = 2 * time.Second
	loginTimeout        = 15 * time.Minute
	// captchaTTL is deliberately shorter than the verification parameter's
	// usual lifetime. A fresh browser verification is cheap compared with
	// sending a stale parameter and receiving code 3007 from ZCode.
	captchaTTL = 40 * time.Second
)

// Credentials is the ZCode session returned after browser authorization.
// The token is a ZCode JWT, not a Z.ai API key.
type Credentials struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
}

// Store persists a ZCode session with owner-only permissions.
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
		return nil, fmt.Errorf("read ZCode credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(b, &credentials); err != nil {
		return nil, fmt.Errorf("decode ZCode credentials: %w", err)
	}
	return &credentials, nil
}

func (s *Store) Save(credentials *Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if credentials == nil || strings.TrimSpace(credentials.AccessToken) == "" {
		return errors.New("cannot save empty ZCode credentials")
	}
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

// Manager performs the browser OAuth flow and supplies the saved ZCode JWT
// to the backend. There is no refresh token in this flow; expired sessions
// can be replaced by visiting the login page again.
type Manager struct {
	Store      *Store
	Issuer     string
	HTTPClient *http.Client
	mu         sync.Mutex
	captchaMu  sync.RWMutex
	captcha    string
	captchaAt  time.Time
}

func NewManager(path string) *Manager {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "llm-proxy", "zcode-auth.json")
	}
	return &Manager{
		Store:      &Store{Path: path},
		Issuer:     Issuer,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *Manager) HasSession() bool {
	credentials, err := m.Store.Load()
	return err == nil && credentials != nil && strings.TrimSpace(credentials.AccessToken) != ""
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credentials, err := m.Store.Load()
	if err != nil {
		return "", err
	}
	if credentials == nil || strings.TrimSpace(credentials.AccessToken) == "" {
		return "", errors.New("not signed in to ZCode; use the dashboard to sign in with ZCode")
	}
	if credentials.ExpiresAt == 0 {
		credentials.ExpiresAt, _ = jwtExpiration(credentials.AccessToken)
	}
	if credentials.ExpiresAt > 0 && credentials.ExpiresAt <= time.Now().Add(5*time.Minute).Unix() {
		return "", errors.New("ZCode session expired; sign in again from the dashboard")
	}
	return credentials.AccessToken, nil
}

// SetCaptchaVerifyParam stores a browser-generated Aliyun verification
// parameter for the short period in which ZCode accepts it. The parameter is
// not written to disk: it is a disposable proof, not an account credential.
func (m *Manager) SetCaptchaVerifyParam(param string) error {
	param = strings.TrimSpace(param)
	if param == "" {
		return errors.New("cannot save an empty ZCode CAPTCHA verification parameter")
	}
	if len(param) > 64<<10 {
		return errors.New("ZCode CAPTCHA verification parameter is too large")
	}
	m.captchaMu.Lock()
	m.captcha = param
	m.captchaAt = time.Now()
	m.captchaMu.Unlock()
	return nil
}

// CaptchaVerifyParam returns the currently cached browser verification
// parameter. It implements the optional source used by the ZCode backend.
func (m *Manager) CaptchaVerifyParam(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	m.captchaMu.RLock()
	param, issuedAt := m.captcha, m.captchaAt
	m.captchaMu.RUnlock()
	if param != "" && time.Since(issuedAt) < captchaTTL {
		return param, nil
	}
	return "", errors.New("ZCode CAPTCHA verification is required; open /login/zcode and click Verify browser session")
}

type oauthEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type initData struct {
	FlowID          string        `json:"flow_id"`
	AuthorizeURL    string        `json:"authorize_url"`
	ExpiresAt       int64         `json:"expires_at"`
	PollIntervalSec flexibleFloat `json:"poll_interval_sec"`
}

type pollData struct {
	Status string `json:"status"`
	Token  string `json:"token"`
}

// LoginDevice starts ZCode's CLI OAuth flow. The announce callback receives
// browser-safe progress messages; it never receives the temporary poll token
// or the resulting JWT.
func (m *Manager) LoginDevice(ctx context.Context, announce func(string)) error {
	pollToken, err := randomPollToken()
	if err != nil {
		return fmt.Errorf("create ZCode OAuth session: %w", err)
	}

	var init initData
	if err := m.oauthJSON(ctx, http.MethodPost, "/api/v1/oauth/cli/init", pollToken, map[string]string{"provider": "zai"}, &init); err != nil {
		return fmt.Errorf("start ZCode OAuth: %w", err)
	}
	if init.FlowID == "" || init.AuthorizeURL == "" {
		return errors.New("ZCode OAuth response omitted the flow or authorization URL")
	}
	if err := validateAuthorizeURL(init.AuthorizeURL); err != nil {
		return fmt.Errorf("ZCode OAuth returned an unsafe authorization URL: %w", err)
	}

	announceLogin(announce, "Open: "+init.AuthorizeURL)
	announceLogin(announce, "Approve sign-in in the new browser tab, then keep this page open.")

	interval := time.Duration(float64(init.PollIntervalSec) * float64(time.Second))
	if interval <= 0 {
		interval = defaultPollInterval
	}
	deadline := time.Now().Add(loginTimeout)
	if init.ExpiresAt > 0 {
		expires := time.Unix(init.ExpiresAt, 0)
		if !expires.After(time.Now()) {
			return errors.New("ZCode OAuth authorization flow has already expired")
		}
		if expires.Before(deadline) {
			deadline = expires
		}
	}

	for time.Now().Before(deadline) {
		if err := waitForContext(ctx, interval); err != nil {
			return err
		}
		var poll pollData
		path := "/api/v1/oauth/cli/poll/" + url.PathEscape(init.FlowID)
		if err := m.oauthJSON(ctx, http.MethodGet, path, pollToken, nil, &poll); err != nil {
			return fmt.Errorf("poll ZCode OAuth: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(poll.Status)) {
		case "pending", "":
			continue
		case "failed", "expired", "denied":
			return errors.New("ZCode OAuth authorization was not completed")
		case "ready":
			if strings.TrimSpace(poll.Token) == "" {
				return errors.New("ZCode OAuth response omitted the session token")
			}
			credentials := &Credentials{AccessToken: strings.TrimSpace(poll.Token)}
			credentials.ExpiresAt, _ = jwtExpiration(credentials.AccessToken)
			if err := m.Store.Save(credentials); err != nil {
				return fmt.Errorf("save ZCode credentials: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("ZCode OAuth returned unknown status %q", poll.Status)
		}
	}
	return errors.New("ZCode OAuth authorization timed out")
}

func (m *Manager) oauthJSON(ctx context.Context, method, path, pollToken string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.Issuer, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+pollToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := m.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var envelope oauthEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		if envelope.Msg == "" {
			return fmt.Errorf("API code %d", envelope.Code)
		}
		return fmt.Errorf("API code %d: %s", envelope.Code, envelope.Msg)
	}
	if out != nil && len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return err
		}
	}
	return nil
}

type flexibleFloat float64

func (n *flexibleFloat) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		*n = flexibleFloat(parsed)
		return nil
	}
	var parsed float64
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*n = flexibleFloat(parsed)
	return nil
}

func randomPollToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func validateAuthorizeURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("authorization URL must be an HTTPS URL without embedded credentials")
	}
	return nil
}

func announceLogin(announce func(string), message string) {
	if announce != nil {
		announce(message)
	}
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func jwtExpiration(token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, errors.New("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, err
	}
	var claims map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return 0, err
	}
	switch value := claims["exp"].(type) {
	case json.Number:
		return value.Int64()
	case float64:
		return int64(value), nil
	default:
		return 0, nil
	}
}

var _ interface {
	AccessToken(context.Context) (string, error)
} = (*Manager)(nil)
