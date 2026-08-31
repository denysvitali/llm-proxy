package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

func loginHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) grokLoginPage(w http.ResponseWriter, _ *http.Request) {
	if s.grokAuth == nil {
		http.Error(w, "Grok account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in to Grok</title><style>
body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px;box-shadow:0 12px 32px #18223012}h1{margin:0 0 12px;font-size:28px}p{line-height:1.55;color:#526174}.btn{display:inline-block;border:0;border-radius:10px;background:#1769e0;color:white;padding:12px 18px;font-size:16px;cursor:pointer;text-decoration:none}.muted{color:#68778b;font-size:14px}.steps{padding:12px 0 20px}.step{margin:12px 0}.step b{display:block;margin-bottom:3px}.log{margin-top:20px;padding:14px;background:#f2f5f9;border-radius:10px;line-height:1.7}.ok{color:#117a54}.err{color:#b42318}</style></head><body><main class="wrap"><section class="card"><h1>Sign in to Grok</h1><p>Connect your xAI account to use its coding subscription through this proxy. Your session is stored locally for this proxy and refreshed when needed.</p><div class="steps"><div class="step"><b>1. Start sign-in</b><span class="muted">Click the button to begin xAI device authorization.</span></div><div class="step"><b>2. Approve in xAI</b><span class="muted">Sign in with the account that has your coding subscription.</span></div><div class="step"><b>3. Return here</b><span class="muted">This page will confirm when the session is ready.</span></div></div><form method="post"><button class="btn" type="submit">Sign in with xAI</button></form><p class="muted"><a href="/">Back to dashboard</a></p></section></main></body></html>`)
}

func (s *Server) grokLogin(w http.ResponseWriter, r *http.Request) {
	if s.grokAuth == nil {
		http.Error(w, "Grok account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	flusher, canFlush := w.(http.Flusher)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Signing in to Grok</title><style>body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px}h1{margin:0 0 12px}.log{margin-top:20px;padding:14px;background:#f2f5f9;border-radius:10px;line-height:1.7}a{color:#1769e0}.ok{color:#117a54}.err{color:#b42318}</style></head><body><main class="wrap"><section class="card"><h1>Waiting for xAI authorization</h1><p>Keep this page open while you approve the sign-in.</p><div class="log">`)
	if canFlush {
		flusher.Flush()
	}
	messages := make(chan string, 8)
	result := make(chan error, 1)
	go func() { result <- s.grokAuth.LoginDevice(r.Context(), func(message string) { messages <- message }) }()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case message := <-messages:
			writeLoginMessage(w, message)
		case err := <-result:
			if err != nil {
				_, _ = fmt.Fprintf(w, `</div><p class="err">Sign-in failed: %s</p><p><a href="/login">Try again</a> · <a href="/">Dashboard</a></p></section></main></body></html>`, html.EscapeString(err.Error()))
			} else {
				_, _ = fmt.Fprint(w, `</div><p class="ok">Sign-in successful. Your xAI account session is ready.</p><p><a href="/">Open dashboard</a></p></section></main></body></html>`)
			}
			if canFlush {
				flusher.Flush()
			}
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, "<!-- waiting -->")
		case <-r.Context().Done():
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

func writeLoginMessage(w http.ResponseWriter, message string) {
	if target, ok := strings.CutPrefix(message, "Open: "); ok {
		escaped := html.EscapeString(target)
		_, _ = fmt.Fprintf(w, `<div><a href="%s" target="_blank" rel="noreferrer">Continue in browser</a></div>`, escaped)
		return
	}
	_, _ = fmt.Fprintf(w, `<div>%s</div>`, html.EscapeString(message))
}
