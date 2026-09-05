package httpconnect

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func poolServer(t *testing.T) (*Server, *fakeOpener, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{}, 64)
	opener := &fakeOpener{healthy: true}
	opener.onOpen = func(conn net.Conn) {
		go func() {
			defer conn.Close()
			defer func() { closed <- struct{}{} }()
			reader := bufio.NewReader(conn)
			for {
				request, err := http.ReadRequest(reader)
				if err != nil {
					return
				}
				_ = request.Body.Close()
				if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
					return
				}
			}
		}()
	}
	return startTestServer(t, opener, 30*time.Second), opener, closed
}

func poolRequest(t *testing.T, server *Server, host string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+host+"/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func consumePoolResponse(t *testing.T, response *http.Response) {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatalf("body=%q error=%v", body, err)
	}
}

func openedPoolStreams(opener *fakeOpener) int {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	return len(opener.remoteConns)
}

func TestPlainHTTPReusesSixteenDestinationsAndEvictsOldest(t *testing.T) {
	server, opener, closed := poolServer(t)
	for pass := 0; pass < 2; pass++ {
		for host := 0; host < 16; host++ {
			consumePoolResponse(t, poolRequest(t, server, fmt.Sprintf("host-%d.test", host)))
		}
	}
	if opened := openedPoolStreams(opener); opened != 16 {
		t.Fatalf("two passes opened %d streams, want 16", opened)
	}
	consumePoolResponse(t, poolRequest(t, server, "host-16.test"))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("seventeenth destination did not evict a stream")
	}
	consumePoolResponse(t, poolRequest(t, server, "host-15.test"))
	if opened := openedPoolStreams(opener); opened != 17 {
		t.Fatalf("recent destination was evicted: %d opens", opened)
	}
	consumePoolResponse(t, poolRequest(t, server, "host-0.test"))
	if opened := openedPoolStreams(opener); opened != 18 {
		t.Fatalf("oldest destination was retained: %d opens", opened)
	}
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	// All opened streams, including evictions, must close on shutdown.
	for n := 1; n < 18; n++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("shutdown left streams open after %d closes", n)
		}
	}
}

func TestPlainHTTPRetainsFourIdleStreamsPerDestination(t *testing.T) {
	server, opener, closed := poolServer(t)
	responses := make([]*http.Response, 0, 5)
	for n := 0; n < 5; n++ {
		responses = append(responses, poolRequest(t, server, "same.test"))
	}
	for _, response := range responses {
		consumePoolResponse(t, response)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("fifth idle stream was not evicted")
	}
	responses = nil
	for n := 0; n < 4; n++ {
		responses = append(responses, poolRequest(t, server, "same.test"))
	}
	if opened := openedPoolStreams(opener); opened != 5 {
		t.Fatalf("four simultaneous requests failed to reuse four idle streams: %d opens", opened)
	}
	for _, response := range responses {
		consumePoolResponse(t, response)
	}
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	for n := 1; n < 5; n++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("shutdown left streams open after %d closes", n)
		}
	}
}

func TestPlainHTTPIdleStreamsExpireAfterSixtySeconds(t *testing.T) {
	// The listener stays outside the virtual-time bubble; relay streams and
	// transport timers are created by RoundTrip inside it.
	server, opener, closed := poolServer(t)
	synctest.Test(t, func(t *testing.T) {
		defer server.transport.CloseIdleConnections()
		consumePoolResponse(t, poolRequest(t, server, "expiry.test"))
		time.Sleep(59 * time.Second)
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("idle stream expired before sixty seconds")
		default:
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Fatal("idle stream survived beyond sixty seconds")
		}
		consumePoolResponse(t, poolRequest(t, server, "expiry.test"))
		if opened := openedPoolStreams(opener); opened != 2 {
			t.Fatalf("expired stream reused: %d opens", opened)
		}
	})
}
