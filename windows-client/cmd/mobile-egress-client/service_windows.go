//go:build windows

package main

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sys/windows/svc"
	"mobile-egress/windows-client/internal/nodeservice"
)

const clientWindowsServiceName = "MobileEgressClient"

type clientWindowsService struct {
	run func(context.Context) error
}

func (service clientWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 1)
	go func() { errors <- service.run(ctx) }()
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

func runNodeService(repository *nodeservice.Repository, stderr io.Writer) int {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve: detect Windows service:", err)
		return 1
	}
	if !isService {
		if err := runForegroundNodeService(repository); err != nil {
			fmt.Fprintln(stderr, "mobile-egress-client serve:", err)
			return 1
		}
		return 0
	}
	handler := clientWindowsService{run: func(ctx context.Context) error {
		return nodeservice.NewService(repository, nodeservice.DefaultDialer{}).Run(ctx)
	}}
	if err := svc.Run(clientWindowsServiceName, handler); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve: Windows service:", err)
		return 1
	}
	return 0
}
