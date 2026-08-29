package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

// Upstream failure handling, shared by every endpoint through exchange().
//
// A broken upstream is handled according to how much has already reached the
// client:
//
//   - Connection phase (Send failed or returned a transient status): nothing
//     was forwarded, so the request is retried after a backoff pause while
//     the client just waits.
//   - Body phase, nothing forwarded: response headers are held back by a
//     gatedWriter until the first byte flows, so a broken body can likewise
//     be retried invisibly. A 200 that ends without producing anything counts
//     as broken here too — that "clean empty stream" used to surface as a
//     silently empty reply.
//   - Body phase, content already flowed: replaying upstream would duplicate
//     it, so the break is surfaced as the client protocol's in-band failure
//     instead of being masked as a completed turn.

const (
	// midstreamRetries is how many extra attempts a broken upstream response
	// body gets while nothing has been forwarded to the client yet. The
	// client only experiences a longer wait.
	midstreamRetries = 2
	// retryBaseDelay spaces the first retry from the failed attempt; later
	// retries double each time, up to the max backoff cap.
	retryBaseDelay = 750 * time.Millisecond
	// retryCompletionWindow is how much of the streamed tail is kept in the
	// rolling window checked for a stream's completion marker.
	retryCompletionWindow = 128
	// defaultRetryAttempts caps retries for statuses the client must never
	// see. They remain bounded so a permanently unhealthy upstream cannot
	// keep a request alive forever. Per-backend override:
	// backends[].retry_attempts.
	defaultRetryAttempts = 3
	// defaultRetryMaxBackoff caps provider-supplied Retry-After delays and
	// the exponential backoff so a bad or extreme header cannot stall
	// requests indefinitely. Per-backend override: backends[].retry_max_backoff.
	defaultRetryMaxBackoff = 30 * time.Second
)

// Retry metric phases and outcomes.
const (
	retryPhaseConnect = "connect"
	retryPhaseBody    = "body"

	retryRecovered = "recovered"
	retryExhausted = "exhausted"
	retrySurfaced  = "surfaced"
)

// midstreamFailureMessage is the client-facing message for an upstream stream
// that broke after content had already been forwarded.
const midstreamFailureMessage = "the upstream stream was interrupted mid-response"

// upstreamFetch performs one upstream attempt for the request.
type upstreamFetch func() (*backend.Response, error)

// retryPause waits out the backoff before another upstream attempt and reports
// whether the client is still there to wait for it.
func retryPause(ctx context.Context, attempt int, maxBackoff time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(retryDelay(attempt, maxBackoff)):
		return true
	}
}

// retryDelay returns the backoff before the given retry attempt: exponential
// from retryBaseDelay, capped at maxBackoff so a long outage bounds each
// pause — and with it how long a client can be kept waiting. Tests run inside
// testing/synctest bubbles, so even the longest pause elapses instantly there.
func retryDelay(attempt int, maxBackoff time.Duration) time.Duration {
	return min(retryBaseDelay<<attempt, maxBackoff)
}

// errReader stands in for an upstream body whose fetch itself failed, so retry
// loops can treat "could not fetch" like any other mid-transfer break.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// retryableUpstream reports whether a connection-phase outcome deserves
// another attempt: transport errors always, and statuses the client must
// never see while nothing has been forwarded — gateway failures (502/503/504)
// plus providers that use 429/422 as transient overload signals.
func retryableUpstream(resp *backend.Response, err error) bool {
	if err != nil {
		return true
	}
	switch resp.Status {
	case http.StatusTooManyRequests, http.StatusUnprocessableEntity,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryAfter returns the delay requested by an upstream Retry-After header.
// Relative second values are supported, capped at the budget's max backoff;
// date-valued or malformed headers fall back to the normal exponential
// backoff.
func retryAfter(header http.Header, maxBackoff time.Duration) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return 0, false
	}
	delay := time.Duration(seconds) * time.Second
	return min(delay, maxBackoff), true
}

// sendWithRetry runs the connection phase: a transient failure gets up to the
// backend's configured number of extra attempts (backends[].retry_attempts,
// default 10) before its status reaches the client. Every attempt — including
// the discarded ones — is recorded as its own upstream request so uptime
// denominators stay honest.
func (s *Server) sendWithRetry(ctx context.Context, log logrus.FieldLogger, rt route, fetch upstreamFetch) (*backend.Response, error) {
	budget := s.retryBudgetFor(rt.backend.Name())
	backendName, model := rt.backend.Name(), rt.model
	var resp *backend.Response
	var err error
	attempts := 0
	for attempt := 0; ; attempt++ {
		resp, err = fetch()
		if !retryableUpstream(resp, err) || attempt >= budget.attempts {
			break
		}
		failed := s.stats.track(backendName, model)
		var retryLog logrus.FieldLogger = log
		if resp != nil {
			failed.setUpstreamStatus(resp.Status)
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorRelay))
			_ = resp.Body.Close()
			failed.noteUpstreamError(body)
			retryLog = retryLog.WithFields(logrus.Fields{
				"upstream_status": resp.Status,
				"upstream_error":  upstreamErrorSummary(body),
			})
		} else if err != nil {
			failed.noteTransportError(err)
			retryLog = retryLog.WithError(err)
		}
		failed.done()
		attempts++
		s.metrics.noteRetryAttempt(retryPhaseConnect, backendName, model)
		retryLog.WithFields(logrus.Fields{
			"attempt":      attempts + 1,
			"max_attempts": budget.attempts + 1,
		}).Warn("upstream unavailable; retrying")
		paused := true
		if resp != nil {
			if delay, ok := retryAfter(resp.Header, budget.maxBackoff); ok && delay > retryDelay(attempt, budget.maxBackoff) {
				select {
				case <-ctx.Done():
					paused = false
				case <-time.After(delay):
				}
			} else {
				paused = retryPause(ctx, attempt, budget.maxBackoff)
			}
		} else {
			paused = retryPause(ctx, attempt, budget.maxBackoff)
		}
		if !paused {
			return nil, ctx.Err()
		}
	}
	switch {
	case attempts == 0:
		// first attempt succeeded; nothing to classify
	case err != nil || retryableUpstream(resp, err):
		s.metrics.noteRetryOutcome(retryPhaseConnect, retryExhausted, backendName, model)
	default:
		s.metrics.noteRetryOutcome(retryPhaseConnect, retryRecovered, backendName, model)
	}
	return resp, err
}

// retryReader performs one more upstream attempt for the body phase. Every
// failure mode — transport error or non-2xx reply — becomes a reader that
// fails immediately, so retry loops treat all breaks alike.
func (s *Server) retryReader(fetch upstreamFetch) io.Reader {
	resp, err := fetch()
	if err != nil {
		return errReader{err: err}
	}
	if resp.Status < 200 || resp.Status >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorRelay))
		_ = resp.Body.Close()
		return errReader{err: fmt.Errorf("upstream returned %d %s", resp.Status, http.StatusText(resp.Status))}
	}
	return resp.Body
}

// fetchResponseBody buffers a non-streaming upstream body to the end,
// retrying while the transfer fails. Nothing has reached the client during
// any of it, so retries only ever look like latency.
func (s *Server) fetchResponseBody(ctx context.Context, rt route, resp *backend.Response, fetch upstreamFetch, limit int64) ([]byte, error) {
	budget := s.retryBudgetFor(rt.backend.Name())
	var body io.Reader = resp.Body
	for attempt := 0; ; attempt++ {
		data, readErr := io.ReadAll(io.LimitReader(body, limit+1))
		if closer, ok := body.(io.Closer); ok {
			_ = closer.Close()
		}
		if readErr == nil {
			if attempt > 0 {
				s.metrics.noteRetryOutcome(retryPhaseBody, retryRecovered, rt.backend.Name(), rt.model)
			}
			return data, nil
		}
		if attempt >= midstreamRetries || !retryPause(ctx, attempt, budget.maxBackoff) {
			s.metrics.noteRetryOutcome(retryPhaseBody, retryExhausted, rt.backend.Name(), rt.model)
			return nil, readErr
		}
		s.metrics.noteRetryAttempt(retryPhaseBody, rt.backend.Name(), rt.model)
		s.log.WithError(readErr).WithField("attempt", attempt+1).Warn("upstream body failed before any output; retrying")
		body = s.retryReader(fetch)
	}
}

// streamDoneChecker returns the completion-marker test for an upstream wire
// format's SSE stream, matched against a rolling tail of what was relayed.
func streamDoneChecker(format backend.Kind) func([]byte) bool {
	switch format {
	case backend.KindAnthropic:
		return anthropicStreamDone
	case backend.KindOpenAIChat:
		return chatStreamDone
	case backend.KindOpenAIResponses:
		return responsesStreamDone
	default:
		return func([]byte) bool { return false }
	}
}

// anthropicStreamDone reports whether the buffered stream tail holds
// Anthropic's terminating event.
func anthropicStreamDone(window []byte) bool {
	return bytes.Contains(window, []byte(`"type":"message_stop"`)) ||
		bytes.Contains(window, []byte("event: message_stop"))
}

// chatStreamDone reports whether the buffered stream tail holds the
// chat-completions sentinel.
func chatStreamDone(window []byte) bool {
	return bytes.Contains(window, []byte("data: [DONE]")) ||
		bytes.Contains(window, []byte("data:[DONE]"))
}

// responsesStreamDone reports whether the buffered stream tail holds a
// terminal Responses API event.
func responsesStreamDone(window []byte) bool {
	return bytes.Contains(window, []byte(`"type":"response.completed"`)) ||
		bytes.Contains(window, []byte(`"type":"response.incomplete"`)) ||
		bytes.Contains(window, []byte(`"type":"response.failed"`)) ||
		bytes.Contains(window, []byte(`"type":"error"`))
}

// pipeSSE relays a server-sent-events body to the client, flushing after
// every read. It reports how many bytes reached the client and whether the
// stream carried its completion marker (checked by done against a rolling
// window of the tail). A failing client write ends the relay as complete:
// with the consumer gone there is nothing left to relay or report.
func pipeSSE(w io.Writer, flush func(), body io.Reader, done func([]byte) bool) (relayed int64, complete bool, err error) {
	buffer := make([]byte, passthroughCopyBufferSize)
	var window []byte
	for {
		read, readErr := body.Read(buffer)
		if read > 0 {
			if _, writeErr := w.Write(buffer[:read]); writeErr != nil {
				return relayed, true, nil
			}
			if flush != nil {
				flush()
			}
			relayed += int64(read)
			window = append(window, buffer[:read]...)
			// Inspect the newly extended window before trimming it. A terminal
			// Responses event can be much larger than retryCompletionWindow (its
			// data payload contains the complete response), with the marker near
			// the beginning. Trimming first would discard that marker and turn a
			// successful stream into a spurious mid-stream failure.
			if done != nil && !complete && done(window) {
				complete = true
			}
			if len(window) > retryCompletionWindow {
				window = window[len(window)-retryCompletionWindow:]
			}
		}
		if readErr != nil {
			return relayed, complete, readErr
		}
	}
}

// gatedWriter commits the response headers the moment the first byte of
// streamed output flows, so a failure before that point can still be answered
// with a clean non-2xx error.
type gatedWriter struct {
	open  func() io.Writer
	w     io.Writer
	wrote int64
}

func (g *gatedWriter) Write(payload []byte) (int, error) {
	g.wrote += int64(len(payload))
	if g.w == nil {
		g.w = g.open()
	}
	return g.w.Write(payload)
}

// streamFailer is implemented by translated-stream writers that can end with
// an explicit in-band failure instead of a normal completion.
type streamFailer interface {
	Fail(message string)
}

// failTranslatedStream ends an already-started translated stream with the
// writer's in-band failure event; writers predating Fail fall back to their
// normal completion.
func failTranslatedStream(writer streamTranslator, message string) {
	if f, ok := writer.(streamFailer); ok {
		f.Fail(message)
		return
	}
	writer.Finish()
}

// relayNativeStreaming streams a successful native response to the client
// byte-for-byte under the common retry contract. done recognizes the wire
// format's completion marker; surface writes the in-band failure in the
// client's dialect once content has flowed; giveUp answers a spent retry
// budget with a clean 502. It reports whether an in-band failure was
// surfaced (content had already reached the client, so no other backend may
// take the request over).
func (s *Server) relayNativeStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	log logrus.FieldLogger,
	rt route,
	resp *backend.Response,
	fetch upstreamFetch,
	done func([]byte) bool,
	surface func(w http.ResponseWriter, message string),
	giveUp func(w http.ResponseWriter, message string),
) bool {
	copyUpstreamRequestID(w.Header(), resp.Header)
	contentType := resp.Header.Get("Content-Type")
	status := resp.Status
	var body io.Reader = resp.Body
	for attempt := 0; ; attempt++ {
		gate := &gatedWriter{open: func() io.Writer {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			} else {
				w.Header().Set("Content-Type", "text/event-stream")
			}
			w.WriteHeader(status)
			return w
		}}
		flush := func() {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		relayed, complete, streamErr := pipeSSE(gate, flush, body, done)
		closeBody(body)

		if complete {
			if attempt > 0 {
				s.metrics.noteRetryOutcome(retryPhaseBody, retryRecovered, rt.backend.Name(), rt.model)
			}
			return false
		}
		if relayed > 0 {
			s.metrics.noteRetryOutcome(retryPhaseBody, retrySurfaced, rt.backend.Name(), rt.model)
			log.WithError(streamErr).Warn("upstream stream broke after content was forwarded; surfacing an in-stream error")
			surface(w, midstreamFailureMessage)
			return true
		}
		if !s.retryOrGiveUp(ctx, w, log, rt, streamErr, attempt, giveUp) {
			return false
		}
		body = s.retryReader(fetch)
	}
}

// relayNativeBuffered buffers a successful native response and relays it
// verbatim once complete, so a mid-transfer break can be retried invisibly.
// A 200 whose JSON body carries a top-level "error" object is not a success —
// fronting gateways (Cloudflare in front of OpenCode Zen among them) serve
// quota and overload errors that way — so it is answered as an upstream
// failure instead of being passed off as a completed turn.
func (s *Server) relayNativeBuffered(
	ctx context.Context,
	w http.ResponseWriter,
	log logrus.FieldLogger,
	rt route,
	resp *backend.Response,
	fetch upstreamFetch,
	giveUp func(w http.ResponseWriter, message string),
) {
	copyUpstreamRequestID(w.Header(), resp.Header)
	data, err := s.fetchResponseBody(ctx, rt, resp, fetch, maxTranslatedResponseBody)
	if err != nil {
		message := fmt.Sprintf("upstream response could not be read: %v", err)
		log.WithError(err).Warn("reading upstream response body failed")
		giveUp(w, message)
		return
	}
	if upstreamErr := errorShapedBody(data); upstreamErr != nil {
		log.WithError(upstreamErr).Warn("upstream answered HTTP 200 with an error body")
		giveUp(w, fmt.Sprintf("upstream returned an error: %v", upstreamErr))
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.Status)
	_, _ = w.Write(data)
}

// errorShapedBody reports whether a buffered 200 body is a JSON object with a
// non-null top-level "error" key, returning it as the upstream's own failure.
// The check is deliberately narrow: any other shape — including bodies that
// merely fail to parse — relays verbatim, since only an explicit error object
// is proof enough to override the upstream's status line.
func errorShapedBody(data []byte) error {
	var probe struct {
		Error *translate.UpstreamError `json:"error"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil // not JSON; not our call to reject
	}
	if probe.Error == nil || (probe.Error.Message == "" && probe.Error.Type == "") {
		return nil
	}
	return probe.Error
}

// relayTranslatedStreaming converts an upstream SSE stream into the client's
// dialect. A broken upstream body is retried transparently for as long as
// nothing has reached the client — the request only ever looks slower. Once
// content has flowed, restarting upstream would duplicate it, so the break is
// surfaced as the protocol's in-band failure instead of a silent truncation.
// It reports whether such an in-band failure was surfaced.
func (s *Server) relayTranslatedStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	log logrus.FieldLogger,
	rt route,
	resp *backend.Response,
	fetch upstreamFetch,
	env translateEnv,
	stream func(env translateEnv, client io.Writer, flush func()) streamTranslator,
	giveUp func(w http.ResponseWriter, message string),
) bool {
	var body io.Reader = resp.Body
	for attempt := 0; ; attempt++ {
		gate := &gatedWriter{open: func() io.Writer {
			responseHeader := w.Header()
			responseHeader.Set("Content-Type", "text/event-stream")
			responseHeader.Set("Cache-Control", "no-cache")
			responseHeader.Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			return w
		}}
		var flush func()
		if flusher, ok := w.(http.Flusher); ok {
			flush = flusher.Flush
		}
		writer := stream(env, gate, flush)
		consumeErr := writer.Consume(body)
		closeBody(body)

		streamErr := consumeErr
		if streamErr == nil && gate.wrote == 0 {
			// A 200 whose translation emitted nothing is the "clean empty
			// stream" that used to reach clients as a silent stop.
			streamErr = errors.New("upstream ended the stream without emitting any events")
		}
		if streamErr == nil {
			if attempt > 0 {
				s.metrics.noteRetryOutcome(retryPhaseBody, retryRecovered, rt.backend.Name(), rt.model)
			}
			return false
		}
		if gate.wrote > 0 {
			s.metrics.noteRetryOutcome(retryPhaseBody, retrySurfaced, rt.backend.Name(), rt.model)
			log.WithError(streamErr).Warn("upstream stream broke after content was forwarded; surfacing an in-stream error")
			failTranslatedStream(writer, midstreamFailureMessage)
			return true
		}
		if !s.retryOrGiveUp(ctx, w, log, rt, streamErr, attempt, giveUp) {
			return false
		}
		body = s.retryReader(fetch)
	}
}

// retryOrGiveUp decides whether another body-phase attempt may start. It
// returns false when the loop must end: either the retry budget is spent and
// the client gets a clean 502, or the client stopped waiting.
func (s *Server) retryOrGiveUp(
	ctx context.Context,
	w http.ResponseWriter,
	log logrus.FieldLogger,
	rt route,
	streamErr error,
	attempt int,
	giveUp func(w http.ResponseWriter, message string),
) bool {
	budget := s.retryBudgetFor(rt.backend.Name())
	if attempt < midstreamRetries && retryPause(ctx, attempt, budget.maxBackoff) {
		s.metrics.noteRetryAttempt(retryPhaseBody, rt.backend.Name(), rt.model)
		log.WithError(streamErr).WithField("attempt", attempt+1).Warn("upstream stream failed before any output; retrying")
		return true
	}
	s.metrics.noteRetryOutcome(retryPhaseBody, retryExhausted, rt.backend.Name(), rt.model)
	log.WithError(streamErr).Warn("upstream stream failed before any output")
	message := midstreamFailureMessage
	if streamErr != nil {
		message = fmt.Sprintf("upstream stream failed: %v", streamErr)
	}
	giveUp(w, message)
	return false
}

// closeBody closes a reader when it also is a closer; upstream bodies and
// sniffers both are.
func closeBody(body io.Reader) {
	if closer, ok := body.(io.Closer); ok {
		_ = closer.Close()
	}
}
