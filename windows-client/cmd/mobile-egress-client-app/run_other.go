//go:build !windows && !darwin

package main

import "errors"

func runApp() error { return errors.New("Inevitable Mobile Relay supports Windows and macOS") }
