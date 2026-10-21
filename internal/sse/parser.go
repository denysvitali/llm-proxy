package sse

import (
	"bytes"
	"fmt"
	"io"
)

const MaxFrameSize = 8 << 20

// Event contains the data and metadata of one complete SSE frame.
// Data lines are joined with a newline, as required by the SSE protocol.
type Event struct {
	Data    []byte
	Type    string
	ID      string
	HasData bool
}

// Decoder accepts arbitrarily fragmented reads. The callback's data is valid
// only during the call. A true callback result stops decoding immediately.
type Decoder struct {
	line      []byte
	event     Event
	frameSize int
	skipLF    bool
	started   bool
	stopped   bool
	failure   error
	decode    func(Event) (bool, error)
}

func NewDecoder(decode func(Event) (bool, error)) *Decoder {
	return &Decoder{decode: decode}
}

func (d *Decoder) Feed(p []byte) (bool, error) {
	if d.failure != nil || d.stopped {
		return d.stopped, d.failure
	}
	for _, b := range p {
		if d.skipLF {
			d.skipLF = false
			if b == '\n' {
				if d.frameSize > 0 {
					d.frameSize++
					if d.frameSize > MaxFrameSize {
						d.failure = fmt.Errorf("SSE frame exceeds %d bytes", MaxFrameSize)
						return false, d.failure
					}
				}
				continue
			}
		}
		d.frameSize++
		if d.frameSize > MaxFrameSize {
			d.failure = fmt.Errorf("SSE frame exceeds %d bytes", MaxFrameSize)
			return false, d.failure
		}
		if b == '\r' || b == '\n' {
			d.skipLF = b == '\r'
			d.stopped, d.failure = d.finishLine()
			d.line = d.line[:0]
			if d.stopped || d.failure != nil {
				return d.stopped, d.failure
			}
		} else {
			d.line = append(d.line, b)
		}
	}
	return false, nil
}

// End finishes the stream at EOF. The SSE spec discards pending events at EOF
// because a stream may have been cut mid-frame, but a data line that was
// newline-terminated is not cut — only the frame's blank line is missing, and
// servers routinely close the stream right after a terminal data line. Such
// pending events are dispatched so a final [DONE], usage chunk, or message_stop
// still counts; a truncated last line without its newline stays discarded.
func (d *Decoder) End() (bool, error) {
	if d.failure != nil {
		return d.stopped, d.failure
	}
	if d.stopped || len(d.line) > 0 || !d.event.HasData || len(d.event.Data) == 0 {
		return d.stopped, nil
	}
	return d.finishLine()
}

func (d *Decoder) finishLine() (bool, error) {
	line := d.line
	if !d.started {
		d.started = true
		line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
	}
	if len(line) == 0 {
		event := d.event
		if event.HasData && len(event.Data) > 0 {
			event.Data = event.Data[:len(event.Data)-1]
		}
		var stop bool
		var err error
		if event.HasData {
			stop, err = d.decode(event)
		}
		d.event.Data = d.event.Data[:0]
		d.event.Type, d.event.ID = "", ""
		d.event.HasData = false
		d.frameSize = 0
		return stop, err
	}
	if line[0] == ':' {
		return false, nil
	}
	field, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "data":
		d.event.HasData = true
		d.event.Data = append(d.event.Data, value...)
		d.event.Data = append(d.event.Data, '\n')
	case "event":
		d.event.Type = string(value)
	case "id":
		if !bytes.ContainsRune(value, 0) {
			d.event.ID = string(value)
		}
	}
	return false, nil
}

// Scan dispatches complete frames only. An unfinished frame at EOF is not an
// event and cannot establish successful stream completion.
func Scan(body io.Reader, decode func(Event) (bool, error)) error {
	d := NewDecoder(decode)
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buffer)
		if n > 0 {
			stop, err := d.Feed(buffer[:n])
			if err != nil || stop {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if _, err := d.End(); err != nil {
					return err
				}
				return nil
			}
			return readErr
		}
	}
}
