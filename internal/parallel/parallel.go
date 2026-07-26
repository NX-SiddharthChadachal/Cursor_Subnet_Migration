// Package parallel provides the small amount of bounded concurrency the
// prechecks and postchecks need without pulling in an external dependency.
package parallel

import (
	"context"
	"sync"
)

// ForEach runs fn over every item with at most limit goroutines in flight. It
// returns the first non-nil error, after every started goroutine has finished.
// A cancelled context stops new work from starting.
func ForEach[T any](ctx context.Context, limit int, items []T, fn func(context.Context, T) error) error {
	if limit < 1 {
		limit = 1
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, limit)
	)

	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			// Fall through to the wait below; the remaining items are skipped.
			goto done
		}

		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, item); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(item)
	}

done:
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// Group runs a fixed set of independent tasks concurrently and returns the first
// error. Unlike ForEach it is unbounded, so it is used for the handful of
// inventory fetches that open a precheck run.
func Group(tasks ...func() error) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for _, task := range tasks {
		wg.Add(1)
		go func(task func() error) {
			defer wg.Done()
			if err := task(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(task)
	}
	wg.Wait()
	return firstErr
}
