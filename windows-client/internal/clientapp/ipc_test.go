package clientapp

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
)

type testService struct{ paired string }

func (s *testService) Status() nodeservice.StandaloneStatus {
	return nodeservice.StandaloneStatus{Phase: "waiting", Message: "Ready to pair"}
}
func (s *testService) Pair(_ context.Context, value string) error { s.paired = value; return nil }
func (s *testService) Import(context.Context, string) error       { return nil }
func (s *testService) Proxy(_ context.Context, kind string) (string, error) {
	return "127.0.0.2:1081:user:secret", nil
}

func TestIPCExposesOnlySafeStatusAndExplicitCopy(t *testing.T) {
	service := &testService{}
	exchange := func(request string) Response {
		server, client := net.Pipe()
		defer client.Close()
		go serveConnection(context.Background(), server, service)
		_, err := client.Write([]byte(request + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := json.NewDecoder(client).Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := exchange(`{"method":"status"}`)
	raw, _ := json.Marshal(response)
	if response.Status == nil || response.Status.Phase != "waiting" || strings.Contains(string(raw), "secret") {
		t.Fatalf("unsafe status: %s", raw)
	}
	if response := exchange(`{"method":"private-key"}`); response.Error == "" || response.Value != "" {
		t.Fatal("unknown secret request accepted")
	}
	if response := exchange(`{"method":"pair","value":"test-invitation"}`); response.Error != "" || service.paired != "test-invitation" {
		t.Fatal("pair was not delivered")
	}
	if response := exchange(`{"method":"proxy","value":"http"}`); response.Value != "127.0.0.2:1081:user:secret" {
		t.Fatal("explicit proxy copy failed")
	}
	if response := exchange(`{"method":"status","extra":"secret"}`); response.Error == "" {
		t.Fatal("unknown IPC field accepted")
	}
}

func TestAppCopyKeepsCredentialsOutOfBindingResponse(t *testing.T) {
	var copied string
	app := New(&testService{}, func(value string) error { copied = value; return nil })
	if err := app.CopyProxy("http"); err != nil {
		t.Fatal(err)
	}
	if copied != "127.0.0.2:1081:user:secret" {
		t.Fatal("proxy not copied")
	}
	if err := app.CopyProxy("private-key"); err == nil {
		t.Fatal("unknown copy kind accepted")
	}
}
