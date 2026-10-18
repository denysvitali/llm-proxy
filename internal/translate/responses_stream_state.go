package translate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxTranslatedStreamBytes = 16 << 20
const maxTranslatedStreamItems = 4096

// responsesStreamState reconciles snapshots with deltas before either outbound
// dialect sees them. Announcement arguments are pending, never a delta prefix.
type responsesStreamState struct {
	items map[int]*responsesStreamItem
	ids   map[string]int
	calls responseCallIDs
	bytes int
}

type responsesStreamItem struct {
	index        int
	sourceCallID string
	item         responsesOutputItem
	args         strings.Builder
	argDelta     bool
	argDone      bool
	closed       bool
	parts        map[int]*responsesStreamPart
}

type responsesStreamPart struct {
	kind string
	text strings.Builder
	seen bool
	done bool
}

func (s *responsesStreamState) consume(event responsesEvent) ([]responsesEvent, error) {
	if s.items == nil {
		s.items = map[int]*responsesStreamItem{}
		s.ids = map[string]int{}
		s.calls = responseCallIDs{}
	}
	if event.OutputIndex < 0 || event.ContentIndex < 0 {
		return nil, errors.New("upstream output and content indices must be non-negative")
	}
	if event.Type == "response.completed" || event.Type == "response.incomplete" {
		if event.Response == nil {
			return nil, errors.New("upstream terminal event requires a response")
		}
		if event.Response.Status != "" && event.Response.Status != strings.TrimPrefix(event.Type, "response.") {
			return nil, errors.New("upstream terminal event contradicts response status")
		}
		response := *event.Response
		response.Status = strings.TrimPrefix(event.Type, "response.")
		if _, err := responsesFinish(response, false); err != nil {
			return nil, err
		}
		event.Response = &response
		var output []responsesEvent
		for index, item := range response.Output {
			frames, err := s.consume(responsesEvent{Type: "response.output_item.done", OutputIndex: index, Item: &item})
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		indices := make([]int, 0, len(s.items))
		for index := range s.items {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			item := s.items[index]
			if item.closed {
				continue
			}
			// Deltas supersede announcement placeholders when no final snapshot exists.
			snapshot := item.item
			if item.argDelta || item.argDone {
				snapshot.Arguments = item.args.String()
			}
			frames, err := s.consume(responsesEvent{Type: "response.output_item.done", OutputIndex: index, Item: &snapshot})
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		return append(output, event), nil
	}
	switch event.Type {
	case "response.output_item.added", "response.output_item.done":
		if event.Item == nil {
			return nil, errors.New("upstream item event requires an item")
		}
		item, added, err := s.item(event.OutputIndex, *event.Item)
		if err != nil {
			return nil, err
		}
		if event.Type == "response.output_item.added" && item.closed {
			return nil, errors.New("upstream reannounced a completed item")
		}
		var output []responsesEvent
		if added {
			announcement := item.item
			announcement.Arguments = ""
			announcement.Content = nil
			output = append(output, responsesEvent{Type: "response.output_item.added", OutputIndex: event.OutputIndex, Item: &announcement})
		}
		if event.Item.Arguments != "" {
			item.item.Arguments = event.Item.Arguments
		}
		if len(event.Item.Content) > 0 {
			item.item.Content = event.Item.Content
		}
		if event.Type == "response.output_item.added" {
			return output, nil
		}
		if item.item.Type == "function_call" {
			frames, err := s.argumentsDone(event.OutputIndex, item, item.item.Arguments)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		for index, part := range item.item.Content {
			kind := part.Type
			text := part.Text
			if kind == "refusal" {
				text = part.Refusal
			}
			if kind != "output_text" && kind != "refusal" && kind != "reasoning_text" {
				return nil, fmt.Errorf("unsupported upstream streamed content %q", kind)
			}
			frames, err := s.textDone(event.OutputIndex, index, item, kind, text)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		if item.closed {
			return output, nil
		}
		item.closed = true
		return append(output, event), nil
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		item := s.items[event.OutputIndex]
		if item == nil || item.item.Type != "function_call" {
			return nil, errors.New("upstream arguments require an announced function call")
		}
		if err := s.checkID(event, item); err != nil {
			return nil, err
		}
		if event.Type == "response.function_call_arguments.done" {
			return s.argumentsDone(event.OutputIndex, item, event.Arguments)
		}
		if item.closed || item.argDone {
			return nil, errors.New("upstream arguments arrived after call completion")
		}
		if err := s.reserve(len(event.Delta)); err != nil {
			return nil, err
		}
		item.args.WriteString(event.Delta)
		item.argDelta = true
		return []responsesEvent{event}, nil
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.output_text.done", "response.refusal.done", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		kind := "output_text"
		if strings.Contains(event.Type, "refusal") {
			kind = "refusal"
		}
		if strings.Contains(event.Type, "reasoning") {
			kind = "reasoning_text"
		}
		item := s.items[event.OutputIndex]
		if item == nil {
			itemType := "message"
			if kind == "reasoning_text" {
				itemType = "reasoning"
			}
			var err error
			item, _, err = s.item(event.OutputIndex, responsesOutputItem{Type: itemType, ID: event.ItemID})
			if err != nil {
				return nil, err
			}
		}
		if err := s.checkID(event, item); err != nil {
			return nil, err
		}
		if strings.HasSuffix(event.Type, ".done") {
			text := event.Text
			if kind == "refusal" {
				text = event.Refusal
			}
			return s.textDone(event.OutputIndex, event.ContentIndex, item, kind, text)
		}
		if item.closed {
			return nil, errors.New("upstream text arrived after item completion")
		}
		part, err := s.part(item, event.ContentIndex, kind)
		if err != nil {
			return nil, err
		}
		if part.done {
			return nil, errors.New("upstream text arrived after part completion")
		}
		if err := s.reserve(len(event.Delta)); err != nil {
			return nil, err
		}
		part.text.WriteString(event.Delta)
		part.seen = true
		return []responsesEvent{event}, nil
	default:
		return []responsesEvent{event}, nil
	}
}

func (s *responsesStreamState) item(index int, incoming responsesOutputItem) (*responsesStreamItem, bool, error) {
	if incoming.Type != "message" && incoming.Type != "function_call" && incoming.Type != "reasoning" {
		return nil, false, fmt.Errorf("unsupported upstream streamed item %q", incoming.Type)
	}
	if len(incoming.Arguments) > maxTranslatedStreamBytes {
		return nil, false, errors.New("upstream argument snapshot exceeds stream limit")
	}
	if item := s.items[index]; item != nil {
		if item.item.Type != incoming.Type || (incoming.ID != "" && item.item.ID != "" && item.item.ID != incoming.ID) ||
			(incoming.CallID != "" && incoming.CallID != item.item.CallID && incoming.CallID != item.sourceCallID) || (incoming.Name != "" && incoming.Name != item.item.Name) {
			return nil, false, errors.New("upstream changed item identity at an existing index")
		}
		if err := s.checkID(responsesEvent{ItemID: incoming.ID}, item); err != nil {
			return nil, false, err
		}
		return item, false, nil
	}
	if len(s.items) >= maxTranslatedStreamItems {
		return nil, false, errors.New("upstream stream has too many items")
	}
	if incoming.ID != "" {
		if old, ok := s.ids[incoming.ID]; ok && old != index {
			return nil, false, errors.New("upstream reused an output item id")
		}
		s.ids[incoming.ID] = index
	}
	sourceCallID := incoming.CallID
	if incoming.Type == "function_call" {
		if incoming.Name == "" {
			return nil, false, errors.New("upstream function call requires a name")
		}
		id, err := s.calls.resolve(incoming.CallID, "")
		if err != nil {
			return nil, false, err
		}
		incoming.CallID = id
	}
	item := &responsesStreamItem{index: index, sourceCallID: sourceCallID, item: incoming, parts: map[int]*responsesStreamPart{}}
	s.items[index] = item
	return item, true, nil
}

func (s *responsesStreamState) checkID(event responsesEvent, item *responsesStreamItem) error {
	if event.ItemID == "" {
		return nil
	}
	if item.item.ID != "" && event.ItemID != item.item.ID {
		return errors.New("upstream delta changed output item id")
	}
	if index, ok := s.ids[event.ItemID]; ok && index != item.index {
		return errors.New("upstream reused an output item id")
	}
	s.ids[event.ItemID] = item.index
	item.item.ID = event.ItemID
	return nil
}

func (s *responsesStreamState) reserve(bytes int) error {
	if bytes > maxTranslatedStreamBytes-s.bytes {
		return errors.New("upstream translated stream exceeds content limit")
	}
	s.bytes += bytes
	return nil
}

func (s *responsesStreamState) argumentsDone(index int, item *responsesStreamItem, args string) ([]responsesEvent, error) {
	if item.argDone {
		if args != "" && args != item.args.String() {
			return nil, errors.New("upstream repeated arguments disagree with completed call")
		}
		return nil, nil
	}
	if item.argDelta {
		if args != "" && args != item.args.String() {
			return nil, errors.New("upstream completed arguments disagree with deltas")
		}
	} else if args != "" {
		if err := s.reserve(len(args)); err != nil {
			return nil, err
		}
		item.args.WriteString(args)
	}
	item.argDone = true
	if item.argDelta || args == "" {
		return nil, nil
	}
	return []responsesEvent{{Type: "response.function_call_arguments.delta", OutputIndex: index, ItemID: item.item.ID, Delta: args}}, nil
}

func (s *responsesStreamState) part(item *responsesStreamItem, index int, kind string) (*responsesStreamPart, error) {
	if part := item.parts[index]; part != nil {
		if part.kind != kind {
			return nil, errors.New("upstream changed content part type")
		}
		return part, nil
	}
	if len(item.parts) >= maxTranslatedStreamItems {
		return nil, errors.New("upstream stream has too many content parts")
	}
	part := &responsesStreamPart{kind: kind}
	item.parts[index] = part
	return part, nil
}

func (s *responsesStreamState) textDone(index, content int, item *responsesStreamItem, kind, text string) ([]responsesEvent, error) {
	part, err := s.part(item, content, kind)
	if err != nil {
		return nil, err
	}
	if part.done {
		if text != part.text.String() {
			return nil, errors.New("upstream repeated text disagrees with completed part")
		}
		return nil, nil
	}
	if part.seen {
		if text != part.text.String() {
			return nil, errors.New("upstream completed text disagrees with deltas")
		}
		part.done = true
		return nil, nil
	}
	if err := s.reserve(len(text)); err != nil {
		return nil, err
	}
	part.text.WriteString(text)
	part.seen, part.done = true, true
	event := "response.output_text.delta"
	if kind == "refusal" {
		event = "response.refusal.delta"
	}
	if kind == "reasoning_text" {
		event = "response.reasoning_text.delta"
	}
	return []responsesEvent{{Type: event, OutputIndex: index, ContentIndex: content, ItemID: item.item.ID, Delta: text}}, nil
}
