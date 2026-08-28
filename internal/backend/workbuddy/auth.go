package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const loginTTL = 5 * time.Minute

type Credentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
	Domain       string `json:"domain,omitempty"`
	UserID       string `json:"-"`
	EnterpriseID string `json:"-"`
}
type account struct {
	UID          string `json:"uid"`
	EnterpriseID string `json:"enterpriseId,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
}
type storedSession struct {
	Auth    Credentials `json:"auth"`
	Account account     `json:"account"`
}
type loginState struct {
	client  *http.Client
	expires time.Time
}

type Manager struct {
	Path    string
	BaseURL string
	HTTP    *http.Client
	mu      sync.Mutex
	loginMu sync.Mutex
	logins  map[string]*loginState
}

func NewManager(path string) *Manager {
	return &Manager{Path: path, BaseURL: defaultBaseURL, HTTP: newAuthClient(), logins: map[string]*loginState{}}
}
func NewSession(path string) *Manager { return NewManager(path) }
func newAuthClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 20, IdleConnTimeout: 90 * time.Second}}
}

func (m *Manager) HasSession() bool {
	session, err := m.load()
	return err == nil && session.Auth.AccessToken != ""
}
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	c, err := m.Credentials(ctx)
	return c.AccessToken, err
}
func (m *Manager) Credentials(ctx context.Context) (Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.load()
	if err != nil {
		return Credentials{}, err
	}
	if session.Auth.ExpiresAt > 0 && session.Auth.ExpiresAt <= time.Now().Add(5*time.Minute).Unix() {
		if session.Auth.RefreshToken == "" {
			return Credentials{}, errors.New("WorkBuddy session expired; sign in again from the dashboard")
		}
		if err = m.refresh(ctx, &session); err != nil {
			return Credentials{}, err
		}
		if err = m.save(session); err != nil {
			return Credentials{}, err
		}
	}
	c := session.Auth
	c.UserID = session.Account.UID
	c.EnterpriseID = session.Account.EnterpriseID
	return c, nil
}
func (m *Manager) load() (storedSession, error) {
	b, err := os.ReadFile(m.Path)
	if errors.Is(err, os.ErrNotExist) {
		return storedSession{}, errors.New("not signed in to WorkBuddy; use the dashboard to sign in")
	}
	if err != nil {
		return storedSession{}, err
	}
	var session storedSession
	if err = json.Unmarshal(b, &session); err != nil {
		return session, fmt.Errorf("decode WorkBuddy session: %w", err)
	}
	if session.Auth.AccessToken == "" {
		return session, errors.New("WorkBuddy session has no access token; sign in again")
	}
	return session, nil
}
func (m *Manager) save(session storedSession) error {
	if err := os.MkdirAll(filepath.Dir(m.Path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := m.Path + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, m.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(m.Path, 0600)
}

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}
type stateData struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}
type tokenData struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	Domain       string `json:"domain"`
}

func (m *Manager) StartLogin(ctx context.Context) (string, string, error) {
	client := newAuthClient()
	var state stateData
	if err := m.do(ctx, client, http.MethodPost, "/v2/plugin/auth/state?platform=CLI", nil, bytes.NewReader([]byte("{}")), &state); err != nil {
		return "", "", fmt.Errorf("start WorkBuddy login: %w", err)
	}
	if state.State == "" || state.AuthURL == "" {
		return "", "", errors.New("WorkBuddy login response omitted state or authorization URL")
	}
	m.loginMu.Lock()
	m.logins[state.State] = &loginState{client: client, expires: time.Now().Add(loginTTL)}
	m.loginMu.Unlock()
	return state.State, state.AuthURL, nil
}
func (m *Manager) PollLogin(ctx context.Context, state string) (bool, error) {
	m.loginMu.Lock()
	login := m.logins[state]
	m.loginMu.Unlock()
	if login == nil {
		return false, errors.New("unknown WorkBuddy login; start again")
	}
	if time.Now().After(login.expires) {
		m.deleteLogin(state)
		return false, errors.New("WorkBuddy login expired; start again")
	}
	var token tokenData
	escapedState := url.QueryEscape(state)
	if err := m.do(ctx, login.client, http.MethodGet, "/v2/plugin/auth/token?state="+escapedState, nil, nil, &token); err != nil {
		return false, nil
	}
	if token.AccessToken == "" {
		return false, nil
	}
	var acct account
	headers := http.Header{"Authorization": []string{"Bearer " + token.AccessToken}}
	_ = m.do(ctx, login.client, http.MethodGet, "/v2/plugin/login/account?state="+escapedState, headers, nil, &acct)
	session := storedSession{Auth: Credentials{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix(), Domain: token.Domain}, Account: acct}
	if err := m.save(session); err != nil {
		return false, err
	}
	m.deleteLogin(state)
	return true, nil
}
func (m *Manager) deleteLogin(state string) {
	m.loginMu.Lock()
	delete(m.logins, state)
	m.loginMu.Unlock()
}
func (m *Manager) refresh(ctx context.Context, session *storedSession) error {
	var token tokenData
	headers := http.Header{"X-Refresh-Token": []string{session.Auth.RefreshToken}, "X-Auth-Refresh-Source": []string{"workbuddy"}}
	if session.Account.EnterpriseID != "" {
		headers.Set("X-Enterprise-Id", session.Account.EnterpriseID)
	}
	if err := m.do(ctx, m.HTTP, http.MethodPost, "/v2/plugin/auth/token/refresh", headers, nil, &token); err != nil {
		return fmt.Errorf("refresh WorkBuddy session: %w", err)
	}
	if token.AccessToken == "" {
		return errors.New("refresh WorkBuddy session: response omitted access token")
	}
	session.Auth.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		session.Auth.RefreshToken = token.RefreshToken
	}
	if token.Domain != "" {
		session.Auth.Domain = token.Domain
	}
	session.Auth.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
	return nil
}
func (m *Manager) do(ctx context.Context, client *http.Client, method, path string, headers http.Header, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://www.codebuddy.cn")
	req.Header.Set("Referer", "https://www.codebuddy.cn/")
	req.Header.Set("User-Agent", "CLI/"+clientVersion+" CodeBuddy/"+clientVersion)
	for k, values := range headers {
		for _, value := range values {
			req.Header.Add(k, value)
		}
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
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var env envelope
	if err = json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Code != 0 {
		return fmt.Errorf("code %d: %s", env.Code, env.Msg)
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}
