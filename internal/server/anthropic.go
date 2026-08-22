package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

const (
	// maxTranslatedResponseBody caps how much of a non-streaming upstream
	// response is buffered before translation (16 MiB).
	maxTranslatedResponseBody = 16 << 20
	// passthroughCopyBufferSize is the io.Copy buffer used when relaying a
	// native Anthropic upstream response to the client.
	passthroughCopyBufferSize = 32 << 10
)

// anthropicStreamWriter is the shared shape of the translate package's SSE
// translators (StreamWriter and ResponsesStreamWriter).
type anthropicStreamWriter interface {
	Consume(io.Reader) error
	Finish()
}

// handleMessages serves POST /v1/messages (Anthropic Messages API). Requests
// are forwarded natively when the routed backend speaks Anthropic, otherwise
// they are translated to the best wire format the backend supports and the
// response is translated back so clients always see pure Anthropic.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	if bodyTooLarge(body, s.cfg.Server.MaxBodyBytes) {
		writeAnthropicError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large")
		return
	}

	var envelope struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "request body must be a JSON object")
		return
	}
	if strings.TrimSpace(envelope.Model) == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	route, ok := s.resolve(r.Context(), envelope.Model)
	if !ok {
		writeAnthropicError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("model %q has no available backend", envelope.Model))
		return
	}

	// Errored tool results arrive one turn after the call they answer;
	// counting them here is what makes the tool-call error rate meaningful.
	s.stats.recordInboundToolErrors(body, route.backend.Name(), route.model)

	log := s.log.WithFields(logrus.Fields{
		"request_id": RequestID(r.Context()),
		"model":      envelope.Model,
		"backend":    route.backend.Name(),
	})
	header := r.Header.Clone()

	switch {
	case route.backend.Supports(backend.KindAnthropic):
		forwarded, err := rewriteModel(body, route.model)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("rewriting model field failed: %v", err))
			return
		}
		s.forwardNative(w, r, log, route, forwarded, header, envelope.Stream)

	case route.backend.Supports(backend.KindOpenAIChat):
		request, err := translate.ParseRequest(body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("invalid Anthropic Messages request: %v", err))
			return
		}
		payload, err := translate.ToOpenAI(request, route.model)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("translating request to chat-completions failed: %v", err))
			return
		}
		thinking := wantsThinking(request)
		s.forwardTranslated(w, r, log, route, backend.KindOpenAIChat, payload, header, envelope.Stream,
			func(client io.Writer, flush func()) anthropicStreamWriter {
				return translate.NewStreamWriter(client, flush, envelope.Model, thinking)
			},
			func(data []byte) ([]byte, error) {
				return translate.FromOpenAI(data, envelope.Model, thinking)
			})

	case route.backend.Supports(backend.KindOpenAIResponses):
		request, err := translate.ParseRequest(body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("invalid Anthropic Messages request: %v", err))
			return
		}
		payload, err := translate.ToResponses(request, route.model)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("translating request to Responses API failed: %v", err))
			return
		}
		thinking := wantsThinking(request)
		s.forwardTranslated(w, r, log, route, backend.KindOpenAIResponses, payload, header, envelope.Stream,
			func(client io.Writer, flush func()) anthropicStreamWriter {
				return translate.NewResponsesStreamWriter(client, flush, envelope.Model, thinking)
			},
			func(data []byte) ([]byte, error) {
				return translate.FromResponses(data, envelope.Model, thinking)
			})

	default:
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "no translation path for backend")
	}
}

// wantsThinking reports whether a successfully parsed Anthropic request asked
// for extended thinking; nil requests never do.
func wantsThinking(request *translate.Request) bool {
	if request == nil {
		return false
	}
	return request.WantsThinking()
}

// forwardNative sends an Anthropic-shaped body upstream unchanged except for
// the rewritten model field, and relays the raw upstream response.
func (s *Server) forwardNative(w http.ResponseWriter, r *http.Request, log logrus.FieldLogger, rt route, payload []byte, header http.Header, streaming bool) {
	tr := s.stats.track(rt.backend.Name(), rt.model)
	defer tr.done()

	resp, err := rt.backend.Send(r.Context(), &backend.Request{
		Kind:      backend.KindAnthropic,
		Model:     rt.model,
		RawBody:   payload,
		Header:    header,
		Streaming: streaming,
	})
	if err != nil {
		log.WithError(err).Warn("backend send failed")
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "backend request failed")
		return
	}
	tr.setUpstreamStatus(resp.Status)

	sn := wrapUpstreamBody(tr, resp, streaming)
	defer func() { sn.Finish(); _ = sn.Close() }()

	if resp.Status < 200 || resp.Status >= 300 {
		s.relayUpstreamError(w, log, resp)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		if streaming {
			contentType = "text/event-stream"
		} else {
			contentType = "application/json"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.Status)

	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, passthroughCopyBufferSize)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				log.WithError(writeErr).Debug("writing upstream bytes to client failed")
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				log.WithError(readErr).Warn("copying upstream body to client failed")
			}
			return
		}
	}
}

// forwardTranslated sends a pre-translated payload upstream and turns the
// response back into the Anthropic Messages shape, streaming or buffered
// depending on what the client asked for.
func (s *Server) forwardTranslated(
	w http.ResponseWriter,
	r *http.Request,
	log logrus.FieldLogger,
	rt route,
	kind backend.Kind,
	payload []byte,
	header http.Header,
	streaming bool,
	newStream func(client io.Writer, flush func()) anthropicStreamWriter,
	convert func([]byte) ([]byte, error),
) {
	tr := s.stats.track(rt.backend.Name(), rt.model)
	defer tr.done()

	resp, err := rt.backend.Send(r.Context(), &backend.Request{
		Kind:      kind,
		Model:     rt.model,
		RawBody:   payload,
		Header:    header,
		Streaming: streaming,
	})
	if err != nil {
		log.WithError(err).Warn("backend send failed")
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "backend request failed")
		return
	}
	tr.setUpstreamStatus(resp.Status)

	sn := wrapUpstreamBody(tr, resp, streaming)
	defer func() { sn.Finish(); _ = sn.Close() }()

	if resp.Status < 200 || resp.Status >= 300 {
		s.relayUpstreamError(w, log, resp)
		return
	}

	if streaming {
		responseHeader := w.Header()
		responseHeader.Set("Content-Type", "text/event-stream")
		responseHeader.Set("Cache-Control", "no-cache")
		responseHeader.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		var flush func()
		if flusher, ok := w.(http.Flusher); ok {
			flush = flusher.Flush
		}
		writer := newStream(w, flush)
		if err := writer.Consume(resp.Body); err != nil {
			log.WithError(err).Warn("consuming upstream stream failed")
			writer.Finish()
		}
		return
	}

	data, err := readAll(resp.Body, maxTranslatedResponseBody)
	if err != nil {
		log.WithError(err).Warn("reading upstream response body failed")
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream response read failed")
		return
	}
	out, err := convert(data)
	if err != nil {
		log.WithError(err).Warn("translating upstream response failed")
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream response translation failed")
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(out))
}

// relayUpstreamError forwards a non-2xx upstream response to the client: the
// upstream body verbatim (capped at maxErrorRelay) when there is one, an
// Anthropic-shaped error derived from the status otherwise.
func (s *Server) relayUpstreamError(w http.ResponseWriter, log logrus.FieldLogger, resp *backend.Response) {
	data, _ := readAll(resp.Body, maxErrorRelay)
	log.WithFields(logrus.Fields{
		"upstream_status": resp.Status,
		"upstream_bytes":  len(data),
	}).Debug("upstream returned an error response")

	if len(data) > 0 {
		if contentType := resp.Header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(data)
		return
	}
	writeAnthropicError(w, resp.Status, anthropicErrorType(resp.Status),
		fmt.Sprintf("upstream request failed with status %d", resp.Status))
}

// handleCountTokens serves POST /v1/messages/count_tokens with a cheap
// characters-over-three estimate; real tokenization is left to providers.
func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	if bodyTooLarge(body, s.cfg.Server.MaxBodyBytes) {
		writeAnthropicError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large")
		return
	}
	tokens := (len(body) + 2) / 3
	if tokens < 1 {
		tokens = 1
	}
	w.Header().Add("Warning", `299 llm-proxy "token count is a conservative estimate"`)
	writeJSON(w, http.StatusOK, map[string]int{"input_tokens": tokens})
}
