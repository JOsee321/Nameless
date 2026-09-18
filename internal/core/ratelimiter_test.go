package core_test

import (
	"context"
	"testing"
	"time"

	"nameless/internal/core"
)

func TestRateLimiterIsolatesDomains(t *testing.T) {
	// rps=1000 so waits are effectively instant; we only check isolation.
	rl := core.NewRateLimiter(1000)
	ctx := context.Background()

	// Both domains should be granted immediately.
	if err := rl.Wait(ctx, "https://example.com/page"); err != nil {
		t.Fatalf("example.com wait: %v", err)
	}
	if err := rl.Wait(ctx, "https://other.com/page"); err != nil {
		t.Fatalf("other.com wait: %v", err)
	}
}

func TestRateLimiterHonoursBurst(t *testing.T) {
	// rps=2 — burst also 2, so first two calls are immediate, third must wait.
	rl := core.NewRateLimiter(2)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := rl.Wait(ctx, "https://slow.com/"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// First two calls should complete well under 100 ms (burst).
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("burst calls too slow: %v", elapsed)
	}
}

func TestRateLimiterContextCancellation(t *testing.T) {
	// rps=1 with burst=1 — first call consumed, second must wait ~1 s.
	rl := core.NewRateLimiter(1)
	ctx := context.Background()

	// Consume the single token.
	if err := rl.Wait(ctx, "https://throttled.com/"); err != nil {
		t.Fatal(err)
	}

	// Cancel immediately and verify Wait returns the context error.
	cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()

	err := rl.Wait(cancelCtx, "https://throttled.com/")
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}
