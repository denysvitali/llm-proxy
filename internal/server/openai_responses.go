package server

import (
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// handleResponses serves POST /v1/responses (OpenAI Responses API, e.g.
// Codex). The translation matrix picks how the request reaches its backend —
// natively when the backend speaks Responses, translated to chat or Anthropic
// Messages otherwise — and exchange relays the response back in Responses
// shape either way.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	envelope, err := decodeOpenAIEnvelope(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON")
		return
	}
	rt, found := s.resolve(r.Context(), envelope.Model)
	if !found {
		writeOpenAIModelNotFound(w, envelope.Model)
		return
	}

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    rt.backend.Name(),
	})
	env := translateEnv{
		kind:        backend.KindOpenAIResponses,
		body:        body,
		model:       rt.model,
		clientModel: envelope.Model,
		streaming:   envelope.Stream,
	}
	wire, servable := resolveWire(env.kind, rt.backend, rt.model)
	if !servable {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("backend %s does not support the Responses API for model %q",
				rt.backend.Name(), envelope.Model))
		return
	}

	var payload []byte
	if wire.native {
		rewritten, err := rewriteModel(body, rt.model)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
				"request body is not valid JSON")
			return
		}
		payload = rewritten
	} else {
		translated, err := wire.path.encode(env)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("cannot translate request for backend %s: %v", rt.backend.Name(), err))
			return
		}
		payload = translated
	}
	s.exchange(w, r, log, rt, openAIResponsesDialect(), wire, payload, r.Header.Clone(), env)
}
