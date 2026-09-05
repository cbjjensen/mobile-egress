package httpconnect

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"runtime"
	"sort"
	"testing"
	"time"
)

// The opener yields once so the pre-open reader can run before completion, then
// completes using an in-memory stream without a network delay. No relay, mobile
// device, destination server, or external network participates in this benchmark.
type acknowledgmentOpener struct {
	finished chan struct{}
}

func (*acknowledgmentOpener) Healthy() bool { return true }

func (opener *acknowledgmentOpener) OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error) {
	runtime.Gosched()
	local, remote := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, remote)
		_ = remote.Close()
		opener.finished <- struct{}{}
	}()
	return local, nil
}

// Run with -run ^$ -bench ^BenchmarkConnectAcknowledgment$ -benchtime=100x
// -count=3 -benchmem. Latency spans sending CONNECT through receiving its 200
// response; the standard allocation metrics also include local TCP setup and
// teardown. A completed tunnel is closed before starting the next sample.
func BenchmarkConnectAcknowledgment(b *testing.B) {
	opener := &acknowledgmentOpener{finished: make(chan struct{}, 1)}
	server := NewServer(Config{Username: "user", Password: "password", Opener: opener})
	if err := server.Start(0); err != nil {
		b.Fatal(err)
	}
	defer server.Stop()
	address := server.Addr().String()
	samples := make([]time.Duration, b.N)
	const request = "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\r\n\r\n"
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		client, err := net.DialTimeout("tcp4", address, 5*time.Second)
		if err != nil {
			b.Fatal(err)
		}
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(client)
		started := time.Now()
		if _, err := io.WriteString(client, request); err != nil {
			_ = client.Close()
			b.Fatal(err)
		}
		response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		samples[n] = time.Since(started)
		if err != nil {
			_ = client.Close()
			b.Fatal(err)
		}
		_ = response.Body.Close()
		_ = client.Close()
		if response.StatusCode != http.StatusOK {
			b.Fatalf("CONNECT status=%d", response.StatusCode)
		}
		select {
		case <-opener.finished:
		case <-time.After(5 * time.Second):
			b.Fatal("tunnel did not close")
		}
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	b.ReportMetric(float64(samples[(len(samples)-1)/2])/float64(time.Microsecond), "median-us")
	b.ReportMetric(float64(samples[(len(samples)*95+99)/100-1])/float64(time.Microsecond), "p95-us")
}
