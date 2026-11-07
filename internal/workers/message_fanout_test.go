package workers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMessageFanoutQuantumDrainsBurstAndYields(t *testing.T) {
	fixed := time.Now()
	now := func() time.Time { return fixed }
	calls := 0
	if err := drainMessageFanoutQuantum(context.Background(), func(context.Context) (bool, error) { calls++; return calls <= 20, nil }, now); err != nil || calls != 21 {
		t.Fatalf("ordinary twenty-job burst throttled: calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := drainMessageFanoutQuantum(context.Background(), func(context.Context) (bool, error) { calls++; return true, nil }, now); err != nil || calls != 32 {
		t.Fatalf("unbounded backlog did not yield: calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := drainMessageFanoutQuantum(context.Background(), func(context.Context) (bool, error) {
		calls++
		fixed = fixed.Add(150 * time.Millisecond)
		return true, nil
	}, now); err != nil || calls != 1 {
		t.Fatalf("slow page ignored elapsed budget: calls=%d err=%v", calls, err)
	}
}
func TestMessageFanoutQuantumCancellationAndFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if err := drainMessageFanoutQuantum(ctx, func(context.Context) (bool, error) { calls++; return true, nil }, time.Now); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled consumer started page: %d %v", calls, err)
	}
	fault := errors.New("failed page")
	if err := drainMessageFanoutQuantum(context.Background(), func(context.Context) (bool, error) { calls++; return true, fault }, time.Now); !errors.Is(err, fault) || calls != 1 {
		t.Fatalf("failed page spun: %d %v", calls, err)
	}
}
