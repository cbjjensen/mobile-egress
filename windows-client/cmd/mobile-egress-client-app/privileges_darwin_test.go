//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestMacClientGUIRejectsRoot(t *testing.T) {
	if err := checkGUIUser(0); err == nil || !strings.Contains(err.Error(), "Applications") {
		t.Fatalf("root received no rejection with a recovery action: %v", err)
	}
	for _, uid := range []int{501, 502} {
		if err := checkGUIUser(uid); err != nil {
			t.Fatalf("ordinary UID %d was rejected: %v", uid, err)
		}
	}
}
