package preopen

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestWatch(t *testing.T) {
	for _, reader := range []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{
		{"raw", func(r io.Reader) io.Reader { return r }},
		{"buffered", func(r io.Reader) io.Reader { return bufio.NewReader(r) }},
	} {
		t.Run(reader.name, func(t *testing.T) {
			t.Run("interrupt_and_cleanup", func(t *testing.T) {
				testOpeningInterruptAndDeadlineCleanupBeforeHandoff(t, reader.wrap)
			})
			t.Run("buffer_limit", func(t *testing.T) {
				testOpeningPreservesBytesAndEnforcesLimit(t, reader.wrap)
			})
		})
	}
}

// Suppress future deadlines so polling cannot accidentally satisfy this test.
// Immediate interrupts and clearing still exercise a real net.Pipe.
type openingConn struct {
	net.Conn
	reading    chan struct{}
	once       sync.Once
	clearing   chan struct{}
	allowClear chan struct{}
}

func (c *openingConn) Read(p []byte) (int, error) {
	c.once.Do(func() { close(c.reading) })
	return c.Conn.Read(p)
}
func (c *openingConn) SetReadDeadline(d time.Time) error {
	if d.IsZero() {
		close(c.clearing)
		<-c.allowClear
	} else if d.After(time.Now()) {
		return nil
	}
	return c.Conn.SetReadDeadline(d)
}

func testOpeningInterruptAndDeadlineCleanupBeforeHandoff(t *testing.T, newReader func(io.Reader) io.Reader) {
	for _, canceled := range []bool{false, true} {
		name := "opened"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			c := &openingConn{Conn: local, reading: make(chan struct{}), clearing: make(chan struct{}), allowClear: make(chan struct{})}
			defer close(c.allowClear)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opened := make(chan struct{})
			result := make(chan Result, 1)
			go Watch(ctx, cancel, c, newReader(c), opened, result)
			<-c.reading
			if canceled {
				cancel()
			} else {
				close(opened)
			}
			select {
			case <-c.clearing:
			case <-time.After(time.Second):
				t.Fatal("open completion/cancel did not interrupt blocked read")
			}
			select {
			case <-result:
				t.Fatal("published handoff before clearing read deadline")
			default:
			}
			// Release cleanup without closing the gate twice.
			c.allowClear <- struct{}{}
			var state Result
			select {
			case state = <-result:
			case <-time.After(time.Second):
				t.Fatal("watcher did not finish")
			}
			if canceled {
				if !errors.Is(state.Err, context.Canceled) {
					t.Fatalf("error = %v", state.Err)
				}
			} else if state.Err != nil {
				t.Fatal(state.Err)
			}
			sent := make(chan error, 1)
			go func() { _, err := peer.Write([]byte("after")); sent <- err }()
			got := make([]byte, 5)
			if _, err := io.ReadFull(local, got); err != nil {
				t.Fatalf("deadline survived handoff: %v", err)
			}
			if string(got) != "after" {
				t.Fatalf("forwarded bytes = %q", got)
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func testOpeningPreservesBytesAndEnforcesLimit(t *testing.T, newReader func(io.Reader) io.Reader) {
	for _, size := range []int{64 << 10, (64 << 10) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opened := make(chan struct{})
			result := make(chan Result, 1)
			c := local
			go Watch(ctx, cancel, c, newReader(c), opened, result)
			payload := bytes.Repeat([]byte("abcdefgh"), (size+7)/8)[:size]
			if _, err := peer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if size == 64<<10 {
				close(opened)
			}
			select {
			case state := <-result:
				if size == 64<<10 {
					if state.Err != nil || !bytes.Equal(state.Buffer, payload) {
						t.Fatalf("buffer length=%d error=%v", len(state.Buffer), state.Err)
					}
				} else {
					if state.Err == nil || ctx.Err() == nil {
						t.Fatal("excess data did not cancel opening")
					}
				}
			case <-time.After(time.Second):
				t.Fatal("watcher did not finish")
			}
		})
	}
}
