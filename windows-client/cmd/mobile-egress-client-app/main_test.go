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
const runtimeMode = "service"
func checkGUIPrivileges() error {
    if os.Getenv("FIXTURE_REJECT_GUI") == "1" { return errors.New("fixture root rejection") }
    return nil
}
func runApp() error { fmt.Println("fixture GUI started"); return nil }
func showStartupError(err error) { fmt.Println("fixture startup error presented:", err) }
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "client-fixture.exe")
	if output, err := exec.Command("go", "build", "-o", binary, mainPath, stubPath).CombinedOutput(); err != nil {
		t.Fatalf("entry fixture did not build: %v\n%s", err, output)
	}
	modeCommand := exec.Command(binary, "--runtime-mode")
	modeCommand.Env = append(os.Environ(), "FIXTURE_REJECT_GUI=1")
	if output, err := modeCommand.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "service" {
		t.Fatalf("runtime-mode query reached GUI or privilege check: %v\n%s", err, output)
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
			if err == nil || strings.Contains(string(output), "GUI started") || !strings.Contains(string(output), "root rejection") || !strings.Contains(string(output), "startup error presented") {
				t.Fatalf("rejected identity reached the GUI: %v\n%s", err, output)
			}
		} else if err != nil || !strings.Contains(string(output), "GUI started") {
			t.Fatalf("ordinary user could not start GUI: %v\n%s", err, output)
		}
	}
	// Use the real compile-time mode declarations, still without Wails, to
	// verify that release inspection cannot be inferred from IPC availability.
	stub = strings.Replace(stub, `const runtimeMode = "service"`, "", 1)
	if err := os.WriteFile(stubPath, []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct{ file, want string }{{"mode_service.go", "service"}, {"mode_user.go", "app"}} {
		source, err := os.ReadFile(mode.file)
		if err != nil {
			t.Fatal(err)
		}
		modePath := filepath.Join(root, "mode.go")
		if err := os.WriteFile(modePath, source, 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("go", "build", "-o", binary, mainPath, stubPath, modePath).CombinedOutput(); err != nil {
			t.Fatalf("mode fixture failed: %v\n%s", err, output)
		}
		command := exec.Command(binary, "--runtime-mode")
		command.Env = append(os.Environ(), "FIXTURE_REJECT_GUI=1")
		if output, err := command.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != mode.want {
			t.Fatalf("%s queried the wrong runtime: %v\n%s", mode.file, err, output)
		}
	}
}
