package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// This file is the proxy's translation core: a matrix keyed by (inbound API,
// backend wire format) that lets every endpoint talk to every backend. A
// handler resolves the best wire format its routed backend supports, encodes
// the request through the matching path, and exchange() relays the response
// back through the reverse direction — buffered or streamed.

// maxTranslatedResponseBody caps how much of an upstream response is buffered
// when it must be translated before being handed to the client.
const maxTranslatedResponseBody = 16 << 20

// passthroughCopyBufferSize is the read size used when relaying an upstream
// body verbatim; each read is flushed, bounding SSE event latency.
const passthroughCopyBufferSize = 32 << 10

// streamTranslator consumes an upstream SSE stream and emits client-dialect
// events; Finish is idempotent and always called.
type streamTranslator interface {
	Consume(io.Reader) error
	Finish()
}

// translateEnv carries everything a conversion needs about one request.
type translateEnv struct {
	kind        backend.Kind // inbound API the client spoke
	body        []byte       // raw inbound request body
	model       string       // upstream model name sent to the backend
	clientModel string       // model name echoed back to the client
	thinking    bool         // Anthropic client asked for extended thinking
	streaming   bool         // client requested an SSE stream
}

// translationPath converts one inbound API shape onto one backend wire format.
// decode turns a buffered upstream success body back into the client dialect;
// stream wraps the SSE equivalent.
type translationPath struct {
	kind   backend.Kind // wire format produced for the backend
	encode func(env translateEnv) ([]byte, error)
	decode func(env translateEnv, data []byte) ([]byte, error)
	stream func(env translateEnv, client io.Writer, flush func()) streamTranslator
}

// translations is every off-diagonal conversion; diagonal entries are
// passthrough (the body is forwarded with only the model rewritten).
var translations = map[[2]backend.Kind]*translationPath{
	// Anthropic clients on OpenAI-shaped backends.
	{backend.KindAnthropic, backend.KindOpenAIChat}: {
		kind: backend.KindOpenAIChat,
		encode: func(env translateEnv) ([]byte, error) {
			request, err := translate.ParseRequest(env.body)
			if err != nil {
				return nil, err
			}
			return translate.ToOpenAI(request, env.model)
		},
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.FromOpenAI(data, env.clientModel, env.thinking)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewStreamWriter(client, flush, env.clientModel, env.thinking)
		},
	},
	{backend.KindAnthropic, backend.KindOpenAIResponses}: {
		kind: backend.KindOpenAIResponses,
		encode: func(env translateEnv) ([]byte, error) {
			request, err := translate.ParseRequest(env.body)
			if err != nil {
				return nil, err
			}
			return translate.ToResponses(request, env.model)
		},
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.FromResponses(data, env.clientModel, env.thinking)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewResponsesStreamWriter(client, flush, env.clientModel, env.thinking)
		},
	},

	// Chat-completions clients.
	{backend.KindOpenAIChat, backend.KindOpenAIResponses}: {
		kind:   backend.KindOpenAIResponses,
		encode: func(env translateEnv) ([]byte, error) { return translate.ChatToResponses(env.body, env.model) },
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.ChatFromResponses(data, env.clientModel)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.ChatStreamFromResponses(client, flush, env.clientModel)
		},
	},
	{backend.KindOpenAIChat, backend.KindAnthropic}: {
		kind:   backend.KindAnthropic,
		encode: func(env translateEnv) ([]byte, error) { return translate.ChatToAnthropic(env.body, env.model) },
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.ChatFromAnthropic(data, env.clientModel)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewChatStreamFromAnthropic(client, flush, env.clientModel)
		},
	},

	// Responses clients.
	{backend.KindOpenAIResponses, backend.KindOpenAIChat}: {
		kind:   backend.KindOpenAIChat,
		encode: func(env translateEnv) ([]byte, error) { return translate.ResponsesToChat(env.body, env.model) },
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.ResponsesFromChatForRequest(data, env.clientModel, env.body)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewResponsesStreamFromChatForRequest(client, flush, env.clientModel, env.body)
		},
	},
	{backend.KindOpenAIResponses, backend.KindAnthropic}: {
		kind:   backend.KindAnthropic,
		encode: func(env translateEnv) ([]byte, error) { return translate.ResponsesToAnthropic(env.body, env.model) },
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.ResponsesFromAnthropicForRequest(data, env.clientModel, env.body)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewResponsesStreamFromAnthropicForRequest(client, flush, env.clientModel, env.body)
		},
	},
}

// wirePreference lists the wire formats each inbound API prefers, most
// natural first: natively before translated, OpenAI shapes before Anthropic
// for OpenAI clients.
var wirePreference = map[backend.Kind][]backend.Kind{
	backend.KindAnthropic:       {backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses},
	backend.KindOpenAIChat:      {backend.KindOpenAIChat, backend.KindOpenAIResponses, backend.KindAnthropic},
	backend.KindOpenAIResponses: {backend.KindOpenAIResponses, backend.KindOpenAIChat, backend.KindAnthropic},
}

// resolvedWire is the chosen way to reach a backend from one inbound API:
// native passthrough, or a conversion path.
type resolvedWire struct {
	native bool             // forward the inbound body unchanged (model rewrite only)
	path   *translationPath // conversion to apply when not native
}

// resolveWire picks the first wire format the backend supports under the
// inbound API's preference order. ok=false means the backend serves neither
// this API nor anything translatable. When the backend implements
// ModelWireOverrider, native support is decided per model so a provider can
// force translation for models whose native endpoint is unreliable.
func resolveWire(in backend.Kind, b backend.Backend, model string) (resolvedWire, bool) {
	supports := b.Supports
	if mo, ok := b.(backend.ModelWireOverrider); ok {
		supports = func(kind backend.Kind) bool { return mo.SupportsModel(kind, model) }
	}
	for _, want := range wirePreference[in] {
		if !supports(want) {
			continue
		}
		if want == in {
			return resolvedWire{native: true}, true
		}
		return resolvedWire{path: translations[[2]backend.Kind{in, want}]}, true
	}
	return resolvedWire{}, false
}

// clientDialect holds the inbound-API-specific answers: how errors are
// phrased when no upstream response exists, how a non-2xx upstream response
// is passed on, and how a mid-stream break is surfaced as the protocol's
// in-band failure once content has already been forwarded.
type clientDialect struct {
	writeError    func(w http.ResponseWriter, status int, errType, message string)
	relayError    func(w http.ResponseWriter, resp *backend.Response)
	surfaceStream func(w http.ResponseWriter, message string)
}

func anthropicDialect(relay func(w http.ResponseWriter, resp *backend.Response)) clientDialect {
	return clientDialect{
		writeError:    writeAnthropicError,
		relayError:    relay,
		surfaceStream: emitAnthropicStreamFailure,
	}
}

// openAIDialect serves chat-completions clients; openAIResponsesDialect
// serves Responses clients. They share error shapes but differ in the
// in-band stream-failure event their SDKs understand.
func openAIDialect() clientDialect {
	return clientDialect{
		writeError:    writeOpenAIError,
		relayError:    relayOpenAIUpstreamError,
		surfaceStream: emitChatStreamFailure,
	}
}

func openAIResponsesDialect() clientDialect {
	return clientDialect{
		writeError:    writeOpenAIError,
		relayError:    relayOpenAIUpstreamError,
		surfaceStream: emitResponsesStreamFailure,
	}
}

// emitAnthropicStreamFailure ends an already-started Anthropic SSE stream
// with the protocol's error event, so the client sees a real failure it can
// replay instead of a truncation that looks finished.
func emitAnthropicStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": message},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// emitChatStreamFailure ends an already-started chat-completions stream with
// the error-chunk shape OpenAI clients understand.
func emitChatStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"error": map[string]any{"message": message, "type": "api_error"},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// emitResponsesStreamFailure ends an already-started Responses stream with a
// response.failed event carrying the failure reason.
func emitResponsesStreamFailure(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id":         "resp_llm-proxy",
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "failed",
			"error":      map[string]any{"code": "api_error", "message": message},
		},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: response.failed\ndata: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// exchangeOutcome says how one backend attempt ended, so the fallback chain
// knows whether another backend may still take the request over.
type exchangeOutcome int

const (
	// exchangeOK: the backend served the request to completion.
	exchangeOK exchangeOutcome = iota
	// exchangeRetryable: the backend failed before anything reached the
	// client (transport error, terminal 5xx, or a spent body-phase retry
	// budget), so a fallback can replay the request invisibly.
	exchangeRetryable
	// exchangeSurfaced: content had already flowed when the upstream broke;
	// the failure was surfaced as the client protocol's in-band error and
	// replaying on another backend would duplicate output.
	exchangeSurfaced
	// exchangeRejected: a non-retryable upstream rejection was relayed to the
	// client verbatim (typically a 4xx the fallback would repeat).
	exchangeRejected
)

// exchangeOutcomeName renders an outcome for span attributes.
func exchangeOutcomeName(outcome exchangeOutcome) string {
	switch outcome {
	case exchangeOK:
		return "ok"
	case exchangeRetryable:
		return "retryable"
	case exchangeSurfaced:
		return "surfaced"
	case exchangeRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// kindName labels an inbound API in client-facing error messages.
func kindName(kind backend.Kind) string {
	switch kind {
	case backend.KindAnthropic:
		return "Anthropic Messages"
	case backend.KindOpenAIChat:
		return "Chat Completions"
	case backend.KindOpenAIResponses:
		return "Responses"
	default:
		return string(kind)
	}
}

// nocloseReader adapts a bytes.Reader as a response body whose Close is a
// no-op, so captured upstream error bodies can be relayed after the original
// connection was already closed without double-close noise.
type nocloseReader struct{ io.Reader }

func (r *nocloseReader) Close() error { return nil }

// exchangeChain walks a resolved route chain: each backend gets the request
// (encoded per its wire format by prepare) until one serves it. A backend
// that fails or cannot encode before anything reaches the client hands the
// request to the next fallback; the last backend's failure is written to the
// client exactly as a single-backend proxy would have.
func (s *Server) exchangeChain(
	w http.ResponseWriter,
	r *http.Request,
	log logrus.FieldLogger,
	chain []route,
	dialect clientDialect,
	env translateEnv,
	prepare func(rt route, wire resolvedWire, env *translateEnv) ([]byte, error),
) {
	for i := range chain {
		rt := chain[i]
		final := i == len(chain)-1
		routeLog := log.WithField("backend", rt.backend.Name())
		wire, servable := resolveWire(env.kind, rt.backend, rt.model)
		if !servable {
			if final {
				routeLog.Error("backend does not support this API; rejecting request")
				dialect.writeError(w, http.StatusBadRequest, "invalid_request_error",
					fmt.Sprintf("backend %s does not support the %s API", rt.backend.Name(), kindName(env.kind)))
				return
			}
			routeLog.Error("backend cannot serve this API; trying next fallback")
			continue
		}
		routeEnv := env
		routeEnv.model = rt.model
		payload, err := prepare(rt, wire, &routeEnv)
		if err != nil {
			if final {
				routeLog.WithError(err).Error("cannot encode request for backend; rejecting request")
				dialect.writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
			routeLog.WithError(err).Error("cannot encode request for backend; trying next fallback")
			continue
		}
		outcome := s.exchange(w, r, routeLog, rt, dialect, wire, payload, r.Header.Clone(), routeEnv, final)
		if outcome != exchangeRetryable || final {
			return
		}
		s.metrics.noteFallback(rt.backend.Name(), chain[i+1].backend.Name())
		routeLog.WithField("fallback", chain[i+1].backend.Name()).
			Warn("backend failed before any output reached the client; falling back")
	}
}

// exchange dispatches one prepared request: sends upstream on the chosen
// wire format with transient-failure retries (native requests pass through
// with only the model rewritten), sniffs every upstream body for usage
// stats, and relays success bodies to the client — verbatim when native,
// translated otherwise, streaming or buffered as the client asked. Broken
// upstream bodies retry while nothing has reached the client; once content
// has flowed they surface as the protocol's in-band failure. final marks the
// last backend of a fallback chain: only it may write failure responses,
// earlier ones report exchangeRetryable so the chain can move on.
func (s *Server) exchange(
	w http.ResponseWriter,
	r *http.Request,
	log logrus.FieldLogger,
	rt route,
	dialect clientDialect,
	wire resolvedWire,
	payload []byte,
	header http.Header,
	env translateEnv,
	final bool,
) (outcome exchangeOutcome) {
	tr := s.stats.track(rt.backend.Name(), rt.model)
	defer tr.done()

	// One child span per backend attempt, so a fallback chain shows up as
	// siblings under the request span with each backend's outcome.
	spanCtx, span := tracer.Start(r.Context(), "upstream "+rt.backend.Name(),
		trace.WithAttributes(
			attribute.String("llm_proxy.backend", rt.backend.Name()),
			attribute.String("llm_proxy.model", rt.model),
		))
	defer func() {
		span.SetAttributes(attribute.String("llm_proxy.outcome", exchangeOutcomeName(outcome)))
		span.End()
	}()
	r = r.WithContext(spanCtx)

	wireFormat := env.kind
	if !wire.native && wire.path != nil {
		wireFormat = wire.path.kind
	}
	req := &backend.Request{
		Kind:      wireFormat,
		Model:     rt.model,
		RawBody:   payload,
		Header:    header,
		Streaming: env.streaming,
	}
	// Every attempt's body is sniffed against the same tracker: usage fields
	// fold by high-water mark, so a recovered retry keeps its stats and a
	// discarded partial one cannot inflate them. All sniffers close when
	// exchange returns.
	var sniffers []*sniffer
	fetch := func() (*backend.Response, error) {
		resp, err := rt.backend.Send(r.Context(), req)
		if err != nil {
			return nil, err
		}
		sse := env.streaming || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
		sn := newSniffer(resp.Body, tr, sse, resp.Status)
		resp.Body = sn
		sniffers = append(sniffers, sn)
		return resp, nil
	}
	defer func() {
		for _, sn := range sniffers {
			sn.Finish()
			_ = sn.Close()
		}
	}()

	resp, err := s.sendWithRetry(r.Context(), log, rt, fetch)
	if err != nil {
		tr.noteTransportError(err)
		if r.Context().Err() != nil {
			// The client stopped waiting; there is nobody to answer.
			return exchangeRetryable
		}
		log.WithError(err).Warn("backend send failed")
		if !final {
			return exchangeRetryable
		}
		dialect.writeError(w, http.StatusBadGateway, "api_error", "backend request failed")
		return exchangeRetryable
	}
	tr.setUpstreamStatus(resp.Status)

	if resp.Status < 200 || resp.Status >= 300 {
		// Capture the error body once for the stats feed, then hand the relay
		// a fresh reader over the same bytes — the dialect relays re-read the
		// body to forward it to the client.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorRelay))
		_ = resp.Body.Close()
		tr.noteUpstreamError(errBody)
		resp.Body = &nocloseReader{Reader: bytes.NewReader(errBody)}
		if resp.Status >= 500 && !final {
			// A server-side failure the client must not see while a fallback
			// remains.
			log.WithField("upstream_status", resp.Status).Warn("upstream server error; falling back")
			return exchangeRetryable
		}
		dialect.relayError(w, resp)
		return exchangeRejected
	}

	// giveUp answers a spent body-phase retry budget. Unless this is the
	// chain's last backend the failure is recorded as retryable instead of
	// written, so the chain can hand the request to the next backend.
	retryable := false
	giveUp := func(w http.ResponseWriter, message string) {
		if !final {
			retryable = true
			log.Warn("upstream body retries spent before any output; giving up on backend")
			return
		}
		dialect.writeError(w, http.StatusBadGateway, "api_error", message)
	}

	switch {
	case wire.native && env.streaming:
		if surfaced := s.relayNativeStreaming(r.Context(), w, log, rt, resp, fetch, streamDoneChecker(wireFormat), dialect.surfaceStream, giveUp); surfaced {
			return exchangeSurfaced
		}
	case wire.native:
		s.relayNativeBuffered(r.Context(), w, log, rt, resp, fetch, giveUp)
	case env.streaming:
		if surfaced := s.relayTranslatedStreaming(r.Context(), w, log, rt, resp, fetch, env, wire.path.stream, giveUp); surfaced {
			return exchangeSurfaced
		}
	default:
		data, err := s.fetchResponseBody(r.Context(), rt, resp, fetch, maxTranslatedResponseBody)
		if err != nil {
			log.WithError(err).Warn("reading upstream response body failed")
			giveUp(w, fmt.Sprintf("upstream response could not be read: %v", err))
			if retryable {
				return exchangeRetryable
			}
			return exchangeOK
		}
		out, err := wire.path.decode(env, data)
		if err != nil {
			var upstreamErr *translate.UpstreamError
			if errors.As(err, &upstreamErr) {
				log.WithError(err).Warn("upstream answered success with an error body")
				dialect.writeError(w, http.StatusBadGateway, "api_error",
					fmt.Sprintf("upstream returned an error: %v", upstreamErr))
				return exchangeRejected
			}
			log.WithError(err).Warn("translating upstream response failed")
			giveUp(w, "upstream returned an unreadable response")
			if retryable {
				return exchangeRetryable
			}
			return exchangeOK
		}
		writeJSON(w, http.StatusOK, json.RawMessage(out))
	}
	if retryable {
		return exchangeRetryable
	}
	return exchangeOK
}
