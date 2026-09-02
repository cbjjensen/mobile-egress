package relayclient_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/windows-client/internal/httpconnect"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/socks"
)

type relayEnvelope struct {
	Version  int    `json:"version"`
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Payload  string `json:"payload"`
}

func TestRealSessionShares256SlotsAcrossSOCKSHTTPConnectAndIdleHTTP(t *testing.T) {
	fixture := newListenerRelayFixture(t, false)
	defer fixture.Close()
	session, err := relayclient.DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	socksServer := socks.NewServer(socks.Config{Username: "user", Password: "password", Opener: session})
	if err := socksServer.Start(0); err != nil {
		t.Fatal(err)
	}
	defer socksServer.Stop()
	httpServer := httpconnect.NewServer(httpconnect.Config{Username: "user", Password: "password", Opener: session})
	if err := httpServer.Start(0); err != nil {
		t.Fatal(err)
	}
	defer httpServer.Stop()

	socksClient := listenerSOCKSClient(t, socksServer.Addr().String())
	listenerWriteAll(t, socksClient, listenerSOCKSConnect("socks.example", 443))
	listenerReadEqual(t, socksClient, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	defer socksClient.Close()

	connectClient, err := net.DialTimeout("tcp4", httpServer.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connectClient.Close()
	_, _ = io.WriteString(connectClient, "CONNECT connect.example:443 HTTP/1.1\r\nHost: connect.example:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connectClient), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT response = %#v / %v", response, err)
	}
	_ = response.Body.Close()
	proxyURL, err := url.Parse("http://user:password@" + httpServer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for _, host := range []string{"idle-one.example", "idle-two.example"} {
		response, requestErr := client.Get("http://" + host + "/")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
	}
	directStreams := make([]io.ReadWriteCloser, 0, relayclient.MaxConcurrentStreams-4)
	for index := 0; index < relayclient.MaxConcurrentStreams-4; index++ {
		stream, openErr := session.OpenStream(context.Background(), "logical.example", 443)
		if openErr != nil {
			t.Fatalf("logical stream %d open error = %v", index+1, openErr)
		}
		directStreams = append(directStreams, stream)
	}
	defer func() {
		for _, stream := range directStreams {
			_ = stream.Close()
		}
	}()
	listenerWaitActive(t, session, 256)
	if _, openErr := session.OpenStream(context.Background(), "over-capacity.example", 443); !errors.Is(openErr, relayclient.ErrStreamLimit) {
		t.Fatalf("stream 257 error = %v, want ErrStreamLimit", openErr)
	}

	if err := httpServer.Stop(); err != nil {
		t.Fatal(err)
	}
	listenerWaitActive(t, session, 253)
	_ = socksClient.Close()
	listenerWaitActive(t, session, 252)
	replacement, openErr := session.OpenStream(context.Background(), "replacement.example", 443)
	if openErr != nil {
		t.Fatalf("replacement stream error = %v", openErr)
	}
	_ = replacement.Close()
}

func TestRealSessionCancelledOpenReleasesItsSlotOnce(t *testing.T) {
	fixture := newListenerRelayFixture(t, true)
	defer fixture.Close()
	session, err := relayclient.DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, openErr := session.OpenStream(ctx, "cancel.example", 443); result <- openErr }()
	select {
	case <-fixture.opened:
	case <-time.After(time.Second):
		t.Fatal("relay did not receive open")
	}
	cancel()
	if openErr := <-result; !errors.Is(openErr, context.Canceled) {
		t.Fatalf("cancelled open error = %v", openErr)
	}
	listenerWaitActive(t, session, 0)
	if stream, openErr := session.OpenStream(context.Background(), "after-cancel.example", 443); openErr != nil {
		t.Fatalf("replacement open error = %v", openErr)
	} else {
		_ = stream.Close()
	}
}

type listenerRelayFixture struct {
	server    *httptest.Server
	identity  relayclient.Identity
	holdOpens bool
	opened    chan struct{}
}

func newListenerRelayFixture(t *testing.T, holdOpens bool) *listenerRelayFixture {
	t.Helper()
	serverCertificate, serverCertificatePEM, _ := listenerCertificate(t, true)
	_, clientCertificatePEM, clientKeyPEM := listenerCertificate(t, false)
	fixture := &listenerRelayFixture{holdOpens: holdOpens, opened: make(chan struct{}, 1)}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/healthz" {
			_ = json.NewEncoder(writer).Encode(map[string]bool{"readiness": true, "agentConnected": true})
			return
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		ports := map[string]uint16{}
		holding := fixture.holdOpens
		for {
			_, raw, readErr := connection.ReadMessage()
			if readErr != nil {
				return
			}
			var envelope relayEnvelope
			if json.Unmarshal(raw, &envelope) != nil {
				return
			}
			switch envelope.Type {
			case "open":
				var target struct {
					Port uint16 `json:"port"`
				}
				decoded, _ := base64.RawURLEncoding.DecodeString(envelope.Payload)
				_ = json.Unmarshal(decoded, &target)
				ports[envelope.StreamID] = target.Port
				if holding {
					holding = false
					fixture.opened <- struct{}{}
					continue
				}
				listenerWriteEnvelope(connection, relayEnvelope{Version: 1, Type: "opened", StreamID: envelope.StreamID})
			case "data":
				if ports[envelope.StreamID] == 80 {
					listenerWriteEnvelope(connection, relayEnvelope{Version: 1, Type: "data", StreamID: envelope.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))})
					ports[envelope.StreamID] = 0
				}
			}
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	fixture.server = server
	fixture.identity = relayclient.Identity{RelayURL: server.URL, Role: "client", Serial: "03", PrivateKeyPEM: string(clientKeyPEM), CertificatePEM: string(clientCertificatePEM), CACertificatePEM: string(serverCertificatePEM)}
	return fixture
}

func (fixture *listenerRelayFixture) Close() { fixture.server.Close() }

func listenerCertificate(t *testing.T, server bool) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "127.0.0.1"}, DNSNames: []string{"127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM, privateKeyPEM
}

func listenerWriteEnvelope(connection *websocket.Conn, envelope relayEnvelope) {
	raw, _ := json.Marshal(envelope)
	_ = connection.WriteMessage(websocket.BinaryMessage, raw)
}
func listenerWaitActive(t *testing.T, session *relayclient.Session, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.Status().ActiveStreams == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active streams = %d, want %d", session.Status().ActiveStreams, want)
}
func listenerSOCKSClient(t *testing.T, address string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	listenerWriteAll(t, connection, []byte{5, 1, 2})
	listenerReadEqual(t, connection, []byte{5, 2})
	listenerWriteAll(t, connection, []byte{1, 4, 'u', 's', 'e', 'r', 8, 'p', 'a', 's', 's', 'w', 'o', 'r', 'd'})
	listenerReadEqual(t, connection, []byte{1, 0})
	return connection
}
func listenerSOCKSConnect(host string, port uint16) []byte {
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	return append(request, byte(port>>8), byte(port))
}
func listenerWriteAll(t *testing.T, writer io.Writer, value []byte) {
	t.Helper()
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
}
func listenerReadEqual(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("read = %v, want %v", got, want)
	}
}
