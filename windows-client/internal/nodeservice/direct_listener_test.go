package nodeservice

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestDirectListenerRejectsExcessUntrustedConnectionsAndRefundsOnClose(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newDirectListener(raw, 1)
	defer listener.Close()
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	first, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	serverFirst := <-accepted
	second, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	if _, err = second.Read(one[:]); err != io.EOF {
		t.Fatalf("excess connection not rejected immediately: %v", err)
	}
	serverFirst.Close()
	serverFirst.Close()
	third, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("connection permit leaked after close")
	}
}
