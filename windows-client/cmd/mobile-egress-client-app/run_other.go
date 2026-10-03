//go:build !windows && !darwin

package main

import "errors"

func runApp() error { return errors.New("Mobile Egress Client supports Windows and macOS") }
