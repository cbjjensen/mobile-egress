package tailscale

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

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
