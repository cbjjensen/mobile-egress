package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/relay/internal/protocol"
)

// These benchmarks use a real disk-backed store and local authenticated TLS
// sessions. The fixture Agent only echoes protocol payloads; it never dials IPs.
func BenchmarkRelayEchoLatency(b *testing.B) {
	for _, tc := range []struct {
		name       string
		clients    int
		delayedDNS bool
	}{
		{"Idle", 1, false},
		{"DelayedDNS", 1, true},
		{"Concurrent4", 4, false},
	} {
		b.Run(tc.name, func(b *testing.B) { benchmarkRelayEcho(b, tc.clients, tc.delayedDNS) })
	}
}

func benchmarkRelayEcho(b *testing.B, clientCount int, delayedDNS bool) {
	const dnsDelay = 20 * time.Millisecond
	relay, server, clients, agent := newLatencyBenchmarkFixture(b, clientCount)
	dnsStarted := make(chan struct{}, 1)
	relay.lookupNetIP = func(ctx context.Context, _, host string) ([]netip.Addr, error) {
		if host != "benchmark.invalid" {
			return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
		}
		dnsStarted <- struct{}{}
		timer := time.NewTimer(dnsDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
		}
	}
	_ = server
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		for {
			e, err := latencyBenchmarkRead(agent)
			if err != nil {
				return
			}
			switch e.Type {
			case protocol.TypeOpen:
				e.Type = protocol.TypeOpened
				e.Payload = ""
			case protocol.TypeData:
			case protocol.TypeClose:
				continue
			default:
				continue
			}
			if latencyBenchmarkWrite(agent, e) != nil {
				return
			}
		}
	}()
	b.Cleanup(func() { _ = agent.Close(); <-agentDone })
	for i, client := range clients {
		if err := latencyBenchmarkWrite(client, openEnvelope(fmt.Sprintf("echo-%d", i), "1.1.1.1", 443)); err != nil {
			b.Fatal(err)
		}
		e, err := latencyBenchmarkRead(client)
		if err != nil || e.Type != protocol.TypeOpened {
			b.Fatalf("open: %#v, %v", e, err)
		}
	}
	samples := make([]time.Duration, b.N)
	errors := make(chan error, clientCount)
	var wg sync.WaitGroup
	payload := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'x'}, 64))
	b.ReportAllocs()
	b.SetBytes(128) // 64 bytes outbound and 64 bytes echoed.
	b.ResetTimer()
	for worker, client := range clients {
		wg.Add(1)
		go func(worker int, client *websocket.Conn) {
			defer wg.Done()
			for i := worker; i < b.N; i += clientCount {
				dnsID := fmt.Sprintf("dns-%d", i)
				if delayedDNS {
					if err := latencyBenchmarkWrite(client, openEnvelope(dnsID, "benchmark.invalid", 443)); err != nil {
						errors <- err
						return
					}
					select {
					case <-dnsStarted:
					case <-time.After(5 * time.Second):
						errors <- fmt.Errorf("DNS lookup did not start")
						return
					}
				}
				started := time.Now()
				if err := latencyBenchmarkWrite(client, protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: fmt.Sprintf("echo-%d", worker), Payload: payload}); err != nil {
					errors <- err
					return
				}
				gotEcho, gotOpen := false, !delayedDNS
				for !gotEcho || !gotOpen {
					e, err := latencyBenchmarkRead(client)
					if err != nil {
						errors <- err
						return
					}
					if e.Type == protocol.TypeData && e.StreamID == fmt.Sprintf("echo-%d", worker) && e.Payload == payload {
						samples[i] = time.Since(started)
						gotEcho = true
					} else if delayedDNS && e.Type == protocol.TypeOpened && e.StreamID == dnsID {
						gotOpen = true
					} else {
						errors <- fmt.Errorf("unexpected envelope %#v", e)
						return
					}
				}
				if delayedDNS {
					if err := latencyBenchmarkWrite(client, protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: dnsID, Payload: base64.RawURLEncoding.EncodeToString([]byte("client_closed"))}); err != nil {
						errors <- err
						return
					}
				}
			}
		}(worker, client)
	}
	wg.Wait()
	b.StopTimer()
	close(errors)
	for err := range errors {
		b.Fatal(err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	b.ReportMetric(float64(samples[len(samples)/2].Nanoseconds())/1000, "median-us")
	b.ReportMetric(float64(samples[(len(samples)-1)*95/100].Nanoseconds())/1000, "p95-us")
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "echoes/s")
}

func latencyBenchmarkWrite(conn *websocket.Conn, envelope protocol.Envelope) error {
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func latencyBenchmarkRead(conn *websocket.Conn) (protocol.Envelope, error) {
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return protocol.Envelope{}, err
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.ParseEnvelope(data)
}

func newLatencyBenchmarkFixture(b *testing.B, count int, transportVersion ...int) (*Service, *httptest.Server, []*websocket.Conn, *websocket.Conn) {
	b.Helper()
	dir := filepath.Join(b.TempDir(), "state")
	code, err := Initialize(context.Background(), InitOptions{StateDir: dir, PublicName: "127.0.0.1"})
	if err != nil {
		b.Fatal(err)
	}
	relay, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = relay.Close() })
	server := httptest.NewUnstartedServer(relay.Handler())
	server.TLS = relay.TLSConfig()
	server.StartTLS()
	b.Cleanup(server.Close)
	ca, err := os.ReadFile(filepath.Join(dir, caCertFilename))
	if err != nil {
		b.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		b.Fatal("invalid CA")
	}
	base := server.Client().Transport.(*http.Transport)
	base.TLSClientConfig.RootCAs = roots
	base.TLSClientConfig.InsecureSkipVerify = false
	post := func(client *http.Client, path string, body any, result any) {
		encoded, err := json.Marshal(body)
		if err != nil {
			b.Fatal(err)
		}
		response, err := client.Post(server.URL+path, "application/json", bytes.NewReader(encoded))
		if err != nil {
			b.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			b.Fatalf("%s: HTTP %d", path, response.StatusCode)
		}
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			b.Fatal(err)
		}
	}
	enroll := func(code, role string) *http.Client {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
		if err != nil {
			b.Fatal(err)
		}
		var result enrollmentResult
		post(server.Client(), "/v1/enroll", map[string]string{"code": code, "role": role, "csrPem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))}, &result)
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			b.Fatal(err)
		}
		cert, err := tls.X509KeyPair([]byte(result.CertificatePEM), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		if err != nil {
			b.Fatal(err)
		}
		transport := base.Clone()
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		b.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport}
	}
	owner := enroll(code, "owner")
	dial := func(role string) *websocket.Conn {
		var pairing pairingResult
		post(owner, "/v1/pairing-codes", map[string]string{"role": role}, &pairing)
		client := enroll(pairing.Code, role)
		dialer := websocket.Dialer{TLSClientConfig: client.Transport.(*http.Transport).TLSClientConfig.Clone()}
		endpoint := "wss" + strings.TrimPrefix(server.URL, "https") + "/v1/session"
		if len(transportVersion) > 0 && transportVersion[0] == 2 {
			endpoint += "?transport=2"
		}
		conn, _, err := dialer.Dial(endpoint, nil)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	agent := dial("agent")
	clients := make([]*websocket.Conn, count)
	for i := range clients {
		clients[i] = dial("client")
	}
	return relay, server, clients, agent
}
