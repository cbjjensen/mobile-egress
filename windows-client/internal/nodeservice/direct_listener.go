package nodeservice

import (
	"net"
	"sync"
)

// Bound unauthenticated TCP/TLS work before net/http starts a goroutine. This
// limits control-plane connections, not streams multiplexed inside the phone
// tunnel. Excess sockets close immediately rather than waiting in a Go queue.
type directListener struct {
	net.Listener
	slots chan struct{}
}
type directConnection struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func newDirectListener(listener net.Listener, limit int) net.Listener {
	return &directListener{Listener: listener, slots: make(chan struct{}, limit)}
}
func (l *directListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &directConnection{Conn: conn, slots: l.slots}, nil
		default:
			conn.Close()
		}
	}
}
func (c *directConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}
