// Package pool runs a function over a list with bounded concurrency.
package pool

import (
	"context"
	"sync"
)

const DefaultWorkers = 4

type Result[T any] struct {
	Value T
	Err   error
}

// Map returns results in input order. A failure only affects its own item;
// once ctx is done, unstarted items report its error.
func Map[In, Out any](ctx context.Context, workers int, in []In, fn func(context.Context, In) (Out, error)) []Result[Out] {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	out := make([]Result[Out], len(in))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, item := range in {
		if err := ctx.Err(); err != nil {
			out[i].Err = err
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i].Err = ctx.Err()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].Value, out[i].Err = fn(ctx, item)
		}()
	}
	wg.Wait()
	return out
}
