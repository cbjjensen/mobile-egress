package main

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestRelayWindowsServiceReportsRunningAndStopsItsServeContext(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	stopped := make(chan struct{})
	handler := relayWindowsService{serve: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	}}
	requests := make(chan svc.ChangeRequest)
	statuses := make(chan svc.Status, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.Execute(nil, requests, statuses)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Windows service did not start relay serve path")
	}
	var running bool
	for !running {
		select {
		case status := <-statuses:
			running = status.State == svc.Running && status.Accepts&svc.AcceptStop != 0 && status.Accepts&svc.AcceptShutdown != 0
		case <-time.After(2 * time.Second):
			t.Fatal("Windows service did not report Running with stop/shutdown acceptance")
		}
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Windows service Stop did not cancel relay serve context")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Windows service handler did not exit")
	}
}
