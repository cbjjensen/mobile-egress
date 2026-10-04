package hostedgateway

import (
	"context"
	"crypto/tls"
	yamux "github.com/libp2p/go-yamux/v5"
	"io"
	"net"
	"testing"
	"time"
)

func TestLostSessionReconnectsWithUnacceptedStream(t *testing.T) {
	cert, roots := certificate(t)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connected := make(chan struct{}, 2)
	go func() {
		for i := 0; i < 2; i++ {
			raw, e := l.Accept()
			if e != nil {
				return
			}
			tc := tls.Server(raw, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{ALPN}})
			tc.SetDeadline(time.Now().Add(2 * time.Second))
			if tc.Handshake() != nil {
				return
			}
			var a admission
			if readFrame(tc, &a) != nil || writeFrame(tc, accepted{1, true}) != nil {
				return
			}
			tc.SetDeadline(time.Time{})
			yc := yamux.DefaultConfig()
			yc.LogOutput = io.Discard
			s, e := yamux.Client(tc, yc, nil)
			if e != nil {
				return
			}
			if i == 0 {
				stream, e := s.Open(ctx)
				if e == nil {
					stream.Write([]byte("unaccepted"))
				}
				time.Sleep(20 * time.Millisecond)
				s.Close()
			}
			connected <- struct{}{}
			if i == 1 {
				<-ctx.Done()
				s.Close()
			}
		}
	}()
	c, e := New(Config{Address: l.Addr().String(), ServerName: "broker.test", DeviceID: "d", ClientID: "c", Token: "t", TLSConfig: &tls.Config{RootCAs: roots}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	go c.Run(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-connected:
		case <-ctx.Done():
			t.Fatal("blocked queued stream prevented reconnect")
		}
	}
}
