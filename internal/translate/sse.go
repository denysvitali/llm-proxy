package translate

import (
	"bufio"
	"io"
	"strings"
)

const maxStreamLine = 8 << 20

// scanSSE passes nonempty data lines to decode until it reports a terminal
// event or the reader ends. All translators share framing and size limits;
// each decoder owns malformed JSON handling, completion markers and errors.
// The supported upstream dialects carry one JSON payload per data line.
func scanSSE(body io.Reader, decode func([]byte) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		stop, err := decode([]byte(payload))
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return scanner.Err()
}
