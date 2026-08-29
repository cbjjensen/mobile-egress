package relayclient

import (
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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSessionUsesRelayV1FramesAndWaitsForOpened(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture(t)
	defer fixture.Close()
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !session.Healthy() {
		t.Fatal("session is not healthy while relay agent is available")
	}

	result := make(chan io.ReadWriteCloser, 1)
	errorsChannel := make(chan error, 1)
	go func() {
		stream, openErr := session.OpenStream(context.Background(), "example.test", 443)
		if openErr != nil {
			errorsChannel <- openErr
			return
		}
		result <- stream
	}()

	select {
	case <-result:
		t.Fatal("OpenStream returned before relay sent opened")
	case err := <-errorsChannel:
		t.Fatalf("OpenStream returned an error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(fixture.allowOpened)

	var stream io.ReadWriteCloser
	select {
	case stream = <-result:
	case err := <-errorsChannel:
		t.Fatalf("OpenStream returned an error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("OpenStream did not return after opened")
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("client-bytes")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("agent-bytes"))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "agent-bytes" {
		t.Fatalf("stream read = %q", got)
	}
}

type sessionFixture struct {
	server      *httptest.Server
	identity    Identity
	allowOpened chan struct{}
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	ca, caKey, caPEM := newTestCA(t, "session-ca")
	serverCertificate := newSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "127.0.0.1"},
		DNSNames: []string{"127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientCertificatePEM := signPublicKey(t, ca, caKey, &clientKey.PublicKey, "client")
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER})
	fixture := &sessionFixture{allowOpened: make(chan struct{})}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/healthz" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"readiness": true, "agentConnected": true, "activeStreams": 0, "totalStreams": 0, "byteCount": 0})
			return
		}
		if request.URL.Path != "/v1/session" || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		messageType, raw, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			t.Errorf("message type = %d, want binary", messageType)
			return
		}
		var open wireEnvelope
		if err := json.Unmarshal(raw, &open); err != nil {
			t.Error(err)
			return
		}
		payload, err := base64.RawURLEncoding.DecodeString(open.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		if open.Version != 1 || open.Type != "open" || open.StreamID == "" || string(payload) != `{"host":"example.test","port":443}` {
			t.Errorf("open envelope = %#v, payload %s", open, payload)
			return
		}
		<-fixture.allowOpened
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID, Payload: ""})
		_, dataRaw, err := connection.ReadMessage()
		if err != nil {
			return
		}
		var data wireEnvelope
		if err := json.Unmarshal(dataRaw, &data); err != nil {
			t.Error(err)
			return
		}
		decoded, _ := base64.RawURLEncoding.DecodeString(data.Payload)
		if data.Type != "data" || string(decoded) != "client-bytes" {
			t.Errorf("data envelope = %#v, decoded %q", data, decoded)
			return
		}
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "data", StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("agent-bytes"))})
		_, _, _ = connection.ReadMessage()
	})
	server := httptest.NewUnstartedServer(handler)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS13,
		ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots,
	}
	server.StartTLS()
	fixture.server = server
	fixture.identity = Identity{
		RelayURL: server.URL, Role: "client", Serial: "03", PrivateKeyPEM: string(clientKeyPEM),
		CertificatePEM: string(clientCertificatePEM) + string(caPEM), CACertificatePEM: string(caPEM),
	}
	return fixture
}

func (fixture *sessionFixture) Close() { fixture.server.Close() }

func writeWireEnvelope(t *testing.T, connection *websocket.Conn, envelope wireEnvelope) {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, raw); err != nil && !strings.Contains(err.Error(), "closed") {
		t.Error(err)
	}
}
