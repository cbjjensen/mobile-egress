//go:build windows && client_usermode

package main

import (
	"context"
	"errors"
)

func prepareAppSession(context.Context) (appSession, error) {
	return appSession{}, errors.New("The app runtime build supports macOS only.")
}
