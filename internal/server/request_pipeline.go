package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// openAIEnvelope is the minimal request envelope shared by both OpenAI
// endpoints; everything else in the body is forwarded untouched.
type openAIEnvelope struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func decodeOpenAIEnvelope(body []byte) (openAIEnvelope, error) {
	var env openAIEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return openAIEnvelope{}, err
	}
	return env, nil
}

// handleOpenAIRequest shares envelope validation and routing for the two
// OpenAI APIs. Their response and stream error shapes stay with the dialect;
// Anthropic keeps its own model validation and inbound tool-error accounting.
func (s *Server) handleOpenAIRequest(w http.ResponseWriter, r *http.Request, kind backend.Kind, dialect clientDialect) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	envelope, err := decodeOpenAIEnvelope(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "request body is not valid JSON")
		return
	}
	_, reasoningEffort, selectorErr := normalizeCodexModelSelector(envelope.Model)
	if selectorErr != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", selectorErr.Error())
		return
	}
	rt, found := s.resolveChain(r.Context(), envelope.Model)
	if !found {
		writeOpenAIModelNotFound(w, envelope.Model)
		return
	}

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    rt[0].backend.Name(),
	})
	env := translateEnv{
		kind:            kind,
		body:            body,
		clientModel:     envelope.Model,
		streaming:       envelope.Stream,
		reasoningEffort: reasoningEffort,
	}
	s.exchangeChain(w, r, log, rt, dialect, env)
}

// prepareRouteRequest encodes a request for one fallback route. Native
// requests only rewrite the model, preserving provider-specific fields and
// original bytes. Translation adds the inbound protocol's validation and
// response options before using the selected translation path.
func prepareRouteRequest(rt route, wire resolvedWire, env *translateEnv) ([]byte, error) {
	if wire.native {
		rewritten, err := rewriteModel(env.body, rt.model)
		if err != nil {
			return nil, errors.New("request body is not valid JSON")
		}
		return rewritten, nil
	}
	if env.kind == backend.KindAnthropic {
		request, err := translate.ParseRequest(env.body)
		if err != nil {
			return nil, fmt.Errorf("invalid Anthropic Messages request: %v", err)
		}
		env.thinking = wantsThinking(request)
	}
	payload, err := wire.path.encode(*env)
	if err != nil {
		if env.kind == backend.KindAnthropic {
			return nil, fmt.Errorf("translating request for backend %s failed: %v", rt.backend.Name(), err)
		}
		return nil, fmt.Errorf("cannot translate request for backend %s: %v", rt.backend.Name(), err)
	}
	return payload, nil
}
