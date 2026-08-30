//go:build windows

package main

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sys/windows/svc"
)

const relayWindowsServiceName = "MobileEgressRelay"

type relayWindowsService struct {
	serve func(context.Context) error
}

func (service relayWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 1)
	go func() {
		errors <- service.serve(ctx)
	}()
	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	statuses <- running
	for {
		select {
		case err := <-errors:
			if err != nil {
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- running
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-errors; err != nil {
					return false, 1
				}
				return false, 0
			}
		}
	}
}

func runAsWindowsServiceIfNeeded(stateDir, listenAddress string, stderr io.Writer) (bool, int) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintln(stderr, "relay serve: detect Windows service:", err)
		return true, 1
	}
	if !isService {
		return false, 0
	}
	handler := relayWindowsService{serve: func(ctx context.Context) error {
		return serveRelay(ctx, stateDir, listenAddress)
	}}
	if err := svc.Run(relayWindowsServiceName, handler); err != nil {
		fmt.Fprintln(stderr, "relay serve: Windows service:", err)
		return true, 1
	}
	return true, 0
}
