//go:build windows

package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseModeAcceptsOnlyFixedInternalOperationAndNonce(t *testing.T) {
	mode, nonce, err := parseMode(nil)
	if err != nil || mode != parentMode || nonce != "" {
		t.Fatalf("parent parse = %q, %q, %v", mode, nonce, err)
	}
	validNonce := strings.Repeat("a", 64)
	mode, nonce, err = parseMode([]string{"--internal-elevated-install", validNonce})
	if err != nil || mode != elevatedInstallMode || nonce != validNonce {
		t.Fatalf("child parse = %q, %q, %v", mode, nonce, err)
	}

	for _, arguments := range [][]string{
		{"--internal-elevated-install", validNonce, "--destination", `C:\elsewhere`},
		{"--internal-elevated-uninstall", validNonce},
		{"--internal-elevated-install", "short"},
	} {
		if _, _, err := parseMode(arguments); err == nil {
			t.Fatalf("accepted arguments %#v", arguments)
		}
	}
}

func TestFailureResultIsRedacted(t *testing.T) {
	result := failureResult(strings.Repeat("b", 64), errors.New(`copy C:\Users\Friend\secret: access denied`))
	if result.Success || result.Code != "install_failed" || result.Message != "Installation did not complete. Verify the Mobile Egress publisher trust before retrying." {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Message, "Friend") || strings.Contains(result.Message, "access denied") || strings.Contains(result.Message, "left behind") {
		t.Fatal("result exposed internal error details")
	}
	if result.Nonce != strings.Repeat("b", 64) {
		t.Fatal("result nonce changed")
	}
}
