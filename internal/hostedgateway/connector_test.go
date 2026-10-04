package hostedgateway

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRejectInvalidProtectedConfiguration(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("accepted missing activation")
	}
}

func TestCloseUnblocksListenerAndRun(t *testing.T) {
	c, err := New(Config{Address: "127.0.0.1:1", ServerName: "broker.example.test", DeviceID: "device", ClientID: "client", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background()) }()
	c.Close()
	if _, err := c.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run leaked")
	}
}
