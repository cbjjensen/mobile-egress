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

func TestFindFunnelApprovalURLAcceptsOnlyTheOfficialFunnelEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "official URL",
			output: "Funnel is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/funnel?node=niiy8GTvVs11CNTRL\n",
			want:   "https://login.tailscale.com/f/funnel?node=niiy8GTvVs11CNTRL",
		},
		{
			name:   "lookalike host",
			output: "https://login.tailscale.com.evil.example/f/funnel?node=niiy8GTvVs11CNTRL",
		},
		{
			name:   "wrong path",
			output: "https://login.tailscale.com/admin?node=niiy8GTvVs11CNTRL",
		},
		{
			name:   "missing node",
			output: "https://login.tailscale.com/f/funnel",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := findFunnelApprovalURL([]byte(test.output)); got != test.want {
				t.Fatalf("findFunnelApprovalURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConnectUsesLoginAndPlatformSetupWithoutConfiguringFunnel(t *testing.T) {
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
		append([]string(nil), testPlatformUpArguments...),
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

func TestConnectReturnsThePlatformSetupFailure(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "tailscale")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		outputs: [][]byte{
			[]byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`),
			[]byte(`{}`),
			nil,
		},
		errors: []error{nil, nil, errors.New("private up failure")},
	}
	_, err := NewController(executable, runner).Connect(context.Background())
	if err == nil || err.Error() != testPlatformUpFailure {
		t.Fatalf("Connect() error = %v, want %q", err, testPlatformUpFailure)
	}
	if got := runner.arguments[len(runner.arguments)-1]; !reflect.DeepEqual(got, testPlatformUpArguments) {
		t.Fatalf("up arguments = %#v, want %#v", got, testPlatformUpArguments)
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
		append([]string(nil), testPlatformUpArguments...),
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
		append([]string(nil), testPlatformUpArguments...),
		{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
	}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("CLI calls = %#v, want %#v", runner.arguments, want)
	}
}

func TestEnableOpensFunnelApprovalBeforeTheCLICommandCompletes(t *testing.T) {
	t.Parallel()

	runner := &approvalStreamingRunner{release: make(chan struct{})}
	controller := NewController(`C:\Program Files\Tailscale\tailscale.exe`, runner)
	var openedURL string
	controller.SetFunnelApprovalHandler(func(approvalURL string) {
		openedURL = approvalURL
		close(runner.release)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status, err := controller.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := openedURL, "https://login.tailscale.com/f/funnel?node=niiy8GTvVs11CNTRL"; got != want {
		t.Fatalf("opened URL = %q, want %q", got, want)
	}
	if !runner.streamed {
		t.Fatal("Enable() used the non-streaming runner for Funnel approval")
	}
	if !status.FunnelReady {
		t.Fatal("Enable() did not verify Funnel after approval")
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

type approvalStreamingRunner struct {
	configured bool
	release    chan struct{}
	streamed   bool
}

func (runner *approvalStreamingRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	if reflect.DeepEqual(arguments, []string{"status", "--json"}) {
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`), nil
	}
	if reflect.DeepEqual(arguments, []string{"funnel", "status", "--json"}) {
		if runner.configured {
			return []byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`), nil
		}
		return []byte(`{}`), nil
	}
	if reflect.DeepEqual(arguments, testPlatformUpArguments) {
		return nil, nil
	}
	return nil, errors.New("unexpected non-streaming command")
}

func (runner *approvalStreamingRunner) RunStreaming(ctx context.Context, _ string, observe func([]byte), arguments ...string) ([]byte, error) {
	if !reflect.DeepEqual(arguments, []string{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"}) {
		return nil, errors.New("unexpected streaming command")
	}
	runner.streamed = true
	observe([]byte("Funnel is not enabled on your tailnet.\nTo enable, visit:\nhttps://login.tailscale.com/f/fun"))
	observe([]byte("nel?node=niiy8GTvVs11CNTRL\n"))
	select {
	case <-runner.release:
		runner.configured = true
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
