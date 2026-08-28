package server

import (
	"fmt"
	"html"
	"net/http"
	"time"
)

func (s *Server) workBuddyLoginPage(w http.ResponseWriter, _ *http.Request) {
	if s.workBuddyAuth == nil {
		http.Error(w, "WorkBuddy account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in to WorkBuddy</title><style>body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px;box-shadow:0 12px 32px #18223012}h1{margin:0 0 12px;font-size:28px}p{line-height:1.55;color:#526174}.btn{display:inline-block;border:0;border-radius:10px;background:#1769e0;color:white;padding:12px 18px;font-size:16px;cursor:pointer}.muted{color:#68778b;font-size:14px}</style></head><body><main class="wrap"><section class="card"><h1>Sign in to WorkBuddy</h1><p>Connect your WorkBuddy account directly to this proxy. The browser authorization supports the Google and GitHub sign-in methods offered by WorkBuddy.</p><form method="post"><button class="btn" type="submit">Sign in with WorkBuddy</button></form><p class="muted">The resulting session is stored locally and refreshed automatically. <a href="/">Back to dashboard</a></p></section></main></body></html>`)
}

func (s *Server) workBuddyLogin(w http.ResponseWriter, r *http.Request) {
	if s.workBuddyAuth == nil {
		http.Error(w, "WorkBuddy account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	flusher, canFlush := w.(http.Flusher)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Signing in to WorkBuddy</title><style>body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px}.log{margin-top:20px;padding:14px;background:#f2f5f9;border-radius:10px;line-height:1.7}.ok{color:#117a54}.err{color:#b42318}a{color:#1769e0}</style></head><body><main class="wrap"><section class="card"><h1>Waiting for WorkBuddy authorization</h1><div class="log">`)
	if canFlush {
		flusher.Flush()
	}
	state, target, err := s.workBuddyAuth.StartLogin(r.Context())
	if err != nil {
		_, _ = fmt.Fprintf(w, `</div><p class="err">Sign-in failed: %s</p></section></main></body></html>`, html.EscapeString(err.Error()))
		return
	}
	_, _ = fmt.Fprintf(w, `<div><a href="%s" target="_blank" rel="noreferrer">Continue to WorkBuddy</a></div><div>Complete sign-in in the new tab, then return here.</div>`, html.EscapeString(target))
	if canFlush {
		flusher.Flush()
	}
	ticker := time.NewTicker(2 * time.Second)
	timeout := time.NewTimer(5 * time.Minute)
	defer ticker.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			done, pollErr := s.workBuddyAuth.PollLogin(r.Context(), state)
			if pollErr != nil {
				_, _ = fmt.Fprintf(w, `</div><p class="err">Sign-in failed: %s</p><p><a href="/login/workbuddy">Try again</a></p></section></main></body></html>`, html.EscapeString(pollErr.Error()))
				return
			}
			if done {
				_, _ = fmt.Fprint(w, `</div><p class="ok">Sign-in successful. Your WorkBuddy account is ready.</p><p><a href="/">Open dashboard</a></p></section></main></body></html>`)
				if canFlush {
					flusher.Flush()
				}
				return
			}
			_, _ = fmt.Fprint(w, "<!-- waiting -->")
			if canFlush {
				flusher.Flush()
			}
		case <-timeout.C:
			_, _ = fmt.Fprint(w, `</div><p class="err">Sign-in timed out.</p><p><a href="/login/workbuddy">Try again</a></p></section></main></body></html>`)
			return
		case <-r.Context().Done():
			return
		}
	}
}
