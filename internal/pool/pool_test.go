package pool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMapKeepsInputOrder(t *testing.T) {
	in := []int{1, 2, 3, 4, 5, 6, 7, 8}
	got := Map(context.Background(), 4, in, func(_ context.Context, n int) (int, error) {
		// later items finish first
		time.Sleep(time.Duration(len(in)-n) * time.Millisecond)
		return n * 10, nil
	})
	if len(got) != len(in) {
		t.Fatalf("got %d results, want %d", len(got), len(in))
	}
	for i, r := range got {
		if r.Err != nil || r.Value != in[i]*10 {
			t.Errorf("result %d = %+v, want value %d", i, r, in[i]*10)
		}
	}
}

func TestMapBoundsConcurrency(t *testing.T) {
	var running, peak atomic.Int32
	in := make([]int, 20)
	Map(context.Background(), 3, in, func(_ context.Context, _ int) (struct{}, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return struct{}{}, nil
	})
	if p := peak.Load(); p > 3 {
		t.Errorf("peak concurrency %d, want at most 3", p)
	}
	if p := peak.Load(); p < 2 {
		t.Errorf("peak concurrency %d, expected the workers to overlap", p)
	}
}

func TestMapDefaultsWorkersWhenNotPositive(t *testing.T) {
	got := Map(context.Background(), 0, []int{1, 2, 3}, func(_ context.Context, n int) (int, error) {
		return n, nil
	})
	if len(got) != 3 || got[2].Value != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestMapOneFailureDoesNotStopTheRest(t *testing.T) {
	boom := errors.New("boom")
	got := Map(context.Background(), 2, []int{1, 2, 3, 4}, func(_ context.Context, n int) (int, error) {
		if n == 2 {
			return 0, boom
		}
		return n, nil
	})
	for i, r := range got {
		if i == 1 {
			if !errors.Is(r.Err, boom) {
				t.Errorf("item 1 err = %v, want boom", r.Err)
			}
			continue
		}
		if r.Err != nil || r.Value != i+1 {
			t.Errorf("item %d = %+v, want value %d and no error", i, r, i+1)
		}
	}
}

func TestMapCancelledContextMarksUnstartedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	got := Map(ctx, 1, make([]int, 10), func(ctx context.Context, _ int) (int, error) {
		if started.Add(1) == 2 {
			cancel()
		}
		return 0, nil
	})
	var cancelled int
	for _, r := range got {
		if errors.Is(r.Err, context.Canceled) {
			cancelled++
		}
	}
	if cancelled == 0 {
		t.Error("expected unstarted items to report context.Canceled")
	}
	if int(started.Load()) >= len(got) {
		t.Errorf("all %d items started despite cancellation", started.Load())
	}
}

func TestMapEmptyInput(t *testing.T) {
	got := Map(context.Background(), 4, []int{}, func(_ context.Context, n int) (int, error) { return n, nil })
	if len(got) != 0 {
		t.Errorf("got %d results for empty input", len(got))
	}
}
