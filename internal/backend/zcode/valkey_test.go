package zcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestValkeyCaptchaStoreSharesAndExpiresProof(t *testing.T) {
	mini := miniredis.RunT(t)
	url := fmt.Sprintf("redis://%s", mini.Addr())
	first, err := NewValkeyCaptchaStore(url, "test:llm-proxy:")
	if err != nil {
		t.Fatalf("NewValkeyCaptchaStore(first): %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := NewValkeyCaptchaStore(url, "test:llm-proxy:")
	if err != nil {
		t.Fatalf("NewValkeyCaptchaStore(second): %v", err)
	}
	defer func() { _ = second.Close() }()

	issuedAt := time.Now()
	if err := first.Set(context.Background(), "shared-param", issuedAt); err != nil {
		t.Fatalf("Set: %v", err)
	}
	param, gotIssuedAt, err := second.Get(context.Background())
	if err != nil {
		t.Fatalf("Get from second replica: %v", err)
	}
	if param != "shared-param" || !gotIssuedAt.Equal(issuedAt) {
		t.Fatalf("shared record = %q at %v, want shared-param at %v", param, gotIssuedAt, issuedAt)
	}

	if err := second.DeleteIfMatch(context.Background(), "wrong-param", gotIssuedAt); err != nil {
		t.Fatalf("DeleteIfMatch(wrong): %v", err)
	}
	if param, _, err := first.Get(context.Background()); err != nil || param != "shared-param" {
		t.Fatalf("wrong proof deleted record: %q, %v", param, err)
	}
	if err := second.DeleteIfMatch(context.Background(), param, gotIssuedAt); err != nil {
		t.Fatalf("DeleteIfMatch: %v", err)
	}
	if _, _, err := first.Get(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get after delete error = %v, want not-exist", err)
	}

	if err := first.Set(context.Background(), "one-use-param", time.Now()); err != nil {
		t.Fatalf("Set one-use: %v", err)
	}
	if got, _, err := second.Take(context.Background()); err != nil || got != "one-use-param" {
		t.Fatalf("Take from second replica = %q, %v; want one-use-param", got, err)
	}
	if _, _, err := first.Take(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second Take error = %v, want not-exist", err)
	}

	if err := first.Set(context.Background(), "expiring-param", time.Now()); err != nil {
		t.Fatalf("Set expiring: %v", err)
	}
	mini.FastForward(captchaTTL + time.Second)
	if _, _, err := second.Get(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get after TTL error = %v, want not-exist", err)
	}
}
