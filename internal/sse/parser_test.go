package sse

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestDecoderFragmentedFrames(t *testing.T) {
	input := "\xef\xbb\xbf: heartbeat\r\nevent: delta\r\nid: first\r\ndata:  leading \r\ndata:\r\ndata:tail\r\n\r\n" +
		"event: ignored\n\n" + "data: next\r\r" + "data: unfinished\n"
	want := []Event{
		{Data: []byte(" leading \n\ntail"), Type: "delta", ID: "first", HasData: true},
		{Data: []byte("next"), HasData: true},
	}
	for size := 1; size <= len(input); size++ {
		var got []Event
		d := NewDecoder(func(event Event) (bool, error) {
			event.Data = append([]byte(nil), event.Data...)
			got = append(got, event)
			return false, nil
		})
		for start := 0; start < len(input); start += size {
			if stop, err := d.Feed([]byte(input[start:min(start+size, len(input))])); stop || err != nil {
				t.Fatalf("fragment size %d: stop=%v, err=%v", size, stop, err)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fragment size %d: events=%#v, want=%#v", size, got, want)
		}
	}
}

func TestDecoderEmptyDataAndMetadata(t *testing.T) {
	var got []Event
	err := Scan(strings.NewReader("id: one\nid: bad\x00id\nevent: first\nevent: last\nretry: 1000\ndata\n\n"), func(event Event) (bool, error) {
		got = append(got, event)
		return false, nil
	})
	if err != nil || len(got) != 1 || string(got[0].Data) != "" || !got[0].HasData || got[0].Type != "last" || got[0].ID != "one" {
		t.Fatalf("events=%#v, err=%v", got, err)
	}
}

func TestDecoderStickyStopAndFailure(t *testing.T) {
	failure := errors.New("callback failure")
	for _, callbackErr := range []error{nil, failure} {
		calls := 0
		d := NewDecoder(func(Event) (bool, error) {
			calls++
			return callbackErr == nil, callbackErr
		})
		for range 2 {
			stop, err := d.Feed([]byte("data: first\n\ndata: second\n\n"))
			if stop != (callbackErr == nil) || !errors.Is(err, callbackErr) {
				t.Fatalf("stop=%v, err=%v, want err=%v", stop, err, callbackErr)
			}
		}
		if calls != 1 {
			t.Fatalf("calls=%d, want 1", calls)
		}
	}
}

func TestDecoderFrameLimitAndReset(t *testing.T) {
	for _, input := range []string{
		"data: " + strings.Repeat("x", MaxFrameSize),
		strings.Repeat(": comment\n", MaxFrameSize/10+1),
		strings.Repeat("data:x\n", MaxFrameSize/7+1),
		strings.Repeat(":x\r\n", MaxFrameSize/4+1),
	} {
		called := false
		err := Scan(strings.NewReader(input), func(Event) (bool, error) { called = true; return false, nil })
		if err == nil || called {
			t.Fatalf("oversized frame: err=%v, called=%v", err, called)
		}
	}
	calls := 0
	frame := "data:" + strings.Repeat("x", MaxFrameSize-7) + "\n\n"
	err := Scan(strings.NewReader(frame+frame), func(Event) (bool, error) { calls++; return false, nil })
	if err != nil || calls != 2 {
		t.Fatalf("frame reset: err=%v, calls=%d", err, calls)
	}
}

func TestScanReadErrorsAndUnfinishedEOF(t *testing.T) {
	failure := errors.New("read failure")
	// A truncated last line is discarded at EOF; a newline-terminated data
	// line is complete enough to dispatch, even without the blank line.
	for _, suffix := range []string{"data: partial", "event: done\n\n"} {
		calls := 0
		if err := Scan(strings.NewReader(suffix), func(Event) (bool, error) { calls++; return false, nil }); err != nil || calls != 0 {
			t.Fatalf("suffix %q: err=%v, calls=%d", suffix, err, calls)
		}
	}
	calls := 0
	if err := Scan(strings.NewReader("data: partial\n"), func(Event) (bool, error) { calls++; return false, nil }); err != nil || calls != 1 {
		t.Fatalf("terminated suffix: err=%v, calls=%d", err, calls)
	}
	for _, stop := range []bool{false, true} {
		calls := 0
		reader := &finalRead{data: []byte("data: first\n\n"), err: failure}
		err := Scan(reader, func(Event) (bool, error) { calls++; return stop, nil })
		if calls != 1 || (stop && err != nil) || (!stop && !errors.Is(err, failure)) {
			t.Fatalf("stop=%v: err=%v, calls=%d", stop, err, calls)
		}
	}
}

type finalRead struct {
	data []byte
	err  error
}

func (r *finalRead) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}
