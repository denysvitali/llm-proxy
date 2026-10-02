package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// Wire definitions describe how each inbound API reaches an upstream API.
// The matrix and selection policy are independent of HTTP delivery:
// exchange.go executes the chosen path, and client_dialect.go writes
// protocol-specific errors and stream failures.

// streamTranslator consumes an upstream SSE stream and emits client-dialect
// events; Finish is idempotent and always called.
type streamTranslator interface {
	Consume(io.Reader) error
	Finish()
}

// translateEnv carries everything a conversion needs about one request.
type translateEnv struct {
	kind            backend.Kind // inbound API the client spoke
	body            []byte       // raw inbound request body
	model           string       // upstream model name sent to the backend
	clientModel     string       // model name echoed back to the client
	thinking        bool         // Anthropic client asked for extended thinking
	streaming       bool         // client requested an SSE stream
	reasoningEffort string       // Codex selector effort, applied only to Codex Responses wire
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

// applyCodexReasoningEffort adds the selector's effort to a Responses request.
// It runs after translation so native and translated requests receive it.
func applyCodexReasoningEffort(rt route, wire resolvedWire, payload []byte, effort string) ([]byte, error) {
	if effort == "" || rt.backend.Name() != "codex" {
		return payload, nil
	}
	wireKind := backend.KindOpenAIResponses
	if !wire.native && wire.path != nil {
		wireKind = wire.path.kind
	}
	if wireKind != backend.KindOpenAIResponses {
		return payload, nil
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, fmt.Errorf("request body is not valid JSON: %v", err)
	}
	if reasoning, exists := request["reasoning"]; exists && reasoning != nil {
		if _, ok := reasoning.(map[string]any); !ok {
			return nil, errors.New("reasoning must be a JSON object")
		}
	} else {
		request["reasoning"] = map[string]any{}
	}
	reasoning := request["reasoning"].(map[string]any)
	reasoning["effort"] = effort
	return json.Marshal(request)
}
