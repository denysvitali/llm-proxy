package shape

import (
	"github.com/denysvitali/llm-proxy/internal/sse"
)

// Stream accumulates an SSE response's shape while its bytes are relayed to
// the client. It implements io.Writer, so the relay passes every byte through
// Write without buffering the body. A stream that is not SSE yields a Body
// with Format "unparsed".
type Stream struct {
	decoder *sse.Decoder
	acc     bodyAccumulator
}

// NewStream returns a Stream ready to consume relayed bytes.
func NewStream() *Stream {
	stream := &Stream{}
	stream.decoder = sse.NewDecoder(func(event sse.Event) (bool, error) {
		stream.acc.frames++
		stream.acc.add(event.Data)
		return false, nil
	})
	return stream
}

// Write feeds relayed bytes into the decoder and never fails the relay.
func (s *Stream) Write(p []byte) (int, error) {
	if _, err := s.decoder.Feed(p); err != nil {
		// A malformed frame ends shape recording, not delivery.
		s.acc.skipped++
	}
	return len(p), nil
}

// Body finalizes and returns the accumulated stream shape. It is safe to call
// once the relay has finished; calling it earlier returns what has been seen.
func (s *Stream) Body() *Body {
	_, _ = s.decoder.End()
	return s.acc.body()
}
