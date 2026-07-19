package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubmitDetachesResultWaitAfterEnqueue(t *testing.T) {
	s := &Supervisor{actions: make(chan Request, 1), submitResultTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Submit(ctx, ActionStop, false) }()
	req := <-s.actions
	cancel()
	req.Reply <- nil
	if err := <-done; err != nil {
		t.Fatalf("queued operation inherited request cancellation: %v", err)
	}
}

func TestSubmitResultWaitHasInternalBound(t *testing.T) {
	s := &Supervisor{actions: make(chan Request, 1), submitResultTimeout: 10 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- s.Submit(context.Background(), ActionRestart, false) }()
	<-s.actions
	err := <-done
	var resultErr *OperationResultError
	if !errors.As(err, &resultErr) {
		t.Fatalf("error=%v, want OperationResultError", err)
	}
}

func TestSubmitCancellationBeforeEnqueueIsPreserved(t *testing.T) {
	s := &Supervisor{actions: make(chan Request)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Submit(ctx, ActionStop, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context cancellation", err)
	}
}
