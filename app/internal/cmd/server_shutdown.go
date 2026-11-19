package cmd

import (
	"context"
	"fmt"
	"time"

	log "git.thebase.vn/base_hub/internal/services/log"
)

// shutdownGrace is how long a step still gets once the shared budget is spent,
// so a stuck component cannot stop the steps registered before it from running.
const shutdownGrace = 2 * time.Second

// serverShutdownTimeout is the single budget shared by every registered stop
// step (the HTTP listener keeps its own shorter drain inside serveFiberApp).
const serverShutdownTimeout = 30 * time.Second

// shutdownStack runs registered stop steps in reverse registration order, the
// order the old chain of defers had, but under one deadline and with every
// step named in the log. A step that panics or overruns the deadline is
// reported and skipped so one stuck component cannot hold the process (or the
// DB pool close behind it) forever.
type shutdownStack struct {
	steps []shutdownStep
}

type shutdownStep struct {
	name string
	stop func(ctx context.Context) error
}

// Add registers a stop step. Register a component right after it starts.
func (s *shutdownStack) Add(name string, stop func(ctx context.Context) error) {
	s.steps = append(s.steps, shutdownStep{name: name, stop: stop})
}

// AddFunc registers a stop function that takes no context or returns no error.
func (s *shutdownStack) AddFunc(name string, stop func()) {
	s.Add(name, func(context.Context) error { stop(); return nil })
}

// Run stops every step, newest first, and returns the failures joined by name.
func (s *shutdownStack) Run(parent context.Context, timeout time.Duration) []error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var failures []error
	for i := len(s.steps) - 1; i >= 0; i-- {
		step := s.steps[i]
		done := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- fmt.Errorf("panic: %v", r)
				}
			}()
			done <- step.stop(ctx)
		}()
		var wait <-chan struct{} = ctx.Done()
		if ctx.Err() != nil {
			wait = graceChan()
		}
		select {
		case err := <-done:
			if err != nil {
				failures = append(failures, fmt.Errorf("shutdown %s: %w", step.name, err))
				log.Errorf("shutdown %s: %v", step.name, err)
			}
		case <-wait:
			failures = append(failures, fmt.Errorf("shutdown %s: %w", step.name, context.DeadlineExceeded))
			log.Errorf("shutdown %s: abandoned after the shutdown budget", step.name)
		}
	}
	s.steps = nil
	return failures
}

func graceChan() <-chan struct{} {
	ch := make(chan struct{})
	time.AfterFunc(shutdownGrace, func() { close(ch) })
	return ch
}
