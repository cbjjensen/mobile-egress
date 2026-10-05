package macosrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientMacPreinstallUsesActiveGUISessionForFreshOwner(t *testing.T) {
	for _, tc := range []struct {
		name, console, gui string
		wantSuccess        bool
	}{
		{"remote GUI with root console device", "501", "yes", true},
		{"no GUI domain", "501", "no", false},
		{"no console user", "empty", "yes", false},
		{"failed lookup with plausible output", "error", "yes", false},
		{"nested UID is not console owner", "nested", "yes", false},
		{"ambiguous duplicate UID", "duplicate", "yes", false},
		{"malformed UID", "malformed", "yes", false},
		{"root user", "0", "yes", false},
		{"system user", "500", "yes", false},
		{"negative UID", "-501", "yes", false},
		{"out of UID range", "4294967296", "yes", false},
		{"numeric overflow", "99999999999999999999999999999999999", "yes", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newClientInstallerScriptFixture(t, "preinstall", false)
			output, err := fixture.run(t, tc.console, tc.gui, "ok")
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("fresh installation success = %v, want %v: %v\n%s", err == nil, tc.wantSuccess, err, output)
			}
			owner, readErr := os.ReadFile(filepath.Join(fixture.state, "owner.uid"))
			if tc.wantSuccess {
				if readErr != nil || string(owner) != "501\n" {
					t.Fatalf("active GUI ownership not persisted: %q %v", owner, readErr)
				}
				if !strings.Contains(fixture.journal(t), "print gui/501\n") {
					t.Fatal("fresh ownership was not checked against a live GUI domain")
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("failed fresh setup wrote ownership: %q %v", owner, readErr)
			}
		})
	}
}

func TestClientMacPreinstallPreservesSavedOwnerWithoutGUI(t *testing.T) {
	for _, console := range []string{"empty", "502", "error"} {
		t.Run(console, func(t *testing.T) {
			t.Parallel()
			fixture := newClientInstallerScriptFixture(t, "preinstall", true)
			output, err := fixture.run(t, console, "no", "ok")
			if err != nil {
				t.Fatalf("repair required an active GUI: %v\n%s", err, output)
			}
			for name, want := range map[string]string{"owner.uid": "501\n", "retained-state": "preserve fixture state"} {
				got, err := os.ReadFile(filepath.Join(fixture.state, name))
				if err != nil || string(got) != want {
					t.Fatalf("repair changed %s: %q %v", name, got, err)
				}
			}
			if strings.Contains(fixture.journal(t), "scutil") || strings.Contains(fixture.journal(t), "gui/") {
				t.Fatal("saved ownership must not depend on the current GUI session")
			}
		})
	}
}
