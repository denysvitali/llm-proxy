package server

import (
	"fmt"
	"io"
	"net/http"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// handleResponses serves POST /v1/responses (OpenAI Responses API).
// Responses-native backends get the request passed through with only the
// model rewritten; chat-only backends (e.g. Venice) get the request
// translated to Chat Completions and their replies converted back into
// Responses shape — required because Codex no longer supports wire_api="chat".
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	env, err := decodeOpenAIEnvelope(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	route, found := s.resolve(r.Context(), env.Model)
	if !found {
		writeOpenAIModelNotFound(w, env.Model)
		return
	}

	switch {
	case route.backend.Supports(backend.KindOpenAIResponses):
		s.responsesPassthrough(w, r, route, body, env.Stream)
	case route.backend.Supports(backend.KindOpenAIChat):
		s.responsesViaChat(w, r, route, body, env.Stream)
	default:
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("backend %s does not accept the Responses API for model %q",
				route.backend.Name(), env.Model))
	}
}

// responsesPassthrough forwards a Responses request verbatim (model rewritten).
func (s *Server) responsesPassthrough(w http.ResponseWriter, r *http.Request, route route, body []byte, stream bool) {
	tr := s.stats.track(route.backend.Name(), route.model)
	defer tr.done()
	rewritten, err := rewriteModel(body, route.model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	resp, err := s.sendOpenAIRequest(r, route.backend, backend.KindOpenAIResponses, route.model, rewritten, stream)
	if err != nil {
		s.failOpenAIBackend(w, route.backend, err)
		return
	}
	tr.setUpstreamStatus(resp.Status)
	if resp.Status < 200 || resp.Status > 299 {
		relayOpenAIUpstreamError(w, resp)
		return
	}
	sn := wrapUpstreamBody(tr, resp, stream)
	defer func() { sn.Finish(); _ = sn.Close() }()
	relayOpenAIBody(w, resp)
}

// responsesViaChat translates a Responses request into Chat Completions,
// sends it to a chat-only backend, and converts the reply back.
func (s *Server) responsesViaChat(w http.ResponseWriter, r *http.Request, route route, body []byte, stream bool) {
	tr := s.stats.track(route.backend.Name(), route.model)
	defer tr.done()
	translated, err := translate.ResponsesToChat(body, route.model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("cannot translate request for backend %s: %v", route.backend.Name(), err))
		return
	}
	resp, err := s.sendOpenAIRequest(r, route.backend, backend.KindOpenAIChat, route.model, translated, stream)
	if err != nil {
		s.failOpenAIBackend(w, route.backend, err)
		return
	}
	tr.setUpstreamStatus(resp.Status)
	if resp.Status < 200 || resp.Status > 299 {
		relayOpenAIUpstreamError(w, resp)
		return
	}
	sn := wrapUpstreamBody(tr, resp, stream)
	defer func() { sn.Finish(); _ = sn.Close() }()
	if !stream {
		s.relayResponsesFromChatBody(w, resp, route.backend.Name(), route.model)
		return
	}
	copyUpstreamRequestID(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := flusherFor(w)
	streamWriter := translate.NewResponsesStreamFromChat(w, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, route.model)
	_ = streamWriter.Consume(resp.Body)
	streamWriter.Finish()
	_ = resp.Body.Close()
}

// relayResponsesFromChatBody buffers a chat-completions reply, converts it to
// a Responses response object, and writes it as JSON.
func (s *Server) relayResponsesFromChatBody(w http.ResponseWriter, resp *backend.Response, backendName, model string) {
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTranslatedResponse+1))
	if err != nil {
		s.log.WithError(err).WithField("backend", backendName).Warn("failed reading upstream response")
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "failed reading upstream response")
		return
	}
	if int64(len(data)) > maxTranslatedResponse {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream response too large")
		return
	}
	out, err := translate.ResponsesFromChat(data, model)
	if err != nil {
		s.log.WithError(err).WithField("backend", backendName).Warn("failed translating upstream response")
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream returned an unreadable response")
		return
	}
	copyUpstreamRequestID(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
