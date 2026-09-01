//go:build darwin

package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"os/user"

	"golang.org/x/sys/unix"

	"mobile-egress/relay/internal/adminservice"
	"mobile-egress/relay/internal/service"
)

func runPlatformDaemon() (adminservice.RunResult, error) {
	return executeDaemon(darwinDaemonDependencies())
}

func darwinDaemonDependencies() daemonDependencies {
	return daemonDependencies{
		effectiveUID:  os.Geteuid,
		setUmask:      unix.Umask,
		lookupGroup:   user.LookupGroup,
		notifyContext: signal.NotifyContext,
		openSocket: func(ctx context.Context, gid uint32) (daemonSocket, error) {
			return adminservice.OpenDarwinAdminSocket(ctx, gid)
		},
		newPathGuard: adminservice.NewDarwinStatePathGuard,
		newRuntime: func(config adminservice.RuntimeConfig) (daemonRuntime, error) {
			return adminservice.NewRuntime(config)
		},
		peer:        adminservice.DarwinPeer,
		listenRelay: net.Listen,
		openRelay: func(path string) (adminservice.RelayInstance, error) {
			return service.Open(path)
		},
	}
}
