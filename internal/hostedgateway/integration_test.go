package hostedgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	yamux "github.com/libp2p/go-yamux/v5"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func certificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "broker.test"}, DNSNames: []string{"broker.test", "phone.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, _ := x509.MarshalECPrivateKey(key)
	cpem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, e := tls.X509KeyPair(cpem, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}))
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cpem)
	return cert, roots
}
func TestPinnedTLSListenerAndLostSessionReconnect(t *testing.T) {
	cert, roots := certificate(t)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			raw, e := l.Accept()
			if e != nil {
				completed <- e
				return
			}
			conn := tls.Server(raw, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{ALPN}})
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			if e := conn.Handshake(); e != nil {
				completed <- e
				return
			}
			var a admission
			if e := readFrame(conn, &a); e != nil {
				completed <- e
				return
			}
			if a.ClientID != "client" || a.DeviceID != "device" || a.Token != "med1.token" {
				completed <- errRejected
				return
			}
			if e := writeFrame(conn, accepted{1, true}); e != nil {
				completed <- e
				return
			}
			conn.SetDeadline(time.Time{})
			yc := yamux.DefaultConfig()
			yc.LogOutput = io.Discard
			session, e := yamux.Client(conn, yc, nil)
			if e != nil {
				completed <- e
				return
			}
			stream, e := session.Open(ctx)
			if e != nil {
				completed <- e
				return
			}
			phone := tls.Client(stream, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "phone.test"})
			phone.SetDeadline(time.Now().Add(3 * time.Second))
			if _, e := phone.Write([]byte("hello")); e != nil {
				completed <- e
				return
			}
			b := make([]byte, 5)
			if _, e := io.ReadFull(phone, b); e != nil || string(b) != "world" {
				completed <- io.ErrUnexpectedEOF
				return
			}
			phone.Close()
			session.Close()
			conn.Close()
		}
		completed <- nil
	}()
	c, e := New(Config{Address: l.Addr().String(), ServerName: "broker.test", DeviceID: "device", ClientID: "client", Token: "med1.token", TLSConfig: &tls.Config{RootCAs: roots}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	go c.Run(ctx)
	native := tls.NewListener(c, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
	for i := 0; i < 2; i++ {
		conn, e := native.Accept()
		if e != nil {
			t.Fatal(e)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		b := make([]byte, 5)
		if _, e := io.ReadFull(conn, b); e != nil || string(b) != "hello" {
			t.Fatalf("native read %q %v", b, e)
		}
		if _, e := conn.Write([]byte("world")); e != nil {
			t.Fatal(e)
		}
		conn.Close()
	}
	select {
	case e := <-completed:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("reconnect failed")
	}
}
