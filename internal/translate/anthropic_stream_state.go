package translate

import (
	"encoding/json"
	"errors"
	"fmt"
)

type anthropicStreamState struct {
	blocks  map[int]*anthropicStreamBlock
	calls   responseCallIDs
	started bool
	stopped bool
	bytes   int
}

type anthropicStreamBlock struct {
	kind     string
	id       string
	sourceID string
	name     string
	input    json.RawMessage
	args     bool
	closed   bool
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
	if event.Type != "message_start" && event.Type != "ping" && !s.started {
		return nil, errors.New("upstream event requires message_start")
	}
	if s.stopped && (event.Type == "content_block_start" || event.Type == "content_block_delta") {
		return nil, errors.New("upstream content arrived after stop_reason")
	}
	switch event.Type {
	case "message_start":
		if s.started || event.Message == nil {
			return nil, errors.New("upstream requires one message_start")
		}
		s.started = true
	case "content_block_start":
		if event.ContentBlock == nil {
			return nil, errors.New("upstream block start requires content")
		}
		block := event.ContentBlock
		sourceID := block.ID
		if old := s.blocks[event.Index]; old != nil {
			if old.closed || old.kind != block.Type || old.sourceID != sourceID || old.name != block.Name {
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
		if block == nil || block.closed || event.Delta == nil {
			return nil, errors.New("upstream delta requires an open block")
		}
		delta := event.Delta
		if delta.Type == "signature_delta" && block.kind == "thinking" {
			return nil, errors.New("opaque thinking signatures require a native route")
		}
		valid := (block.kind == "text" && delta.Type == "text_delta") || (block.kind == "thinking" && delta.Type == "thinking_delta") || (block.kind == "tool_use" && delta.Type == "input_json_delta")
		if !valid {
			return nil, errors.New("upstream delta type contradicts block type")
		}
		if err := s.reserve(len(delta.Text) + len(delta.Thinking) + len(delta.PartialJSON)); err != nil {
			return nil, err
		}
		if delta.Type == "input_json_delta" {
			block.args = true
		}
	case "content_block_stop":
		block := s.blocks[event.Index]
		if block == nil {
			return nil, errors.New("upstream stopped an unknown block")
		}
		if block.closed {
			return nil, nil
		}
		block.closed = true
		if block.kind == "tool_use" && !block.args && len(block.input) > 0 {
			fallback := anthropicEvent{Type: "content_block_delta", Index: event.Index, Delta: &anthropicStreamDelta{Type: "input_json_delta", PartialJSON: string(block.input)}}
			return []anthropicEvent{fallback, event}, nil
		}
	case "message_delta":
		if event.Delta != nil {
			if err := validateAnthropicStop(event.Delta.StopReason); err != nil {
				return nil, err
			}
			if event.Delta.StopReason != "" {
				s.stopped = true
			}
		}
	case "message_stop":
		if !s.started {
			return nil, errors.New("upstream stopped without message_start")
		}
		if !s.stopped {
			return nil, errors.New("upstream stopped without stop_reason")
		}
		for _, block := range s.blocks {
			if !block.closed {
				return nil, errors.New("upstream stopped with open content blocks")
			}
		}
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
