//go:build windows

package setup

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestParentTokenPolicySupportsNormalServerDesktop(t *testing.T) {
	for _, test := range []struct {
		name           string
		elevationType  uint32
		sessionID      uint32
		serviceAccount bool
		wantAllowed    bool
	}{
		{"unsplit Administrator or standard desktop", 1, 1, false, true},
		{"unsplit RDP Administrator", 1, 7, false, true},
		{"normal filtered administrator", 3, 1, false, true},
		{"explicitly elevated split administrator", 2, 1, false, false},
		{"unknown token type", 99, 1, false, false},
		{"zero token type", 0, 1, false, false},
		{"noninteractive session zero", 1, 0, false, false},
		{"service account in desktop session", 1, 1, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateWindowsParentToken(test.elevationType, test.sessionID, test.serviceAccount)
			if (err == nil) != test.wantAllowed {
				t.Fatalf("allowed=%v, want %v: %v", err == nil, test.wantAllowed, err)
			}
		})
	}
}

func TestParentTokenInspectionFailsClosedForInvalidHandle(t *testing.T) {
	if err := checkWindowsParentToken(windows.Token(0)); err == nil {
		t.Fatal("accepted a token that cannot be inspected")
	}
}
