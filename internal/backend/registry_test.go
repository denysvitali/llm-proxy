package backend

import (
	"context"
	"errors"
	"testing"
)

type stubBackend struct{ name string }

func (s *stubBackend) Name() string { return s.name }
func (s *stubBackend) Models(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (s *stubBackend) Supports(kind Kind) bool { return false }
func (s *stubBackend) Send(ctx context.Context, req *Request) (*Response, error) {
	return nil, errors.New("not implemented")
}

func TestRegistryRoundTrip(t *testing.T) {
	Register("test-stub", func(opts Options) (Backend, error) {
		return &stubBackend{name: "test-stub"}, nil
	})
	if !Has("test-stub") {
		t.Fatal("Has(test-stub) = false after Register")
	}
	b, err := New("test-stub", Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if b.Name() != "test-stub" {
		t.Fatalf("Name() = %q, want %q", b.Name(), "test-stub")
	}
}

func TestNewUnknownType(t *testing.T) {
	if _, err := New("does-not-exist", Options{}); err == nil {
		t.Fatal("New with unknown type should fail")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	Register("dup-test", func(Options) (Backend, error) { return nil, errors.New("x") })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	Register("dup-test", func(Options) (Backend, error) { return nil, errors.New("x") })
}
