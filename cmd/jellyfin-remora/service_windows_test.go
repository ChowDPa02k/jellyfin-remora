//go:build windows

package main

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestWindowsServiceStopCancelsDaemon(t *testing.T) {
	started := make(chan struct{})
	handler := &serviceHandler{run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}}
	requests := make(chan svc.ChangeRequest, 1)
	statuses := make(chan svc.Status, 8)
	done := make(chan uint32, 1)
	go func() {
		_, code := handler.Execute(nil, requests, statuses)
		done <- code
	}()
	<-started
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service handler did not stop")
	}
}
