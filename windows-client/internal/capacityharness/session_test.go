//go:build capacityharness

package capacityharness

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
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/internal/capacity"
)

func TestCapacityWireEnvelopeAcceptsDataAtThirtyTwoKiB(t *testing.T) {
	raw, err := json.Marshal(capacityWireEnvelope{
		Version: 1, Type: "data", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCapacityWireEnvelope(raw); err != nil {
		t.Fatalf("parseCapacityWireEnvelope() rejected a 32 KiB data payload: %v", err)
	}
}

func TestCapacityWireEnvelopeRejectsDataOverThirtyTwoKiB(t *testing.T) {
	raw, err := json.Marshal(capacityWireEnvelope{
		Version: 1, Type: "data", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10+1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCapacityWireEnvelope(raw); err == nil {
		t.Fatal("parseCapacityWireEnvelope() accepted a data payload larger than 32 KiB")
	}
}

func TestCapacityWireEnvelopePreservesLargerNonDataPayloadLimit(t *testing.T) {
	raw, err := json.Marshal(capacityWireEnvelope{
		Version: 1, Type: "rejected", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10+1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseCapacityWireEnvelope(raw); err != nil {
		t.Fatalf("parseCapacityWireEnvelope() applied the data limit to a non-data payload: %v", err)
	}
}

func TestProductionSessionDriverUsesMTLSAndReceivesRemoteTwoHundredFiftySeventhStreamLimit(t *testing.T) {
	t.Parallel()

	fixture := newHarnessSessionFixture(t)
	defer fixture.server.Close()
	session, err := (ProductionSessionDialer{}).Dial(context.Background(), fixture.credential)
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	defer session.Close()

	streams := make([]CapacityStream, 0, capacity.ClientMaxConcurrentStreams)
	for index := 0; index < capacity.ClientMaxConcurrentStreams; index++ {
		stream, openErr := session.OpenStream(context.Background(), "echo.example.com", 443)
		if openErr != nil {
			t.Fatalf("OpenStream(%d) = %v", index+1, openErr)
		}
		streams = append(streams, stream)
	}
	if _, openErr := session.OpenStream(context.Background(), "echo.example.com", 443); !rejectedWith(openErr, "client_stream_limit") {
		t.Fatalf("OpenStream(%d) = %v, want remote client_stream_limit", capacity.ClientMaxConcurrentStreams+1, openErr)
	}
	if got := fixture.peerCertificates.Load(); got == 0 {
		t.Fatal("session fixture never received a verified mTLS peer certificate")
	}

	message := []byte("strict-session-echo")
	if count, writeErr := streams[0].Write(message); writeErr != nil || count != len(message) {
		t.Fatalf("Write() = %d/%v", count, writeErr)
	}
	received := make([]byte, len(message))
	if _, readErr := io.ReadFull(streams[0], received); readErr != nil || string(received) != string(message) {
		t.Fatalf("ReadFull() = %q/%v", received, readErr)
	}
	for _, stream := range streams {
		_ = stream.Close()
	}
}

func TestProductionSessionDriverFailsClosedOnTLSIdentityMismatchWithoutDisclosingMaterial(t *testing.T) {
	t.Parallel()

	fixture := newHarnessSessionFixture(t)
	defer fixture.server.Close()
	fixture.credential.PrivateKeyPEM = []byte("SECRET-PRIVATE-KEY-MATERIAL")
	_, err := (ProductionSessionDialer{}).Dial(context.Background(), fixture.credential)
	if err == nil {
		t.Fatal("Dial() accepted mismatched private key material")
	}
	if strings.Contains(err.Error(), "SECRET-PRIVATE-KEY-MATERIAL") {
		t.Fatal("Dial() error disclosed private key material")
	}
}

func TestOpenStreamCancellationDoesNotWaitForWriteAdmission(t *testing.T) {
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	session := &capacitySession{
		ctx: sessionCtx, cancel: cancelSession, connected: true,
		streams: make(map[string]*capacityStream), closed: make(map[string]struct{}),
		writeGate: make(chan struct{}, 1),
	}
	session.writeGate <- struct{}{}
	defer func() { <-session.writeGate }()

	ctx, cancel := context.WithCancel(context.Background())
	type openResult struct {
		stream CapacityStream
		err    error
	}
	result := make(chan openResult, 1)
	go func() {
		stream, err := session.OpenStream(ctx, "echo.example.com", 443)
		result <- openResult{stream: stream, err: err}
	}()
	cancel()
	select {
	case opened := <-result:
		if opened.stream != nil || !errors.Is(opened.err, context.Canceled) {
			t.Fatalf("OpenStream() = %#v/%v, want prompt context cancellation", opened.stream, opened.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OpenStream ignored cancellation while waiting for write admission")
	}
	session.mu.Lock()
	remaining := len(session.streams)
	session.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("canceled OpenStream retained %d pending streams", remaining)
	}
}

func TestCapacitySessionCloseContextJoinsReadLoopBeforeReturning(t *testing.T) {
	fixture := newHarnessSessionFixture(t)
	defer fixture.server.Close()
	opened, err := (ProductionSessionDialer{}).Dial(context.Background(), fixture.credential)
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	session, ok := opened.(*capacitySession)
	if !ok {
		t.Fatalf("Dial() returned %T, want *capacitySession", opened)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext() = %v", err)
	}
	select {
	case <-session.readDone:
	default:
		t.Fatal("CloseContext returned before the session read loop exited")
	}
}

func TestCapacitySessionCloseContextBoundsReadLoopJoin(t *testing.T) {
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	session := &capacitySession{
		ctx: sessionCtx, cancel: cancelSession, readDone: make(chan struct{}),
		streams: make(map[string]*capacityStream), closed: make(map[string]struct{}),
	}
	// Model already-closed local resources with an uncooperative read worker.
	session.closeOnce.Do(func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := session.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CloseContext() = %v, want bounded deadline", err)
	}
}

func TestCapacityStreamRejectsCloseClaimAfterPublishingDone(t *testing.T) {
	streamCtx, cancel := context.WithCancel(context.Background())
	stream := &capacityStream{
		done: make(chan struct{}), openResult: make(chan error, 1),
		ctx: streamCtx, cancel: cancel,
	}
	stream.finish(io.EOF)
	select {
	case <-stream.Done():
	default:
		t.Fatal("capacity stream did not publish Done")
	}
	if stream.TryBeginClose() {
		t.Fatal("capacity stream accepted a close claim after terminal publication")
	}
}

func TestCapacityStreamRejectsCloseClaimAfterSessionDeathBeforeStreamFinish(t *testing.T) {
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	disconnected := make(chan struct{})
	continueClose := make(chan struct{})
	session := &capacitySession{
		ctx: sessionCtx, cancel: cancelSession, connected: true,
		streams: make(map[string]*capacityStream), closed: make(map[string]struct{}),
		afterDisconnect: func() {
			close(disconnected)
			<-continueClose
		},
	}
	streamCtx, cancelStream := context.WithCancel(sessionCtx)
	stream := &capacityStream{
		session: session, id: "fixture", done: make(chan struct{}),
		openResult: make(chan error, 1), ctx: streamCtx, cancel: cancelStream,
	}
	session.streams[stream.id] = stream
	closeFinished := make(chan struct{})
	go func() {
		session.closeLocal()
		close(closeFinished)
	}()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("session did not publish disconnection before failAll")
	}
	select {
	case <-stream.Done():
		t.Fatal("stream terminal state published before the controlled failAll gap")
	default:
	}
	if stream.TryBeginClose() {
		t.Fatal("capacity stream accepted a close claim after session death")
	}
	close(continueClose)
	select {
	case <-closeFinished:
	case <-time.After(time.Second):
		t.Fatal("session close did not finish after the controlled gap")
	}
}

type harnessSessionFixture struct {
	server           *httptest.Server
	credential       *ClientCredential
	peerCertificates atomic.Int64
}

func newHarnessSessionFixture(t *testing.T) *harnessSessionFixture {
	t.Helper()
	ca, caKey, caPEM := newHarnessTestCA(t)
	serverCertificate, _, _ := newHarnessSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "capacity-client"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	_, clientCertificatePEM, clientKeyPEM := newHarnessSignedCertificate(t, ca, caKey, clientTemplate)
	fixture := &harnessSessionFixture{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		fixture.peerCertificates.Add(1)
		switch request.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"readiness": true, "agentConnected": true, "connectedClients": 0, "activeStreams": 0,
				"totalStreams": 0, "byteCount": 0, "errorCounts": map[string]int64{},
			})
		case "/v1/session":
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			active := 0
			for {
				messageType, raw, readErr := connection.ReadMessage()
				if readErr != nil {
					return
				}
				if messageType != websocket.BinaryMessage {
					return
				}
				var envelope capacityWireEnvelope
				if json.Unmarshal(raw, &envelope) != nil {
					return
				}
				switch envelope.Type {
				case "open":
					if active >= capacity.ClientMaxConcurrentStreams {
						envelope.Type = "rejected"
						envelope.Payload = base64.RawURLEncoding.EncodeToString([]byte("client_stream_limit"))
					} else {
						active++
						envelope.Type = "opened"
						envelope.Payload = ""
					}
				case "data":
					// Echo the same strict wire payload.
				case "close":
					active--
					continue
				case "pong":
					continue
				default:
					return
				}
				encoded, _ := json.Marshal(envelope)
				if connection.WriteMessage(websocket.BinaryMessage, encoded) != nil {
					return
				}
			}
		default:
			http.NotFound(writer, request)
		}
	})
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots,
	}
	server.StartTLS()
	fixture.server = server
	fixture.credential = &ClientCredential{
		RelayURL: server.URL, Role: "client", Serial: "A", PrivateKeyPEM: clientKeyPEM,
		CertificatePEM: string(clientCertificatePEM) + string(caPEM), CACertificatePEM: string(caPEM),
	}
	return fixture
}

func newHarnessTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "capacity-test-ca"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newHarnessSignedCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, template *x509.Certificate) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM, keyPEM
}
