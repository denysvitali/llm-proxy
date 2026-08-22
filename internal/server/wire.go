package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"

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
			return translate.ResponsesFromChat(data, env.clientModel)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewResponsesStreamFromChat(client, flush, env.clientModel)
		},
	},
	{backend.KindOpenAIResponses, backend.KindAnthropic}: {
		kind:   backend.KindAnthropic,
		encode: func(env translateEnv) ([]byte, error) { return translate.ResponsesToAnthropic(env.body, env.model) },
		decode: func(env translateEnv, data []byte) ([]byte, error) {
			return translate.ResponsesFromAnthropic(data, env.clientModel)
		},
		stream: func(env translateEnv, client io.Writer, flush func()) streamTranslator {
			return translate.NewResponsesStreamFromAnthropic(client, flush, env.clientModel)
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
// this API nor anything translatable.
func resolveWire(in backend.Kind, b backend.Backend) (resolvedWire, bool) {
	for _, want := range wirePreference[in] {
		if !b.Supports(want) {
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
// phrased when no upstream response exists, and how a non-2xx upstream
// response is passed on.
type clientDialect struct {
	writeError func(w http.ResponseWriter, status int, errType, message string)
	relayError func(w http.ResponseWriter, resp *backend.Response)
}

func anthropicDialect(relay func(w http.ResponseWriter, resp *backend.Response)) clientDialect {
	return clientDialect{
		writeError: writeAnthropicError,
		relayError: relay,
	}
}

func openAIDialect() clientDialect {
	return clientDialect{
		writeError: writeOpenAIError,
		relayError: relayOpenAIUpstreamError,
	}
}

// exchange dispatches one prepared request: tracks stats, sends upstream on
// the chosen wire format (native requests pass through with only the model
// rewritten), sniffs the response for usage stats, and relays success bodies
// to the client — verbatim when native, translated otherwise, streaming or
// buffered as the client asked.
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
) {
	tr := s.stats.track(rt.backend.Name(), rt.model)
	defer tr.done()

	wireFormat := env.kind
	if !wire.native && wire.path != nil {
		wireFormat = wire.path.kind
	}
	resp, err := rt.backend.Send(r.Context(), &backend.Request{
		Kind:      wireFormat,
		Model:     rt.model,
		RawBody:   payload,
		Header:    header,
		Streaming: env.streaming,
	})
	if err != nil {
		log.WithError(err).Warn("backend send failed")
		dialect.writeError(w, http.StatusBadGateway, "api_error", "backend request failed")
		return
	}
	tr.setUpstreamStatus(resp.Status)

	sn := wrapUpstreamBody(tr, resp, env.streaming)
	defer func() { sn.Finish(); _ = sn.Close() }()

	if resp.Status < 200 || resp.Status >= 300 {
		dialect.relayError(w, resp)
		return
	}

	if wire.native {
		s.relayPassthroughBody(w, resp, env.streaming)
		return
	}

	if env.streaming {
		responseHeader := w.Header()
		responseHeader.Set("Content-Type", "text/event-stream")
		responseHeader.Set("Cache-Control", "no-cache")
		responseHeader.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		var flush func()
		if flusher, ok := w.(http.Flusher); ok {
			flush = flusher.Flush
		}
		writer := wire.path.stream(env, w, flush)
		if err := writer.Consume(resp.Body); err != nil {
			log.WithError(err).Warn("consuming upstream stream failed")
			writer.Finish()
		}
		return
	}

	data, err := readAll(resp.Body, maxTranslatedResponseBody)
	if err != nil {
		log.WithError(err).Warn("reading upstream response body failed")
		dialect.writeError(w, http.StatusBadGateway, "api_error", "upstream response read failed")
		return
	}
	out, err := wire.path.decode(env, data)
	if err != nil {
		log.WithError(err).Warn("translating upstream response failed")
		dialect.writeError(w, http.StatusBadGateway, "api_error", "upstream returned an unreadable response")
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(out))
}

// relayPassthroughBody copies a successful upstream response to the client
// verbatim, preserving Content-Type (defaulting by streaming mode) and
// flushing after every read so SSE events arrive as they are produced.
func (s *Server) relayPassthroughBody(w http.ResponseWriter, resp *backend.Response, streaming bool) {
	copyUpstreamRequestID(w.Header(), resp.Header)
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

	flusher := flusherFor(w)
	buffer := make([]byte, passthroughCopyBufferSize)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				s.log.WithError(writeErr).Debug("writing upstream bytes to client failed")
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				s.log.WithError(readErr).Debug("copying upstream body to client failed")
			}
			return
		}
	}
}
