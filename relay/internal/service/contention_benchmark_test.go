package service

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/internal/tunnelwire"
	"mobile-egress/relay/internal/protocol"
)

// BenchmarkRelayMixedContention measures local relay contention, including the
// fixture endpoints' codecs, over authenticated loopback TLS/WebSockets. It does
// not measure cellular latency, Funnel, or transport head-of-line blocking on a
// real network. Every case retains one Client and one shared Agent WebSocket.
// Each bulk stream has at most one 32 KiB echo outstanding, keeping even eight
// streams comfortably below the production mailbox limits.
func BenchmarkRelayMixedContention(b *testing.B) {
	for _, transport := range []int{1, 2} {
		for _, bulkStreams := range []int{0, 1, 8} {
			b.Run(fmt.Sprintf("Transport%d/Bulk%d", transport, bulkStreams), func(b *testing.B) {
				benchmarkRelayMixedContention(b, transport, bulkStreams)
			})
		}
	}
}

func benchmarkRelayMixedContention(b *testing.B, transport, bulkStreams int) {
	_, _, clients, agent := newLatencyBenchmarkFixture(b, 1, transport)
	client := clients[0]
	binaryData := transport == 2
	write := func(conn *websocket.Conn, envelope protocol.Envelope) error {
		data, err := envelope.MarshalForPeer(binaryData)
		if err != nil {
			return err
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return conn.WriteMessage(websocket.BinaryMessage, data)
	}
	// Consume and verify the production capability advertisement before sending
	// raw data. Legacy sessions must receive no such negotiation traffic.
	if binaryData {
		for _, conn := range []*websocket.Conn{agent, client} {
			envelope, err := latencyBenchmarkRead(conn)
			if err != nil {
				b.Fatal(err)
			}
			payload, err := envelope.DecodePayload()
			if err != nil || envelope.Type != protocol.TypePing || string(payload) != tunnelwire.Capability {
				b.Fatalf("capability advertisement: %#v, %v", envelope, err)
			}
			if err := write(conn, protocol.Envelope{Version: 1, Type: protocol.TypePong}); err != nil {
				b.Fatal(err)
			}
		}
	}

	done := make(chan struct{})
	errors := make(chan error, 1)
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(done) }) }
	fail := func(err error) {
		select {
		case errors <- err:
		default:
		}
		stop()
	}
	var workers sync.WaitGroup
	b.Cleanup(func() {
		stop()
		_ = client.Close()
		_ = agent.Close()
		workers.Wait()
	})
	workers.Add(1)
	go func() {
		defer workers.Done()
		// This goroutine is the Agent's only reader and writer. The synthetic
		// Agent echoes bytes and never opens an external network connection.
		for {
			envelope, err := latencyBenchmarkRead(agent)
			if err != nil {
				fail(err)
				return
			}
			switch envelope.Type {
			case protocol.TypeOpen:
				envelope.Type = protocol.TypeOpened
				envelope.Payload = ""
			case protocol.TypeData:
			case protocol.TypePing:
				envelope.Type = protocol.TypePong
			default:
				fail(fmt.Errorf("unexpected Agent envelope type %q", envelope.Type))
				return
			}
			if err := write(agent, envelope); err != nil {
				fail(err)
				return
			}
		}
	}()

	ids := make([]string, bulkStreams+1)
	inbound := make(map[string]chan protocol.Envelope, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("contention-%d", i)
		inbound[ids[i]] = make(chan protocol.Envelope, 1)
		if err := write(client, openEnvelope(ids[i], "1.1.1.1", 443)); err != nil {
			b.Fatal(err)
		}
		envelope, err := latencyBenchmarkRead(client)
		if err != nil || envelope.Type != protocol.TypeOpened || envelope.StreamID != ids[i] {
			b.Fatalf("open: %#v, %v", envelope, err)
		}
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			envelope, err := latencyBenchmarkRead(client)
			if err != nil {
				fail(err)
				return
			}
			ch, ok := inbound[envelope.StreamID]
			if !ok || envelope.Type != protocol.TypeData || (envelope.Data != nil) != binaryData {
				fail(fmt.Errorf("unexpected Client envelope type %q, stream %q, binary %t", envelope.Type, envelope.StreamID, envelope.Data != nil))
				return
			}
			select {
			case ch <- envelope:
			case <-done:
				return
			}
		}
	}()
	var clientWrite sync.Mutex
	echo := func(id string, payload []byte) bool {
		clientWrite.Lock()
		err := write(client, protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: id, Data: payload})
		clientWrite.Unlock()
		if err != nil {
			fail(err)
			return false
		}
		select {
		case envelope := <-inbound[id]:
			actual, err := envelope.DecodePayload()
			if err != nil || !bytes.Equal(actual, payload) {
				fail(fmt.Errorf("echo payload mismatch for %s: %v", id, err))
				return false
			}
			return true
		case <-done:
			return false
		}
	}

	const bulkBytes = 32 << 10
	bulkPayload := bytes.Repeat([]byte{'b'}, bulkBytes)
	probePayload := bytes.Repeat([]byte{'p'}, 64)
	samples := make([]time.Duration, b.N)
	var bulkEchoes atomic.Int64
	start := make(chan struct{})
	stopLoad := make(chan struct{})
	ready := make(chan struct{}, bulkStreams)
	var loadWorkers sync.WaitGroup
	for _, id := range ids[1:] {
		loadWorkers.Add(1)
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			defer loadWorkers.Done()
			select {
			case <-start:
			case <-done:
				return
			}
			first := true
			for {
				select {
				case <-stopLoad:
					return
				case <-done:
					return
				default:
				}
				if !echo(id, bulkPayload) {
					return
				}
				bulkEchoes.Add(1)
				if first {
					ready <- struct{}{}
					first = false
				}
			}
		}(id)
	}
	b.ReportAllocs()
	b.ResetTimer()
	close(start)
	// Every bulk stream must complete a real echo before sampling probes. Bulk
	// workers then remain active until the last probe; no fixed sleeps or open
	// loop flood rates determine the offered throughput.
	for range bulkStreams {
		select {
		case <-ready:
		case <-done:
			b.Fatal(<-errors)
		}
	}
	for i := range samples {
		started := time.Now()
		if !echo(ids[0], probePayload) {
			b.Fatal(<-errors)
		}
		samples[i] = time.Since(started)
	}
	b.StopTimer()
	completedBulkBytes := bulkEchoes.Load() * 2 * bulkBytes
	close(stopLoad)
	loadWorkers.Wait() // Drain the bounded outstanding echoes outside timing.
	select {
	case err := <-errors:
		b.Fatal(err)
	default:
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	b.ReportMetric(float64(samples[len(samples)/2].Nanoseconds())/1000, "p50-us")
	b.ReportMetric(float64(samples[(len(samples)-1)*95/100].Nanoseconds())/1000, "p95-us")
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "probes/s")
	b.ReportMetric(float64(b.N*2*len(probePayload)), "probe-payload-B")
	b.ReportMetric(float64(completedBulkBytes), "bulk-payload-B")
	b.ReportMetric(float64(completedBulkBytes)/(1<<20)/b.Elapsed().Seconds(), "bulk-MiB/s")
}
