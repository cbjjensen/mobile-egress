//go:build windows || darwin

package main

import (
	"mobile-egress/windows-client/internal/clientapp"
)

type appSession struct {
	service clientapp.Service
	close   func()
}
