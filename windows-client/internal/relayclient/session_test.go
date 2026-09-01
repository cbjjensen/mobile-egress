package relayclient

import (
	"bytes"
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

func TestUnreadStreamDoesNotBlockOtherStreamFrames(t *testing.T) {
	t.Parallel()

	sendFlood := make(chan struct{})
	allowServerClose := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		first := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: first.StreamID})
		<-sendFlood
		for index := 0; index < 6; index++ {
			writeWireEnvelope(t, connection, wireEnvelope{
				Version: 1, Type: "data", StreamID: first.StreamID,
				Payload: base64.RawURLEncoding.EncodeToString([]byte{byte(index)}),
			})
		}
		for {
			next := readTestWireEnvelope(t, connection)
			if next.Type == "open" {
				writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: next.StreamID})
				<-allowServerClose
				return
			}
		}
	})
	defer fixture.Close()
	defer close(allowServerClose)
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first, err := session.OpenStream(context.Background(), "first.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	close(sendFlood)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := session.OpenStream(ctx, "second.example", 443)
	if err != nil {
		t.Fatalf("second stream was blocked by unread first stream: %v", err)
	}
	defer second.Close()
}

func TestSessionDrainsRelayDataBeforeNormalCloseForDelayedConsumer(t *testing.T) {
	const (
		attempts        = 12
		framesPerStream = 4
	)

	readyForClose := make(chan string)
	allowClose := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		for attempt := 0; attempt < attempts; attempt++ {
			open := readTestWireEnvelope(t, connection)
			writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
			readyForClose <- open.StreamID
			<-allowClose
			for frame := 0; frame < framesPerStream; frame++ {
				writeWireEnvelope(t, connection, wireEnvelope{
					Version: 1, Type: "data", StreamID: open.StreamID,
					Payload: base64.RawURLEncoding.EncodeToString([]byte{byte('a' + frame)}),
				})
			}
			writeWireEnvelope(t, connection, wireEnvelope{
				Version: 1, Type: "close", StreamID: open.StreamID,
				Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed")),
			})
		}
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	for attempt := 0; attempt < attempts; attempt++ {
		opened, err := session.OpenStream(context.Background(), "drain.example", 443)
		if err != nil {
			t.Fatalf("open stream %d: %v", attempt, err)
		}
		stream := opened.(*relayStream)
		if stream.id != <-readyForClose {
			t.Fatalf("stream %d readiness id mismatch", attempt)
		}
		allowClose <- struct{}{}

		select {
		case <-stream.done:
		case <-time.After(time.Second):
			t.Fatalf("stream %d did not process relay close", attempt)
		}

		payload := make([]byte, framesPerStream)
		if _, err := io.ReadFull(stream, payload); err != nil {
			t.Fatalf("stream %d lost data accepted before close: %v", attempt, err)
		}
		if string(payload) != "abcd" {
			t.Fatalf("stream %d payload = %q, want abcd", attempt, payload)
		}
		if count, err := stream.Read(make([]byte, 1)); count != 0 || err != io.EOF {
			t.Fatalf("stream %d post-drain read = (%d, %v), want (0, EOF)", attempt, count, err)
		}
	}
}

func TestRelayStreamLocalTerminalDoesNotDrainBufferedInbound(t *testing.T) {
	stream := &relayStream{inbound: make(chan []byte, 1), done: make(chan struct{}), openResult: make(chan error, 1)}
	stream.inbound <- []byte("accepted-before-local-close")
	stream.finish(io.EOF)

	if count, err := stream.Read(make([]byte, 1)); count != 0 || err != io.EOF {
		t.Fatalf("local terminal read = (%d, %v), want (0, EOF)", count, err)
	}
}

func TestSessionLocallyAdmitsThirtyTwoStreamsAndRejectsTheThirtyThird(t *testing.T) {
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		for {
			open := readTestWireEnvelope(t, connection)
			if open.Type != "open" {
				return
			}
			writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
		}
	})
	defer fixture.Close()
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	streams := make([]io.ReadWriteCloser, 0, 32)
	for index := 0; index < 32; index++ {
		stream, openErr := session.OpenStream(context.Background(), "capacity.example", 443)
		if openErr != nil {
			t.Fatalf("stream %d open error = %v", index+1, openErr)
		}
		streams = append(streams, stream)
	}
	if _, openErr := session.OpenStream(context.Background(), "over-capacity.example", 443); !errors.Is(openErr, ErrStreamLimit) {
		t.Fatalf("stream 33 open error = %v, want ErrStreamLimit", openErr)
	}
	for _, stream := range streams {
		_ = stream.Close()
	}
}

func TestSessionBoundsLateFrameTombstonesAtOneHundredTwentyEight(t *testing.T) {
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		for index := 0; index < 129; index++ {
			open := readTestWireEnvelope(t, connection)
			writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
			_ = readTestWireEnvelope(t, connection)
		}
	})
	defer fixture.Close()
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	for index := 0; index < 129; index++ {
		stream, openErr := session.OpenStream(context.Background(), "late-frame.example", 443)
		if openErr != nil {
			t.Fatalf("stream %d open error = %v", index+1, openErr)
		}
		if closeErr := stream.Close(); closeErr != nil {
			t.Fatalf("stream %d close error = %v", index+1, closeErr)
		}
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if got := len(session.closedStreams); got != 128 {
		t.Fatalf("late-frame tombstones = %d, want 128", got)
	}
	if got := len(session.closedOrder); got != 128 {
		t.Fatalf("late-frame tombstone order = %d, want 128", got)
	}
}

func TestRelayStreamFramesOutboundPayloadsAtSixteenKiB(t *testing.T) {
	frames := make(chan [][]byte, 1)
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		open := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
		var received [][]byte
		for total := 0; total < 32<<10+1; {
			data := readTestWireEnvelope(t, connection)
			payload, err := decodeWirePayload(data.Payload)
			if err != nil {
				t.Error(err)
				return
			}
			received = append(received, payload)
			total += len(payload)
		}
		frames <- received
	})
	defer fixture.Close()
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	stream, err := session.OpenStream(context.Background(), "frames.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	payload := bytes.Repeat([]byte("x"), 32<<10+1)
	if written, writeErr := stream.Write(payload); writeErr != nil || written != len(payload) {
		t.Fatalf("stream Write() = (%d, %v), want (%d, nil)", written, writeErr, len(payload))
	}
	got := <-frames
	wantSizes := []int{16 << 10, 16 << 10, 1}
	if len(got) != len(wantSizes) {
		t.Fatalf("outbound data frame count = %d, want %d", len(got), len(wantSizes))
	}
	for index, want := range wantSizes {
		if len(got[index]) != want {
			t.Fatalf("outbound data frame %d = %d bytes, want %d", index+1, len(got[index]), want)
		}
	}
}

func TestRelayStreamCloseCannotInterleaveBetweenOutboundChunks(t *testing.T) {
	frames := make(chan []wireEnvelope, 1)
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		open := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})

		received := make([]wireEnvelope, 0, 3)
		for len(received) < cap(received) {
			if err := connection.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
				t.Error(err)
				return
			}
			_, raw, err := connection.ReadMessage()
			if err != nil {
				break
			}
			var envelope wireEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Error(err)
				return
			}
			received = append(received, envelope)
		}
		frames <- received
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	opened, err := session.OpenStream(context.Background(), "close-race.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	stream := opened.(*relayStream)

	secondChunkReady := make(chan struct{})
	allowSecondChunk := make(chan struct{})
	stream.beforeWriteChunk = func(index int) {
		if index == 1 {
			close(secondChunkReady)
			<-allowSecondChunk
		}
	}
	type writeResult struct {
		count int
		err   error
	}
	result := make(chan writeResult, 1)
	go func() {
		count, writeErr := stream.Write(bytes.Repeat([]byte("x"), 32<<10))
		result <- writeResult{count: count, err: writeErr}
	}()

	select {
	case <-secondChunkReady:
	case <-time.After(time.Second):
		t.Fatal("write did not reach its second chunk")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	close(allowSecondChunk)

	write := <-result
	if write.count != 16<<10 || !errors.Is(write.err, io.ErrClosedPipe) {
		t.Fatalf("Write() after concurrent close = (%d, %v), want (%d, io.ErrClosedPipe)", write.count, write.err, 16<<10)
	}
	got := <-frames
	if len(got) != 2 || got[0].Type != "data" || got[1].Type != "close" {
		t.Fatalf("outbound frame order = %#v, want data then close only", got)
	}
	if count, err := stream.Write(nil); count != 0 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("empty Write() after close = (%d, %v), want (0, io.ErrClosedPipe)", count, err)
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

func newCustomSessionFixture(t *testing.T, websocketHandler func(*websocket.Conn)) *sessionFixture {
	t.Helper()
	ca, caKey, caPEM := newTestCA(t, "custom-session-ca")
	serverCertificate := newSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(20), Subject: pkix.Name{CommonName: "127.0.0.1"},
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
	fixture := &sessionFixture{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/healthz" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"readiness": true, "agentConnected": true})
			return
		}
		connection, upgradeErr := upgrader.Upgrade(writer, request, nil)
		if upgradeErr != nil {
			t.Error(upgradeErr)
			return
		}
		defer connection.Close()
		websocketHandler(connection)
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
		RelayURL: server.URL, Role: "client", Serial: "03",
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER})),
		CertificatePEM: string(clientCertificatePEM) + string(caPEM), CACertificatePEM: string(caPEM),
	}
	return fixture
}

func readTestWireEnvelope(t *testing.T, connection *websocket.Conn) wireEnvelope {
	t.Helper()
	_, raw, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var envelope wireEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

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
