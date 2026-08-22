package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// flusherFor returns a Flusher for w; middleware's statusRecorder implements
// Flush so SSE handlers can flush through it.
func flusherFor(w http.ResponseWriter) http.Flusher {
	flusher, _ := w.(http.Flusher)
	return flusher
}

// maxTranslatedResponse caps how much of an upstream response is buffered
// when it must be translated before being handed to the client.
const maxTranslatedResponse = 16 << 20

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

// writeOpenAIModelNotFound answers the canonical OpenAI 404 for a model that
// no configured backend can serve.
func writeOpenAIModelNotFound(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusNotFound, openAIErrorBody{
		Error: openAIErrorObj{
			Message: fmt.Sprintf("model %q has no available backend", model),
			Type:    openAIErrorType(http.StatusNotFound),
			Code:    "model_not_found",
		},
	})
}

// copyUpstreamRequestID forwards the upstream x-request-id / request-id
// headers onto the client response when present.
func copyUpstreamRequestID(dst, src http.Header) {
	for _, name := range []string{"X-Request-Id", "Request-Id"} {
		if v := src.Get(name); v != "" {
			dst.Set(name, v)
		}
	}
}

// streamOpenAIBody copies an upstream body to the client, flushing after
// every read (at most 32 KiB) so SSE events are delivered as they arrive.
func streamOpenAIBody(w http.ResponseWriter, body io.Reader) {
	flusher := flusherFor(w)
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// relayOpenAIUpstreamError answers a non-2xx upstream response: forward up to
// maxErrorRelay bytes of the upstream body (with its Content-Type) when there
// is one, otherwise synthesize an OpenAI error with the mapped type.
func relayOpenAIUpstreamError(w http.ResponseWriter, resp *backend.Response) {
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorRelay))
	copyUpstreamRequestID(w.Header(), resp.Header)
	if len(bytes.TrimSpace(data)) > 0 {
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(resp.Status)
		_, _ = w.Write(data)
		return
	}
	writeOpenAIError(w, resp.Status, openAIErrorType(resp.Status), "upstream request failed")
}

// relayOpenAIBody streams a successful upstream response through to the
// client verbatim, preserving Content-Type and streaming with flushes.
func relayOpenAIBody(w http.ResponseWriter, resp *backend.Response) {
	defer func() { _ = resp.Body.Close() }()
	copyUpstreamRequestID(w.Header(), resp.Header)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.Status)
	streamOpenAIBody(w, resp.Body)
}

// relayOpenAITranslatedBody buffers a successful upstream Responses payload,
// converts it to a chat-completions response, and writes it as JSON.
func (s *Server) relayOpenAITranslatedBody(w http.ResponseWriter, resp *backend.Response, backendName, model string) {
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
	out, err := translate.ChatFromResponses(data, model)
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

// relayOpenAIResponsesStream converts an upstream Responses SSE stream into
// chat-completions chunks on the fly.
func relayOpenAIResponsesStream(w http.ResponseWriter, resp *backend.Response, model string) {
	defer func() { _ = resp.Body.Close() }()
	copyUpstreamRequestID(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := flusherFor(w)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	stream := translate.ChatStreamFromResponses(w, flush, model)
	_ = stream.Consume(resp.Body)
}

// sendOpenAIRequest forwards a prepared body to one backend.
func (s *Server) sendOpenAIRequest(r *http.Request, b backend.Backend, kind backend.Kind, model string, raw []byte, streaming bool) (*backend.Response, error) {
	return b.Send(r.Context(), &backend.Request{
		Kind:      kind,
		Model:     model,
		RawBody:   raw,
		Header:    r.Header.Clone(),
		Streaming: streaming,
	})
}

// failOpenAIBackend logs and reports a transport-level backend failure.
func (s *Server) failOpenAIBackend(w http.ResponseWriter, b backend.Backend, err error) {
	s.log.WithError(err).WithField("backend", b.Name()).Error("backend request failed")
	writeOpenAIError(w, http.StatusBadGateway, "api_error",
		fmt.Sprintf("backend %q request failed", b.Name()))
}

// handleChatCompletions serves POST /v1/chat/completions (OpenAI API).
// Backends speaking chat natively get the request passed through with only
// the model rewritten; Responses-only backends get the translated wire form
// and their replies converted back into chat-completions shape.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
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
	case route.backend.Supports(backend.KindOpenAIChat):
		tr := s.stats.track(route.backend.Name(), route.model)
		defer tr.done()
		rewritten, err := rewriteModel(body, route.model)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
				"request body is not valid JSON")
			return
		}
		resp, err := s.sendOpenAIRequest(r, route.backend, backend.KindOpenAIChat, route.model, rewritten, env.Stream)
		if err != nil {
			s.failOpenAIBackend(w, route.backend, err)
			return
		}
		tr.setUpstreamStatus(resp.Status)
		if resp.Status < 200 || resp.Status > 299 {
			relayOpenAIUpstreamError(w, resp)
			return
		}
		sn := wrapUpstreamBody(tr, resp, env.Stream)
		defer func() { sn.Finish(); _ = sn.Close() }()
		relayOpenAIBody(w, resp)

	case route.backend.Supports(backend.KindOpenAIResponses):
		tr := s.stats.track(route.backend.Name(), route.model)
		defer tr.done()
		translated, err := translate.ChatToResponses(body, route.model)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("cannot translate request for backend %s: %v", route.backend.Name(), err))
			return
		}
		resp, err := s.sendOpenAIRequest(r, route.backend, backend.KindOpenAIResponses, route.model, translated, env.Stream)
		if err != nil {
			s.failOpenAIBackend(w, route.backend, err)
			return
		}
		tr.setUpstreamStatus(resp.Status)
		if resp.Status < 200 || resp.Status > 299 {
			relayOpenAIUpstreamError(w, resp)
			return
		}
		sn := wrapUpstreamBody(tr, resp, env.Stream)
		defer func() { sn.Finish(); _ = sn.Close() }()
		if env.Stream {
			relayOpenAIResponsesStream(w, resp, route.model)
			return
		}
		s.relayOpenAITranslatedBody(w, resp, route.backend.Name(), route.model)

	default:
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("backend %s does not support the Chat Completions API for model %q",
				route.backend.Name(), env.Model))
	}
}
