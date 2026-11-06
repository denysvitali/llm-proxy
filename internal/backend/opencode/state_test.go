package opencode

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFreeQuotaLatchTransitions(t *testing.T) {
	now := time.Now()
	if got := (freeQuota{until: now.Add(time.Minute)}).stateAt(now); got != quotaArmed {
		t.Fatalf("future reset state = %v, want armed", got)
	}
	if got := (freeQuota{until: now}).stateAt(now); got != quotaLapsed {
		t.Fatalf("reset at now state = %v, want lapsed", got)
	}
	c := &Client{}
	c.armFreeQuotaLocked("live", freeQuota{until: now.Add(time.Hour), header: http.Header{}})
	c.armFreeQuotaLocked("old", freeQuota{until: now.Add(-time.Second)})
	if resp := c.freeQuotaResponse("old"); resp != nil {
		t.Fatalf("lapsed latch replayed a response")
	}
	if _, ok := c.quotas["old"]; ok {
		t.Fatalf("lapsed latch was not swept")
	}
	resp := c.freeQuotaResponse("live")
	if resp == nil || resp.Status != 429 {
		t.Fatalf("armed latch response = %+v", resp)
	}
	if c.freeQuotaResponse("never") != nil {
		t.Fatalf("unarmed model replayed a response")
	}
}

type errAfterReader struct {
	data string
	err  error
	done bool
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

func TestToolNameRenamingReaderSourceState(t *testing.T) {
	r := newToolNameRenamingReader(strings.NewReader("data: [DONE]\n: tail"), nil)
	out, err := io.ReadAll(r)
	if err != nil || string(out) != "data: [DONE]\n: tail" {
		t.Fatalf("ReadAll = %q, %v", out, err)
	}
	if r.state != renameSourceDone {
		t.Fatalf("state after EOF = %v, want done", r.state)
	}
	if n, err := r.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("Read after done = %d, %v", n, err)
	}

	boom := errors.New("boom")
	r = newToolNameRenamingReader(&errAfterReader{data: "data: x\n", err: boom}, nil)
	buf := make([]byte, 64)
	if n, err := r.Read(buf); err != nil || string(buf[:n]) != "data: x\n" {
		t.Fatalf("first Read = %q, %v", buf[:n], err)
	}
	if r.state != renameSourceOpen {
		t.Fatalf("state after full line = %v, want open", r.state)
	}
	if _, err := r.Read(buf); !errors.Is(err, boom) {
		t.Fatalf("second Read err = %v, want boom", err)
	}
	if r.state != renameSourceDone {
		t.Fatalf("state after error = %v, want done", r.state)
	}
	if _, err := r.Read(buf); err != io.EOF {
		t.Fatalf("Read after error = %v, want EOF", err)
	}
}

func TestAggregatorProgressGates(t *testing.T) {
	if _, err := aggregateChatSSE(strings.NewReader("data: [DONE]\n")); err != nil {
		t.Fatalf("chat stream with only [DONE] failed: %v", err)
	}
	if _, err := aggregateChatSSE(strings.NewReader(": keepalive\n")); err == nil {
		t.Fatalf("silent chat stream succeeded")
	}
	if _, err := aggregateAnthropicSSE(strings.NewReader(`data: {"type":"ping"}` + "\n")); err != nil {
		t.Fatalf("anthropic stream with an event failed: %v", err)
	}
	if _, err := aggregateAnthropicSSE(strings.NewReader("data: [DONE]\n")); err == nil {
		t.Fatalf("silent anthropic stream succeeded")
	}
}
