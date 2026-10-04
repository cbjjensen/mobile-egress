package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compile the real entry point without Wails so a failed privilege check can
// prove that no GUI startup was attempted, on either development platform.
func TestClientGUIRejectsPrivilegesBeforeStartingWails(t *testing.T) {
	root := t.TempDir()
	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainPath, mainSource, 0o600); err != nil {
		t.Fatal(err)
	}
	stubPath := filepath.Join(root, "fixture.go")
	stub := `package main
import ("errors"; "fmt"; "os")
func checkGUIPrivileges() error {
    if os.Getenv("FIXTURE_REJECT_GUI") == "1" { return errors.New("fixture root rejection") }
    return nil
}
func runApp() error { fmt.Println("fixture GUI started"); return nil }
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "client-fixture.exe")
	if output, err := exec.Command("go", "build", "-o", binary, mainPath, stubPath).CombinedOutput(); err != nil {
		t.Fatalf("entry fixture did not build: %v\n%s", err, output)
	}
	for _, reject := range []bool{true, false} {
		command := exec.Command(binary)
		flag := "0"
		if reject {
			flag = "1"
		}
		command.Env = append(os.Environ(), "FIXTURE_REJECT_GUI="+flag)
		output, err := command.CombinedOutput()
		if reject {
			if err == nil || strings.Contains(string(output), "GUI started") || !strings.Contains(string(output), "root rejection") {
				t.Fatalf("rejected identity reached the GUI: %v\n%s", err, output)
			}
		} else if err != nil || !strings.Contains(string(output), "GUI started") {
			t.Fatalf("ordinary user could not start GUI: %v\n%s", err, output)
		}
	}
}
