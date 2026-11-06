package sse

import (
	"errors"
	"testing"
)

func TestDecoderPhaseTransitions(t *testing.T) {
	d := NewDecoder(func(Event) (bool, error) { return false, nil })
	if d.phase != decoderAwaitingFirstLine {
		t.Fatalf("initial phase = %v", d.phase)
	}
	if _, err := d.Feed([]byte("\xef\xbb\xbfdata: a\r")); err != nil {
		t.Fatal(err)
	}
	if d.phase != decoderStreaming || d.terminator != lineTerminatorAfterCR {
		t.Fatalf("after first line phase=%v terminator=%v", d.phase, d.terminator)
	}
	if _, err := d.Feed([]byte("\n")); err != nil || d.terminator != lineTerminatorNone {
		t.Fatalf("CRLF fold err=%v terminator=%v", err, d.terminator)
	}

	failure := errors.New("callback")
	d = NewDecoder(func(Event) (bool, error) { return true, failure })
	if stop, err := d.Feed([]byte("data: x\n\ndata: ignored\n\n")); !stop || !errors.Is(err, failure) {
		t.Fatalf("Feed = %v, %v", stop, err)
	}
	if d.phase != decoderStopped {
		t.Fatalf("phase = %v, want stopped", d.phase)
	}
	for _, call := range []func() (bool, error){func() (bool, error) { return d.Feed([]byte("data: y\n\n")) }, d.End} {
		if stop, err := call(); !stop || !errors.Is(err, failure) {
			t.Fatalf("terminal result = %v, %v", stop, err)
		}
	}

	d = NewDecoder(func(Event) (bool, error) { return false, failure })
	if stop, err := d.Feed([]byte("data: x\n\n")); stop || !errors.Is(err, failure) || d.phase != decoderFailed {
		t.Fatalf("Feed = %v, %v phase=%v", stop, err, d.phase)
	}
	if stop, err := d.End(); stop || !errors.Is(err, failure) {
		t.Fatalf("End = %v, %v", stop, err)
	}
}

func TestDecoderBOMOnlyOnFirstLine(t *testing.T) {
	var got []string
	d := NewDecoder(func(event Event) (bool, error) {
		got = append(got, string(event.Data))
		return false, nil
	})
	if _, err := d.Feed([]byte("data: a\n\n\xef\xbb\xbfdata: b\n\ndata: c\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.End(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("events = %q, want [a c] (BOM on a later line is not a data field)", got)
	}
}

func TestDecoderPhaseStrings(t *testing.T) {
	for phase, want := range map[decoderPhase]string{decoderAwaitingFirstLine: "awaiting_first_line", decoderStreaming: "streaming",
		decoderStopped: "stopped", decoderFailed: "failed", decoderPhase(9): "decoderPhase(9)"} {
		if phase.String() != want {
			t.Errorf("%d.String() = %q, want %q", int(phase), phase.String(), want)
		}
	}
	if lineTerminatorAfterCR.String() != "after_cr" || lineTerminatorNone.String() != "none" {
		t.Error("lineTerminator strings")
	}
}
