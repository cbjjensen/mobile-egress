//go:build (windows || darwin) && !client_usermode

package main

import (
	"context"
	"mobile-egress/windows-client/internal/clientapp"
)

func prepareAppSession(context.Context) (appSession, error) {
	return appSession{service: clientapp.LocalClient{}, close: func() {}}, nil
}
