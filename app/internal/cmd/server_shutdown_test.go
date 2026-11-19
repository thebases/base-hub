package cmd

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestShutdownStackRunsNewestFirstAndSurvivesFailures(t *testing.T) {
	var order []string
	var stack shutdownStack
	stack.AddFunc("db", func() { order = append(order, "db") })
	stack.Add("worker", func(context.Context) error { order = append(order, "worker"); return errors.New("boom") })
	stack.AddFunc("panicky", func() { panic("x") })
	stack.AddFunc("kafka", func() { order = append(order, "kafka") })

	failures := stack.Run(context.Background(), time.Second)
	if want := []string{"kafka", "worker", "db"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %v, want worker error and panic", failures)
	}
}

func TestShutdownStackAbandonsStepPastDeadline(t *testing.T) {
	var stack shutdownStack
	ran := false
	stack.AddFunc("later", func() { ran = true })
	release := make(chan struct{})
	defer close(release)
	stack.Add("stuck", func(context.Context) error { <-release; return nil })

	failures := stack.Run(context.Background(), 50*time.Millisecond)
	if len(failures) != 1 || !errors.Is(failures[0], context.DeadlineExceeded) {
		t.Fatalf("failures = %v", failures)
	}
	if !ran {
		t.Fatal("a stuck step must not stop the steps registered before it")
	}
}
