package translate

import (
	"io"

	"github.com/denysvitali/llm-proxy/internal/sse"
)

// scanSSE dispatches complete data frames until the decoder reports a terminal
// event. Payload whitespace is retained and multiline data is joined by SSE rules.
func scanSSE(body io.Reader, decode func([]byte) (bool, error)) error {
	return sse.Scan(body, func(event sse.Event) (bool, error) {
		if len(event.Data) == 0 {
			return false, nil
		}
		return decode(event.Data)
	})
}
