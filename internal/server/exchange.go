package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// Exchange execution connects routing and wire selection to HTTP delivery.
// Wire definitions supply conversions, client dialects format failures,
// and retry.go owns retrying and relaying upstream response bodies.

// maxTranslatedResponseBody caps how much of an upstream response is buffered
// when it must be translated before being handed to the client.
const maxTranslatedResponseBody = 16 << 20

// passthroughCopyBufferSize is the read size used when relaying an upstream
// body verbatim; each read is flushed, bounding SSE event latency.
const passthroughCopyBufferSize = 32 << 10

// exchangeOutcome describes delivery to the client independently of whether
// the fallback chain can try another backend.
type exchangeOutcome int

const (
	// exchangeOK: the backend served the request to completion.
	exchangeOK exchangeOutcome = iota
	// exchangeFailed: the backend failed, or delivery to the client failed.
	exchangeFailed
	// exchangeSurfaced: content had already flowed when the upstream broke;
	// the failure was surfaced as the client protocol's in-band error and
	// replaying on another backend would duplicate output.
	exchangeSurfaced
	// exchangeRejected: a non-retryable upstream rejection was relayed to the
	// client verbatim (typically a 4xx the fallback would repeat).
	exchangeRejected
	// exchangeCanceled: the client stopped waiting before completion.
	exchangeCanceled
)

// exchangeResult keeps the delivery verdict, response commitment, and replay
// policy separate. A final 502 is a committed failure, not a successful
// exchange; an uncommitted failure may be handed to another backend.
type exchangeResult struct {
	outcome          exchangeOutcome
	committed        bool
	fallbackEligible bool
	message          string
}

func failedExchange(message string) exchangeResult {
	return exchangeResult{outcome: exchangeFailed, fallbackEligible: true, message: message}
}

// exchangeOutcomeName renders an outcome for span attributes.
func exchangeOutcomeName(outcome exchangeOutcome) string {
	switch outcome {
	case exchangeOK:
		return "ok"
	case exchangeFailed:
		return "failed"
	case exchangeSurfaced:
		return "surfaced"
	case exchangeRejected:
		return "rejected"
	case exchangeCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// nocloseReader adapts a bytes.Reader as a response body whose Close is a
// no-op, so captured upstream error bodies can be relayed after the original
// connection was already closed without double-close noise.
type nocloseReader struct{ io.Reader }

func (r *nocloseReader) Close() error { return nil }

// exchangeChain walks a resolved route chain: each backend gets the request
// (encoded per its selected wire format) until one serves it. A backend
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
) {
	for i := range chain {
		if r.Context().Err() != nil {
			return
		}
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
		payload, err := prepareRouteRequest(rt, wire, &routeEnv)
		if err == nil {
			payload, err = applyCodexReasoningEffort(rt, wire, payload, routeEnv.reasoningEffort)
		}
		if err != nil {
			if final {
				routeLog.WithError(err).Error("cannot encode request for backend; rejecting request")
				dialect.writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
			routeLog.WithError(err).Error("cannot encode request for backend; trying next fallback")
			continue
		}
		result := s.exchange(w, r, routeLog, rt, dialect, wire, payload, r.Header.Clone(), routeEnv, final)
		if !result.fallbackEligible || result.committed || final || r.Context().Err() != nil {
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
// earlier ones return an uncommitted, fallback-eligible failure.
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
) (result exchangeResult) {
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
		span.SetAttributes(
			attribute.String("llm_proxy.outcome", exchangeOutcomeName(result.outcome)),
			attribute.Bool("llm_proxy.response_committed", result.committed),
			attribute.Bool("llm_proxy.fallback_eligible", result.fallbackEligible),
		)
		span.End()
	}()
	r = r.WithContext(spanCtx)
	finish := func(result exchangeResult) exchangeResult {
		if result.outcome != exchangeOK && r.Context().Err() != nil {
			result.outcome = exchangeCanceled
			result.fallbackEligible = false
			return result
		}
		if final && result.fallbackEligible && !result.committed {
			dialect.writeError(w, http.StatusBadGateway, "api_error", result.message)
			result.committed = true
			result.fallbackEligible = false
		}
		return result
	}

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
	// Keep only request metadata in the dashboard history. The request and
	// translated payloads can contain prompts, tool inputs, and credentials;
	// neither should outlive this exchange in proxy-owned state.
	s.stats.inspect(tr, RequestID(r.Context()), string(wireFormat))
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
	if r.Context().Err() != nil {
		tr.noteTransportError(r.Context().Err())
		return exchangeResult{outcome: exchangeCanceled}
	}
	if err != nil {
		tr.noteTransportError(err)
		log.WithError(err).Warn("backend send failed")
		return finish(failedExchange("backend request failed"))
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
			return finish(failedExchange("upstream server error"))
		}
		dialect.relayError(w, resp)
		return exchangeResult{outcome: exchangeRejected, committed: true}
	}

	switch {
	case wire.native && env.streaming:
		result = s.relayNativeStreaming(r.Context(), w, log, rt, resp, fetch, streamDoneChecker(wireFormat), dialect.surfaceStream)
	case wire.native:
		result = s.relayNativeBuffered(r.Context(), w, log, rt, resp, fetch)
	case env.streaming:
		result = s.relayTranslatedStreaming(r.Context(), w, log, rt, resp, fetch, env, wire.path.stream)
	default:
		data, err := s.fetchResponseBody(r.Context(), rt, resp, fetch, maxTranslatedResponseBody)
		if err != nil {
			log.WithError(err).Warn("reading upstream response body failed")
			return finish(failedExchange(fmt.Sprintf("upstream response could not be read: %v", err)))
		}
		out, err := wire.path.decode(env, data)
		if err != nil {
			var upstreamErr *translate.UpstreamError
			if errors.As(err, &upstreamErr) {
				log.WithError(err).Warn("upstream answered success with an error body")
				return finish(failedExchange(fmt.Sprintf("upstream returned an error: %v", upstreamErr)))
			}
			log.WithError(err).Warn("translating upstream response failed")
			return finish(failedExchange("upstream returned an unreadable response"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(json.RawMessage(out)); err != nil {
			return finish(exchangeResult{outcome: exchangeFailed, committed: true, message: err.Error()})
		}
		result = exchangeResult{outcome: exchangeOK, committed: true}
	}
	return finish(result)
}
