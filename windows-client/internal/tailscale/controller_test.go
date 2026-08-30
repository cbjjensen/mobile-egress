package tailscale

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestControllerInstalledRequiresARegularExecutable(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	executable := filepath.Join(directory, "tailscale.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NewController(executable, &fakeRunner{}).Installed() {
		t.Fatal("Installed() = false for an existing executable")
	}
	if NewController(filepath.Join(directory, "missing.exe"), &fakeRunner{}).Installed() {
		t.Fatal("Installed() = true for a missing executable")
	}
	if NewController(directory, &fakeRunner{}).Installed() {
		t.Fatal("Installed() = true for a directory")
	}
}

func TestConnectUsesLoginAndUnattendedSetupWithoutConfiguringFunnel(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "tailscale.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		outputs: [][]byte{
			nil,
			nil,
			nil,
			[]byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`),
			[]byte(`{}`),
		},
		errors: []error{errors.New("offline"), nil, nil, nil, nil},
	}
	controller := NewController(executable, runner)
	status, err := controller.Connect(context.Background())
	if err != nil || !status.Online {
		t.Fatalf("Connect() = %#v/%v, want online", status, err)
	}
	want := [][]string{
		{"status", "--json"},
		{"login"},
		{"up", "--unattended=true"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
	}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.arguments, want)
	}
	for _, arguments := range runner.arguments {
		if len(arguments) > 0 && arguments[0] == "funnel" && (len(arguments) < 2 || arguments[1] != "status") {
			t.Fatalf("Connect() configured Funnel with arguments %#v", arguments)
		}
	}
}

func TestControllerStatusAndEnableUseExactCLICommands(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{
		[]byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`),
		[]byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`),
		[]byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`),
		[]byte(`{}`),
		nil,
		nil,
		[]byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`),
		[]byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`),
	}}
	controller := NewController(`C:\Program Files\Tailscale\tailscale.exe`, runner)
	status, err := controller.Status(context.Background())
	if err != nil || status.FQDN != "bridge.tail123.ts.net" || !status.FunnelReady {
		t.Fatalf("Status() = %#v/%v", status, err)
	}
	status, err = controller.Enable(context.Background())
	if err != nil || status.PublicURL != "https://bridge.tail123.ts.net:8443" {
		t.Fatalf("Enable() = %#v/%v", status, err)
	}
	want := [][]string{
		{"status", "--json"},
		{"funnel", "status", "--json"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
		{"up", "--unattended=true"},
		{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
	}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.arguments, want)
	}
}

func TestEnableStartsInteractiveBrowserLoginWhenStatusIsOffline(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		outputs: [][]byte{nil, nil, nil, nil, []byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`), []byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`)},
		errors:  []error{errors.New("offline"), nil, nil, nil, nil, nil},
	}
	controller := NewController(`C:\Program Files\Tailscale\tailscale.exe`, runner)
	if _, err := controller.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"status", "--json"},
		{"login"},
		{"up", "--unattended=true"},
		{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
	}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.arguments, want)
	}
}

type fakeRunner struct {
	outputs   [][]byte
	errors    []error
	arguments [][]string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	runner.arguments = append(runner.arguments, append([]string(nil), arguments...))
	output := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	var err error
	if len(runner.errors) > 0 {
		err = runner.errors[0]
		runner.errors = runner.errors[1:]
	}
	return output, err
}
