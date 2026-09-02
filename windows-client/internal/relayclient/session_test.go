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
	stream := newInMemoryRelayStream(newInboundBudget(1, 64))
	if !stream.enqueueInbound([]byte("accepted-before-local-close")) {
		t.Fatal("inbound frame was rejected")
	}
	stream.finish(io.EOF)

	if count, err := stream.Read(make([]byte, 1)); count != 0 || err != io.EOF {
		t.Fatalf("local terminal read = (%d, %v), want (0, EOF)", count, err)
	}
}

func TestSessionAllows256CombinedStreamsAndRejects257(t *testing.T) {
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

	streams := make([]io.ReadWriteCloser, 0, 256)
	for index := 0; index < 256; index++ {
		stream, openErr := session.OpenStream(context.Background(), "capacity.example", 443)
		if openErr != nil {
			t.Fatalf("stream %d open error = %v", index+1, openErr)
		}
		streams = append(streams, stream)
	}
	if _, openErr := session.OpenStream(context.Background(), "over-capacity.example", 443); !errors.Is(openErr, ErrStreamLimit) {
		t.Fatalf("stream 257 open error = %v, want ErrStreamLimit", openErr)
	}
	for _, stream := range streams {
		_ = stream.Close()
	}
}

func TestClosedStreamHistoryRetainsExactly1024Tombstones(t *testing.T) {
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		for index := 0; index < 1025; index++ {
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

	for index := 0; index < 1025; index++ {
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
	if got := len(session.closedStreams); got != 1024 {
		t.Fatalf("late-frame tombstones = %d, want 1024", got)
	}
	if got := len(session.closedOrder); got != 1024 {
		t.Fatalf("late-frame tombstone order = %d, want 1024", got)
	}
}

func TestInboundPartialReadRetainsReservationUntilFinalByte(t *testing.T) {
	budget := newInboundBudget(4, 16)
	stream := newInMemoryRelayStream(budget)
	if !stream.enqueueInbound([]byte("abcd")) {
		t.Fatal("initial inbound frame was rejected")
	}
	assertInboundUsage(t, budget, 1, 4)

	first := make([]byte, 2)
	if count, err := stream.Read(first); count != 2 || err != nil || string(first) != "ab" {
		t.Fatalf("first Read() = (%d, %v, %q), want (2, nil, ab)", count, err, first)
	}
	assertInboundUsage(t, budget, 1, 4)

	second := make([]byte, 2)
	if count, err := stream.Read(second); count != 2 || err != nil || string(second) != "cd" {
		t.Fatalf("second Read() = (%d, %v, %q), want (2, nil, cd)", count, err, second)
	}
	assertInboundUsage(t, budget, 0, 0)
}

func TestInboundSessionBudgetIsSharedAcrossStreams(t *testing.T) {
	t.Run("frames", func(t *testing.T) {
		budget := newInboundBudget(2, 64)
		first := newInMemoryRelayStream(budget)
		second := newInMemoryRelayStream(budget)
		if !first.enqueueInbound([]byte("a")) || !second.enqueueInbound([]byte("b")) {
			t.Fatal("frames within the shared boundary were rejected")
		}
		if first.enqueueInbound(nil) {
			t.Fatal("aggregate frame N+1 was admitted")
		}
		assertInboundUsage(t, budget, 2, 2)
		if _, err := first.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		if !first.enqueueInbound(nil) {
			t.Fatal("released aggregate frame reservation was not reusable")
		}
		assertInboundUsage(t, budget, 2, 1)
	})

	t.Run("bytes", func(t *testing.T) {
		budget := newInboundBudget(4, 5)
		first := newInMemoryRelayStream(budget)
		second := newInMemoryRelayStream(budget)
		if !first.enqueueInbound([]byte("abc")) || !second.enqueueInbound([]byte("de")) {
			t.Fatal("bytes within the shared boundary were rejected")
		}
		if second.enqueueInbound([]byte("f")) {
			t.Fatal("aggregate byte N+1 was admitted")
		}
		assertInboundUsage(t, budget, 2, 5)
		if _, err := first.Read(make([]byte, 3)); err != nil {
			t.Fatal(err)
		}
		if !second.enqueueInbound([]byte("f")) {
			t.Fatal("released byte reservation was not reusable")
		}
		assertInboundUsage(t, budget, 2, 3)
	})
}

func TestInboundRemoteCloseDrainsBeforeRefundAndEOF(t *testing.T) {
	budget := newInboundBudget(4, 16)
	stream := newInMemoryRelayStream(budget)
	if !stream.enqueueInbound([]byte("ab")) || !stream.enqueueInbound([]byte("cd")) {
		t.Fatal("inbound frames were rejected")
	}
	stream.finishAfterInboundDrain()
	assertInboundUsage(t, budget, 2, 4)

	payload, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "abcd" {
		t.Fatalf("drained payload = %q, want abcd", payload)
	}
	assertInboundUsage(t, budget, 0, 0)
}

func TestInboundCloseAndFailureRefundExactlyOnce(t *testing.T) {
	tests := []struct {
		name      string
		terminate func(*Session, *relayStream)
	}{
		{name: "local close", terminate: func(_ *Session, stream *relayStream) { stream.finish(io.EOF) }},
		{name: "cancellation", terminate: func(_ *Session, stream *relayStream) { stream.finish(context.Canceled) }},
		{name: "session failure", terminate: func(session *Session, _ *relayStream) { session.failAll(ErrRelayUnavailable) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget := newInboundBudget(4, 16)
			stream := newInMemoryRelayStream(budget)
			stream.session.streams = map[string]*relayStream{stream.id: stream}
			if !stream.enqueueInbound([]byte("abcd")) {
				t.Fatal("inbound frame was rejected")
			}
			partial := make([]byte, 1)
			if count, err := stream.Read(partial); count != 1 || err != nil {
				t.Fatalf("partial Read() = (%d, %v), want (1, nil)", count, err)
			}
			assertInboundUsage(t, budget, 1, 4)

			test.terminate(stream.session, stream)
			test.terminate(stream.session, stream)
			assertInboundUsage(t, budget, 0, 0)
		})
	}
}

func TestInboundPerStreamSaturationClosesOnlyContributingStream(t *testing.T) {
	boundarySent := make(chan struct{})
	allowOverflow := make(chan struct{})
	closeReason := make(chan string, 1)
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		first := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: first.StreamID})
		second := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: second.StreamID})
		for index := 0; index < 32; index++ {
			writeWireEnvelope(t, connection, wireEnvelope{
				Version: 1, Type: "data", StreamID: first.StreamID,
				Payload: base64.RawURLEncoding.EncodeToString([]byte{byte(index)}),
			})
		}
		close(boundarySent)
		<-allowOverflow
		writeWireEnvelope(t, connection, wireEnvelope{
			Version: 1, Type: "data", StreamID: first.StreamID,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("overflow")),
		})
		closed := readTestWireEnvelope(t, connection)
		decoded, err := decodeWirePayload(closed.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		if closed.Type != "close" || closed.StreamID != first.StreamID {
			t.Errorf("saturation close = %#v, want first stream close", closed)
			return
		}
		closeReason <- string(decoded)
		writeWireEnvelope(t, connection, wireEnvelope{
			Version: 1, Type: "data", StreamID: second.StreamID,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("peer")),
		})
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	firstOpened, err := session.OpenStream(context.Background(), "full.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.OpenStream(context.Background(), "peer.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	first := firstOpened.(*relayStream)
	<-boundarySent
	waitForInboundUsage(t, session.inboundBudget, 32, 32)
	close(allowOverflow)
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("saturated stream was not closed")
	}
	if reason := <-closeReason; reason != "client_closed" {
		t.Fatalf("saturation close reason = %q, want client_closed", reason)
	}
	peer := make([]byte, 4)
	if _, err := io.ReadFull(second, peer); err != nil {
		t.Fatalf("peer stream stopped after unrelated saturation: %v", err)
	}
	if string(peer) != "peer" {
		t.Fatalf("peer payload = %q, want peer", peer)
	}
}

func TestInboundOpenCancellationRefundsAndReleasesSessionSlot(t *testing.T) {
	dataSent := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		first := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{
			Version: 1, Type: "data", StreamID: first.StreamID,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("waiting")),
		})
		close(dataSent)
		closed := readTestWireEnvelope(t, connection)
		if closed.Type != "close" || closed.StreamID != first.StreamID {
			t.Errorf("cancel close = %#v, want first stream close", closed)
			return
		}
		replacement := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: replacement.StreamID})
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, openErr := session.OpenStream(ctx, "cancel.example", 443)
		result <- openErr
	}()
	<-dataSent
	waitForInboundUsage(t, session.inboundBudget, 1, len("waiting"))
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OpenStream error = %v, want context.Canceled", err)
	}
	waitForInboundUsage(t, session.inboundBudget, 0, 0)
	if active := session.Status().ActiveStreams; active != 0 {
		t.Fatalf("active streams after cancellation = %d, want 0", active)
	}
	replacement, err := session.OpenStream(context.Background(), "replacement.example", 443)
	if err != nil {
		t.Fatalf("replacement stream open: %v", err)
	}
	_ = replacement.Close()
}

func TestInboundMalformedSessionTeardownRefundsExactlyOnce(t *testing.T) {
	dataSent := make(chan struct{})
	allowMalformed := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		open := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
		writeWireEnvelope(t, connection, wireEnvelope{
			Version: 1, Type: "data", StreamID: open.StreamID,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("retained")),
		})
		close(dataSent)
		<-allowMalformed
		if err := connection.WriteMessage(websocket.BinaryMessage, []byte("{")); err != nil {
			t.Error(err)
		}
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.OpenStream(context.Background(), "malformed.example", 443); err != nil {
		t.Fatal(err)
	}
	<-dataSent
	waitForInboundUsage(t, session.inboundBudget, 1, len("retained"))
	close(allowMalformed)
	select {
	case <-session.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("malformed relay frame did not close the session")
	}
	waitForInboundUsage(t, session.inboundBudget, 0, 0)
	session.failAll(ErrRelayUnavailable)
	assertInboundUsage(t, session.inboundBudget, 0, 0)
}

func TestInboundSessionCloseRefundsExactlyOnce(t *testing.T) {
	dataSent := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(connection *websocket.Conn) {
		open := readTestWireEnvelope(t, connection)
		writeWireEnvelope(t, connection, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
		writeWireEnvelope(t, connection, wireEnvelope{
			Version: 1, Type: "data", StreamID: open.StreamID,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("retained")),
		})
		close(dataSent)
		_, _, _ = connection.ReadMessage()
	})
	defer fixture.Close()

	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.OpenStream(context.Background(), "close.example", 443); err != nil {
		t.Fatal(err)
	}
	<-dataSent
	waitForInboundUsage(t, session.inboundBudget, 1, len("retained"))
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	assertInboundUsage(t, session.inboundBudget, 0, 0)
}

func newInMemoryRelayStream(budget *inboundBudget) *relayStream {
	session := &Session{
		ctx: context.Background(), streams: make(map[string]*relayStream),
		closedStreams: make(map[string]struct{}), inboundBudget: budget,
	}
	return &relayStream{
		session: session, id: "in-memory", inbound: make(chan inboundFrame, 32),
		done: make(chan struct{}), openResult: make(chan error, 1),
	}
}

func assertInboundUsage(t *testing.T, budget *inboundBudget, wantFrames, wantBytes int) {
	t.Helper()
	frames, bytes := budget.outstanding()
	if frames != wantFrames || bytes != wantBytes {
		t.Fatalf("inbound usage = (%d frames, %d bytes), want (%d frames, %d bytes)", frames, bytes, wantFrames, wantBytes)
	}
}

func waitForInboundUsage(t *testing.T, budget *inboundBudget, wantFrames, wantBytes int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		frames, bytes := budget.outstanding()
		if frames == wantFrames && bytes == wantBytes {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assertInboundUsage(t, budget, wantFrames, wantBytes)
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
