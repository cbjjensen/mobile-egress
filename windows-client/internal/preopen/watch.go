// Package preopen watches a proxy client while its relay stream is opening.
package preopen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// Result contains client data to forward after opening, or an opening failure.
type Result struct {
	Buffer []byte
	Err    error
}

// Watch buffers up to 64 KiB from reader while opening is pending and cancels
// the open on disconnect or excess data. reader must read from connection,
// optionally through a buffer. Closing openingComplete ends the watch; the
// result is published only after the interrupt read deadline has been cleared,
// at which point the caller can safely resume reading from reader.
func Watch(ctx context.Context, cancel context.CancelFunc, connection net.Conn, reader io.Reader, openingComplete <-chan struct{}, result chan<- Result) {
	var state Result
	// Publish only after the interrupt goroutine has exited and its deadline
	// has been cleared, so forwarding cannot race with either operation.
	defer func() { result <- state }()
	stopInterrupt := make(chan struct{})
	interruptDone := make(chan struct{})
	go func() {
		defer close(interruptDone)
		select {
		case <-openingComplete:
		case <-ctx.Done():
		case <-stopInterrupt:
			return
		}
		_ = connection.SetReadDeadline(time.Now())
	}()
	defer func() {
		close(stopInterrupt)
		<-interruptDone
		_ = connection.SetReadDeadline(time.Time{})
	}()
	var buffered bytes.Buffer
	readBuffer := make([]byte, 8<<10)
	for {
		select {
		case <-openingComplete:
			state = Result{Buffer: append([]byte(nil), buffered.Bytes()...)}
			return
		case <-ctx.Done():
			state = Result{Err: ctx.Err()}
			return
		default:
		}
		read, err := reader.Read(readBuffer)
		if read > 0 {
			if buffered.Len()+read > 64<<10 {
				cancel()
				state = Result{Err: errors.New("pre-open client data limit exceeded")}
				return
			}
			_, _ = buffered.Write(readBuffer[:read])
		}
		if err != nil {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				continue
			}
			cancel()
			state = Result{Err: err}
			return
		}
	}
}
