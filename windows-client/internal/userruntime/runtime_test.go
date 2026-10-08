package userruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/securestore"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("runtime did not reach expected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloseCancelsAndJoinsDirectRuntimeAndActiveConnections(t *testing.T) {
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "macos", "arm64", "test")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if err := direct.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", BindAddress: address, Endpoint: "https://client.example:8443", DisplayName: "Mac"}); err != nil {
		t.Fatal(err)
	}
	lifetime := New(context.Background(), direct.Run, 10*time.Millisecond)
	t.Cleanup(lifetime.Close)
	waitFor(t, func() bool { return direct.Status().Running })
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	lifetime.Close()
	if direct.Status().Running {
		t.Fatal("close returned before Direct stopped")
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("active listener connection survived close")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("active listener connection was not closed")
	}
	replacement, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener leaked after close: %v", err)
	}
	replacement.Close()
	lifetime.Close() // Native shutdown and window-close can both invoke Close.
}

func TestRecoveryWaitIsCanceledWithoutStartingAnotherRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	failed := make(chan struct{})
	lifetime := New(ctx, func(context.Context) error {
		if attempts.Add(1) == 1 {
			close(failed)
		}
		return errors.New("port unavailable")
	}, time.Hour)
	<-failed
	cancel()
	done := make(chan struct{})
	go func() { lifetime.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for retry delay")
	}
	if attempts.Load() != 1 {
		t.Fatal("recovery started after cancellation")
	}
}

func TestOccupiedTLSListenerRecoversWithoutChangingSavedAddress(t *testing.T) {
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	address := blocked.Addr().String()
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "macos", "arm64", "test")
	if err := direct.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", BindAddress: address, Endpoint: "https://client.example:8443", DisplayName: "Mac"}); err != nil {
		t.Fatal(err)
	}
	lifetime := New(context.Background(), direct.Run, 10*time.Millisecond)
	defer lifetime.Close()
	waitFor(t, func() bool { return direct.Status().Phase == "error" })
	status := direct.Status()
	if status.Running || status.BindAddress != address || !strings.Contains(status.Message, "port") {
		t.Fatalf("occupied listener was hidden or renumbered: %#v", status)
	}
	blocked.Close()
	waitFor(t, func() bool { return direct.Status().Running })
	if direct.Status().BindAddress != address {
		t.Fatal("recovery changed saved address")
	}
}

func TestOccupiedPhoneProxyRollsBackAndKeepsItsStablePorts(t *testing.T) {
	blocked, err := net.Listen("tcp", net.JoinHostPort(proxyendpoint.Host, "1099"))
	if err != nil {
		t.Skipf("test proxy port unavailable: %v", err)
	}
	defer blocked.Close()
	available, err := net.Listen("tcp", net.JoinHostPort(proxyendpoint.Host, "1098"))
	if err != nil {
		t.Skipf("test proxy port unavailable: %v", err)
	}
	available.Close()
	store := securestore.NewMemoryStore()
	direct := nodeservice.NewDirect(nodeservice.NewRepository(store), "macos", "arm64", "test")
	tlsPort, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsAddress := tlsPort.Addr().String()
	tlsPort.Close()
	if err := direct.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", BindAddress: tlsAddress, Endpoint: "https://client.example:8443", DisplayName: "Mac"}); err != nil {
		t.Fatal(err)
	}
	phone, err := direct.AddPhone(context.Background(), "Phone")
	if err != nil {
		t.Fatal(err)
	}
	// A saved phone can retain slot 9 after earlier phones are removed. Use
	// those stable ports so an installed development Client can keep slot 0.
	raw, err := store.Get(context.Background(), "direct-client-state-v2")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	saved["phones"].([]any)[0].(map[string]any)["slot"] = 9
	raw, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "direct-client-state-v2", raw); err != nil {
		t.Fatal(err)
	}
	direct = nodeservice.NewDirect(nodeservice.NewRepository(store), "macos", "arm64", "test")
	lifetime := New(context.Background(), direct.Run, 10*time.Millisecond)
	defer lifetime.Close()
	waitFor(t, func() bool { return direct.Status().Running })
	phones, err := direct.Phones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := phones.Phones[0]
	if p.ProxyRunning || p.HTTPAddress != net.JoinHostPort(proxyendpoint.Host, "1099") || p.Phase != "proxy_error" || !strings.Contains(p.Message, "1099") {
		t.Fatalf("proxy conflict hidden or renumbered: %#v", p)
	}
	rolledBack, err := net.Listen("tcp", net.JoinHostPort(proxyendpoint.Host, "1098"))
	if err != nil {
		t.Fatalf("partial proxy pair leaked: %v", err)
	}
	rolledBack.Close()
	blocked.Close()
	if err := direct.RetryPhoneProxy(context.Background(), phone.PhoneID); err != nil {
		t.Fatal(err)
	}
	phones, err = direct.Phones(context.Background())
	if err != nil || !phones.Phones[0].ProxyRunning || phones.Phones[0].HTTPAddress != net.JoinHostPort(proxyendpoint.Host, "1099") {
		t.Fatalf("stable proxy retry failed: %#v %v", phones, err)
	}
	active := make([]net.Conn, 0, 2)
	for _, address := range []string{net.JoinHostPort(proxyendpoint.Host, "1098"), net.JoinHostPort(proxyendpoint.Host, "1099")} {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		active = append(active, connection)
		defer connection.Close()
	}
	lifetime.Close()
	for _, connection := range active {
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := connection.Read(make([]byte, 1)); err == nil {
			t.Fatal("active proxy connection survived app close")
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("active proxy connection was not canceled")
		}
	}
	for _, address := range []string{net.JoinHostPort(proxyendpoint.Host, "1098"), net.JoinHostPort(proxyendpoint.Host, "1099")} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("proxy leaked after close: %v", err)
		}
		listener.Close()
	}
}
