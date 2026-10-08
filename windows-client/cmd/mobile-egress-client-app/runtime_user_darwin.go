//go:build darwin && client_usermode

package main

import (
	"context"
	"errors"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/clientapp"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/userruntime"
)

func prepareAppSession(ctx context.Context) (appSession, error) {
	home, err := userHomeDirectory()
	if err != nil {
		return appSession{}, errors.New("Your home directory is unavailable. Sign in to your Mac account and reopen the app.")
	}
	lock, err := userruntime.Acquire(home)
	if err != nil {
		return appSession{}, err
	}
	if err := userruntime.CheckInstalledService(ctx); err != nil {
		lock.Close()
		return appSession{}, err
	}
	store, err := securestore.NewUserKeychainStore()
	if err != nil {
		lock.Close()
		return appSession{}, errors.New("The app could not open your protected login Keychain. Unlock your login Keychain in Keychain Access, then reopen the signed app from your Applications folder. Your saved activation and phone pairing were kept.")
	}
	direct := nodeservice.NewDirect(nodeservice.NewRepository(store), "macos", runtime.GOARCH, version)
	lifetime := userruntime.New(ctx, direct.Run, 2*time.Second)
	var once sync.Once
	closeSession := func() { once.Do(func() { lifetime.Close(); lock.Close() }) }
	return appSession{service: clientapp.WithUserFirewall(direct), close: closeSession}, nil
}

func userHomeDirectory() (string, error) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", err
	}
	return account.HomeDir, nil
}
