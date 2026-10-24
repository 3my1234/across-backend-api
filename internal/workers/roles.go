package workers

import (
	"context"
	"errors"
	"sync"
)

func runSelected(ctx context.Context, roles []string, runners map[string]func(context.Context)) error {
	seen := make(map[string]bool)
	if len(roles) == 0 {
		return errors.New("worker role selection is empty")
	}
	// Validate the entire selection before starting any consumer.
	for _, name := range roles {
		if runners[name] == nil || seen[name] {
			return errors.New("worker role selection is invalid")
		}
		seen[name] = true
	}
	var group sync.WaitGroup
	for _, name := range roles {
		run := runners[name]
		group.Add(1)
		go func() { defer group.Done(); run(ctx) }()
	}
	group.Wait()
	return nil
}
