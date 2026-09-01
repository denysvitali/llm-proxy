package server

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"time"
)

func (s *Server) zcodeLoginPage(w http.ResponseWriter, _ *http.Request) {
	if s.zcodeAuth == nil {
		http.Error(w, "ZCode account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	// The CAPTCHA SDK runs in the user's browser and needs to contact Aliyun
	// directly. This page is still a tightly scoped login surface; the more
	// restrictive global login policy remains in effect for other providers.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://*.alicdn.com https://*.aliyuncs.com 'unsafe-inline' 'unsafe-eval'; connect-src 'self' https://*.aliyuncs.com https://*.alicdn.com; frame-src https://*.aliyuncs.com https://*.alicdn.com; img-src 'self' data: https://*.alicdn.com https://*.aliyuncs.com; style-src 'self' https://*.alicdn.com https://*.aliyuncs.com 'unsafe-inline'; font-src 'self' data: https://*.alicdn.com https://*.aliyuncs.com; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; worker-src blob:")
	_, _ = fmt.Fprint(w, `<script>window.AliyunCaptchaConfig={region:'sgp',prefix:'no8xfe'};</script>`)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in to ZCode</title><style>
body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px;box-shadow:0 12px 32px #18223012}h1{margin:0 0 12px;font-size:28px}h2{font-size:19px;margin:28px 0 8px}p{line-height:1.55;color:#526174}.btn{display:inline-block;border:0;border-radius:10px;background:#1769e0;color:white;padding:12px 18px;font-size:16px;cursor:pointer}.btn:disabled{opacity:.55;cursor:wait}.muted{color:#68778b;font-size:14px}.steps{padding:12px 0 20px}.step{margin:12px 0}.step b{display:block;margin-bottom:3px}.captcha{border-top:1px solid #e5e9f0;padding-top:12px}.status{min-height:24px;color:#526174}.ok{color:#117a54}.err{color:#b42318}</style></head><body><main class="wrap"><section class="card"><h1>Sign in to ZCode</h1><p>Connect your ZCode account to use the Start Plan through this proxy. No JWT or upstream API key is needed in your configuration; the session is stored locally for this proxy.</p><div class="steps"><div class="step"><b>1. Start sign-in</b><span class="muted">Click the button to create a one-time browser authorization.</span></div><div class="step"><b>2. Approve in ZCode</b><span class="muted">Sign in with the account that has your Start Plan offer.</span></div><div class="step"><b>3. Return here</b><span class="muted">Keep this page open until the session is ready.</span></div></div><form method="post"><button class="btn" type="submit">Sign in with ZCode</button></form><div class="captcha"><h2>Browser verification</h2><p class="muted">ZCode requires a short-lived browser verification before model requests. Run this in the same browser that can access this proxy.</p><div id="zcode-captcha"></div><button class="btn" id="verify-captcha" type="button">Verify browser session</button><p class="status" id="captcha-status" aria-live="polite"></p></div><p class="muted"><a href="/">Back to dashboard</a></p></section></main><script src="https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js"></script><script>
(function(){
  var button=document.getElementById('verify-captcha');
  var status=document.getElementById('captcha-status');
  var captcha=null;
  var initialized=false;
  var startRequested=false;
  var verificationActive=false;
	var sdkButton=document.createElement('button');
	sdkButton.id='zcode-captcha-sdk-trigger';sdkButton.hidden=true;sdkButton.type='button';document.body.appendChild(sdkButton);
  function setStatus(message, className){status.textContent=message;status.className='status '+(className||'');}
  function startVerification(){
	if(verificationActive)return;
    if(!initialized||!captcha){startRequested=true;setStatus('Browser verification is loading; it will start automatically when ready.');return;}
    startRequested=false;verificationActive=true;button.disabled=true;setStatus('Starting browser verification…');
    try{captcha.startTracelessVerification();}catch(error){setStatus('Could not start verification: '+error.message,'err');button.disabled=false;}
  }
  if(typeof window.initAliyunCaptcha!=='function'){setStatus('Aliyun CAPTCHA SDK did not load. Check browser network access and retry.','err');return;}
  var initialization;
  try{
    initialization=window.initAliyunCaptcha({
      SceneId:'11xygtvd',mode:'popup',region:'sgp',prefix:'no8xfe',
	  element:'#zcode-captcha',button:'#zcode-captcha-sdk-trigger',
      captchaLogoImg:'',showErrorTip:true,delayBeforeSuccess:false,
      getInstance:function(instance){captcha=instance;initialized=true;if(startRequested)startVerification();else if(!verificationActive&&!button.disabled)setStatus('Browser verification is ready.');},
      success:function(param){
        setStatus('Verification received. Saving it to the proxy…');
        fetch('/login/zcode/captcha',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({verify_param:param})})
          .then(function(response){return response.json().then(function(body){return {ok:response.ok,body:body};});})
          .then(function(result){if(!result.ok)throw new Error(result.body.error||'proxy rejected the verification');verificationActive=false;setStatus('Browser verification saved. It is valid for about 40 seconds.','ok');button.disabled=false;})
          .catch(function(error){startRequested=false;verificationActive=false;setStatus('Could not save verification: '+error.message,'err');button.disabled=false;});
      },
      fail:function(){startRequested=false;verificationActive=false;setStatus('Browser verification failed. Retry when ready.','err');button.disabled=false;},
      onError:function(){startRequested=false;verificationActive=false;setStatus('Browser verification failed. Retry when ready.','err');button.disabled=false;}
    });
    if(initialization&&typeof initialization.catch==='function')initialization.catch(function(error){startRequested=false;verificationActive=false;setStatus('Could not initialize verification: '+error.message,'err');button.disabled=false;});
  }catch(error){startRequested=false;verificationActive=false;setStatus('Could not initialize verification: '+error.message,'err');button.disabled=false;}
  window.setTimeout(function(){if(!initialized){setStatus('Browser verification is taking longer than expected. Keep this page open or reload and retry.','err');button.disabled=false;}},30000);
  button.addEventListener('click',startVerification);
})();
</script></body></html>`)
}

// zcodeCaptcha accepts the short-lived verification parameter produced by the
// browser SDK on the ZCode login page. It deliberately does not persist it.
func (s *Server) zcodeCaptcha(w http.ResponseWriter, r *http.Request) {
	if s.zcodeAuth == nil {
		http.Error(w, "ZCode account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	var request struct {
		VerifyParam string `json:"verify_param"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid CAPTCHA verification request"})
		return
	}
	if err := s.zcodeAuth.SetCaptchaVerifyParamContext(r.Context(), request.VerifyParam); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) zcodeLogin(w http.ResponseWriter, r *http.Request) {
	if s.zcodeAuth == nil {
		http.Error(w, "ZCode account sign-in is unavailable", http.StatusServiceUnavailable)
		return
	}
	loginHeaders(w)
	flusher, canFlush := w.(http.Flusher)
	_, _ = fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Signing in to ZCode</title><style>body{font:16px system-ui,sans-serif;background:#f5f7fb;color:#182230;margin:0}.wrap{max-width:560px;margin:10vh auto;padding:24px}.card{background:white;border:1px solid #dfe4ec;border-radius:18px;padding:32px}h1{margin:0 0 12px}.log{margin-top:20px;padding:14px;background:#f2f5f9;border-radius:10px;line-height:1.7}a{color:#1769e0}.ok{color:#117a54}.err{color:#b42318}</style></head><body><main class="wrap"><section class="card"><h1>Waiting for ZCode authorization</h1><p>Keep this page open while you approve the sign-in.</p><div class="log">`)
	if canFlush {
		flusher.Flush()
	}
	messages := make(chan string, 8)
	result := make(chan error, 1)
	go func() {
		result <- s.zcodeAuth.LoginDevice(r.Context(), func(message string) {
			select {
			case messages <- message:
			case <-r.Context().Done():
			}
		})
	}()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case message := <-messages:
			writeLoginMessage(w, message)
		case err := <-result:
			if err != nil {
				_, _ = fmt.Fprintf(w, `</div><p class="err">Sign-in failed: %s</p><p><a href="/login/zcode">Try again</a> · <a href="/">Dashboard</a></p></section></main></body></html>`, html.EscapeString(err.Error()))
			} else {
				_, _ = fmt.Fprint(w, `</div><p class="ok">Sign-in successful. Your ZCode Start Plan is ready.</p><p><a href="/login/zcode">Open browser verification</a> · <a href="/">Dashboard</a></p></section></main></body></html>`)
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
