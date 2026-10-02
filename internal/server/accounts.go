package server

import (
	"net/http"

	"github.com/denysvitali/llm-proxy/internal/backend"
	codexbackend "github.com/denysvitali/llm-proxy/internal/backend/codex"
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

// AccountProviders shares each subscription account between its backend and
// browser routes. Nil managers leave the corresponding account unavailable.
// Concrete managers keep each provider's distinct login and usage APIs explicit.
type AccountProviders struct {
	Grok        *grokbackend.Manager
	WorkBuddy   *workbuddybackend.Manager
	Codex       *codexbackend.Manager
	ZCode       *zcodebackend.Manager
	MiniMaxCode *minimaxcodebackend.Manager
}

// accountSession is the small capability shared by subscription accounts; it
// deliberately does not add authentication concerns to backend.Backend.
type accountSession interface {
	backend.TokenSource
	HasSession() bool
}

type accountProvider struct {
	label          string
	session        accountSession
	registerRoutes func(*Server, *http.ServeMux)
}

func (p accountProvider) configured() bool {
	return p.session != nil && p.session.HasSession()
}

func (a AccountProviders) providers() map[string]*accountProvider {
	providers := map[string]*accountProvider{
		"grok":         {label: "xAI account", registerRoutes: (*Server).registerGrokRoutes},
		"workbuddy":    {label: "WorkBuddy account", registerRoutes: (*Server).registerWorkBuddyRoutes},
		"codex":        {label: "ChatGPT account", registerRoutes: (*Server).registerCodexRoutes},
		"zcode":        {label: "ZCode account", registerRoutes: (*Server).registerZCodeRoutes},
		"minimax-code": {label: "MiniMax Code account", registerRoutes: (*Server).registerMiniMaxCodeRoutes},
	}
	// Check concrete pointers before putting them into the capability interface:
	// an absent manager must remain a nil TokenSource, not a typed nil.
	if a.Grok != nil {
		providers["grok"].session = a.Grok
	}
	if a.WorkBuddy != nil {
		providers["workbuddy"].session = a.WorkBuddy
	}
	if a.Codex != nil {
		providers["codex"].session = a.Codex
	}
	if a.ZCode != nil {
		providers["zcode"].session = a.ZCode
	}
	if a.MiniMaxCode != nil {
		providers["minimax-code"].session = a.MiniMaxCode
	}
	return providers
}

// TokenSources returns the same manager instances used for browser sign-in and
// account status. API-key providers and absent managers have no token source.
func (a AccountProviders) TokenSources() map[string]backend.TokenSource {
	sources := make(map[string]backend.TokenSource)
	for name, provider := range a.providers() {
		if provider.session != nil {
			sources[name] = provider.session
		}
	}
	return sources
}

func (s *Server) registerGrokRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", s.grokLoginPage)
	mux.HandleFunc("POST /login", s.grokLogin)
	mux.HandleFunc("GET /api/grok/usage", s.handleGrokUsage)
}

func (s *Server) registerWorkBuddyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/workbuddy", s.workBuddyLoginPage)
	mux.HandleFunc("POST /login/workbuddy", s.workBuddyLogin)
}

func (s *Server) registerCodexRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/codex", s.codexLoginPage)
	mux.HandleFunc("POST /login/codex", s.codexLogin)
}

func (s *Server) registerZCodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/zcode", s.zcodeLoginPage)
	mux.HandleFunc("POST /login/zcode", s.zcodeLogin)
	mux.HandleFunc("POST /login/zcode/captcha", s.zcodeCaptcha)
	mux.HandleFunc("GET /api/zcode/usage", s.handleZcodeUsage)
	mux.HandleFunc("GET /api/zcode/quota", s.handleZcodeQuota)
	mux.HandleFunc("GET /api/zcode/balance", s.handleZcodeQuota)
	mux.HandleFunc("POST /api/zcode/claim", s.zcodeClaim)
	mux.HandleFunc("GET /api/zcode/offers", s.zcodeOffers)
}

func (s *Server) registerMiniMaxCodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/minimax-code", s.minimaxCodeLoginPage)
	mux.HandleFunc("POST /login/minimax-code", s.minimaxCodeLogin)
	mux.HandleFunc("GET /api/minimax-code/usage", s.handleMiniMaxCodeUsage)
	mux.Handle("POST /api/minimax-code/checkin", http.NewCrossOriginProtection().Handler(http.HandlerFunc(s.handleMiniMaxCodeCheckin)))
}
