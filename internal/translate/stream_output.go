package translate

import (
	"encoding/json"
	"io"
)

// writeStreamFrame publishes one complete frame and surfaces short writes. A
// caller retains the first error so no later terminal event hides a broken write.
func writeStreamFrame(writer io.Writer, flush func(), event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, len(data)+len(event)+20)
	if event != "" {
		frame = append(frame, "event: "...)
		frame = append(frame, event...)
		frame = append(frame, '\n')
	}
	frame = append(frame, "data: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	return writeStreamBytes(writer, flush, frame)
}

func writeStreamBytes(writer io.Writer, flush func(), frame []byte) error {
	n, err := writer.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	if err == nil && flush != nil {
		flush()
	}
	return err
}
