package translate

import (
	"encoding/json"
	"errors"
	"fmt"
)

// anthropicStreamMessagePhase is the message-level lifecycle of an upstream
// Messages stream as seen by anthropicStreamState.
//
//	awaiting  --message_start-->            streaming
//	streaming --message_delta(stop_reason)--> stopped
//	stopped   --message_delta(stop_reason)--> stopped
//
// Only ping (and error, which always fails) is legal while awaiting. A second
// message_start is rejected from any later phase, and content_block_start or
// content_block_delta is rejected once stopped. message_stop does not change
// the phase: it is terminal for the consuming writer, which stops reading.
type anthropicStreamMessagePhase uint8

const (
	anthropicStreamAwaitingStart anthropicStreamMessagePhase = iota
	anthropicStreamStreaming
	anthropicStreamStopped
)

func (p anthropicStreamMessagePhase) String() string {
	switch p {
	case anthropicStreamAwaitingStart:
		return "awaiting_message_start"
	case anthropicStreamStreaming:
		return "streaming"
	case anthropicStreamStopped:
		return "stopped"
	}
	return fmt.Sprintf("anthropicStreamMessagePhase(%d)", uint8(p))
}

// anthropicStreamBlockPhase is the lifecycle of one upstream content block.
//
//	open      --input_json_delta--> open_args
//	open      --content_block_stop--> closed
//	open_args --content_block_stop--> closed
//
// Deltas are legal only while open or open_args; a repeated stop on a closed
// block is ignored. A tool block closing while still open (no streamed
// arguments) replays its start-time input as one synthetic delta.
type anthropicStreamBlockPhase uint8

const (
	anthropicStreamBlockOpen anthropicStreamBlockPhase = iota
	anthropicStreamBlockOpenArgs
	anthropicStreamBlockClosed
)

func (p anthropicStreamBlockPhase) String() string {
	switch p {
	case anthropicStreamBlockOpen:
		return "open"
	case anthropicStreamBlockOpenArgs:
		return "open_args"
	case anthropicStreamBlockClosed:
		return "closed"
	}
	return fmt.Sprintf("anthropicStreamBlockPhase(%d)", uint8(p))
}

type anthropicStreamState struct {
	blocks map[int]*anthropicStreamBlock
	calls  responseCallIDs
	phase  anthropicStreamMessagePhase
	bytes  int
}

// begin moves awaiting -> streaming on the single legal message_start.
func (s *anthropicStreamState) begin() error {
	if s.phase != anthropicStreamAwaitingStart {
		return errors.New("upstream requires one message_start")
	}
	s.phase = anthropicStreamStreaming
	return nil
}

// stop records a stop_reason; it is legal (and idempotent) once streaming.
func (s *anthropicStreamState) stop() {
	s.phase = anthropicStreamStopped
}

type anthropicStreamBlock struct {
	kind     string
	id       string
	sourceID string
	name     string
	input    json.RawMessage
	phase    anthropicStreamBlockPhase
}

func (b *anthropicStreamBlock) open() bool {
	return b.phase != anthropicStreamBlockClosed
}

// sawArgs moves open -> open_args; it is a no-op once arguments were seen.
func (b *anthropicStreamBlock) sawArgs() {
	if b.phase == anthropicStreamBlockOpen {
		b.phase = anthropicStreamBlockOpenArgs
	}
}

// close moves an open block to closed and reports whether the transition
// happened and whether it closed without streamed arguments.
func (b *anthropicStreamBlock) close() (closed, withoutArgs bool) {
	if b.phase == anthropicStreamBlockClosed {
		return false, false
	}
	withoutArgs = b.phase == anthropicStreamBlockOpen
	b.phase = anthropicStreamBlockClosed
	return true, withoutArgs
}

func (s *anthropicStreamState) consume(event anthropicEvent) ([]anthropicEvent, error) {
	if event.Type == "error" {
		upstream := &UpstreamError{Type: "api_error", Message: "upstream stream failed"}
		if event.Error != nil {
			if event.Error.Type != "" {
				upstream.Type = event.Error.Type
			}
			if event.Error.Message != "" {
				upstream.Message = event.Error.Message
			}
		}
		return nil, upstream
	}
	if event.Index < 0 {
		return nil, errors.New("upstream block index must be non-negative")
	}
	if s.blocks == nil {
		s.blocks = map[int]*anthropicStreamBlock{}
		s.calls = responseCallIDs{}
	}
	if event.Type != "message_start" && event.Type != "ping" && s.phase == anthropicStreamAwaitingStart {
		return nil, errors.New("upstream event requires message_start")
	}
	if s.phase == anthropicStreamStopped && (event.Type == "content_block_start" || event.Type == "content_block_delta") {
		return nil, errors.New("upstream content arrived after stop_reason")
	}
	switch event.Type {
	case "message_start":
		if event.Message == nil {
			return nil, errors.New("upstream requires one message_start")
		}
		if err := s.begin(); err != nil {
			return nil, err
		}
	case "content_block_start":
		if event.ContentBlock == nil {
			return nil, errors.New("upstream block start requires content")
		}
		block := event.ContentBlock
		sourceID := block.ID
		if old := s.blocks[event.Index]; old != nil {
			if !old.open() || old.kind != block.Type || old.sourceID != sourceID || old.name != block.Name {
				return nil, errors.New("upstream changed block identity")
			}
			return nil, nil
		}
		if len(s.blocks) >= maxTranslatedStreamItems {
			return nil, errors.New("upstream stream has too many blocks")
		}
		switch block.Type {
		case "text", "thinking":
		case "tool_use":
			if block.Name == "" {
				return nil, errors.New("upstream tool block requires a name")
			}
			id, err := s.calls.resolve(block.ID, "")
			if err != nil {
				return nil, err
			}
			block.ID = id
			if len(block.Input) > 0 {
				if _, err := argumentsJSON(string(block.Input)); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("unsupported upstream block %q", block.Type)
		}
		s.blocks[event.Index] = &anthropicStreamBlock{kind: block.Type, id: block.ID, sourceID: sourceID, name: block.Name, input: block.Input}
		if err := s.reserve(len(block.Text) + len(block.Thinking) + len(block.Input)); err != nil {
			return nil, err
		}
	case "content_block_delta":
		block := s.blocks[event.Index]
		if block == nil || !block.open() || event.Delta == nil {
			return nil, errors.New("upstream delta requires an open block")
		}
		delta := event.Delta
		// A thinking block's provider signature is opaque state no translated
		// wire can carry, so it is dropped here exactly as the buffered path
		// drops it (see "provider signatures are not replayed" in README).
		// Failing instead would break every translated stream from a provider
		// that signs its thinking blocks — MiniMax Code signs every one, and
		// the client sees an ordinary answer cut short.
		if delta.Type == "signature_delta" && block.kind == "thinking" {
			return nil, nil
		}
		valid := (block.kind == "text" && delta.Type == "text_delta") || (block.kind == "thinking" && delta.Type == "thinking_delta") || (block.kind == "tool_use" && delta.Type == "input_json_delta")
		if !valid {
			return nil, errors.New("upstream delta type contradicts block type")
		}
		if err := s.reserve(len(delta.Text) + len(delta.Thinking) + len(delta.PartialJSON)); err != nil {
			return nil, err
		}
		if delta.Type == "input_json_delta" {
			block.sawArgs()
		}
	case "content_block_stop":
		block := s.blocks[event.Index]
		if block == nil {
			return nil, errors.New("upstream stopped an unknown block")
		}
		closed, withoutArgs := block.close()
		if !closed {
			return nil, nil
		}
		if block.kind == "tool_use" && withoutArgs && len(block.input) > 0 {
			fallback := anthropicEvent{Type: "content_block_delta", Index: event.Index, Delta: &anthropicStreamDelta{Type: "input_json_delta", PartialJSON: string(block.input)}}
			return []anthropicEvent{fallback, event}, nil
		}
	case "message_delta":
		if event.Delta != nil {
			if err := validateAnthropicStop(event.Delta.StopReason); err != nil {
				return nil, err
			}
			if event.Delta.StopReason != "" {
				s.stop()
			}
		}
	case "message_stop":
		// Unreachable: the awaiting-phase guard above already reports
		// "upstream event requires message_start" for a bare message_stop.
		if s.phase == anthropicStreamAwaitingStart {
			return nil, errors.New("upstream stopped without message_start")
		}
		// A message_delta carrying stop_reason and content_block_stop for every
		// open block normally precede message_stop, but sloppy upstreams omit
		// them. Native passthrough accepts a bare message_stop as terminal
		// (streamDoneChecker), so the translated path must not fail a stream
		// the native path would accept; the writer defaults the stop reason.
	case "ping":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported upstream Messages event %q", event.Type)
	}
	return []anthropicEvent{event}, nil
}

func (s *anthropicStreamState) reserve(bytes int) error {
	if bytes > maxTranslatedStreamBytes-s.bytes {
		return errors.New("upstream translated stream exceeds content limit")
	}
	s.bytes += bytes
	return nil
}
