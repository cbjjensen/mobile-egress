// Package hostedgateway adapts the authenticated outbound gateway transport to
// the existing Client TLS server. It never terminates phone TLS.
package hostedgateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	yamux "github.com/libp2p/go-yamux/v5"
)

const ALPN = "mobile-egress-gateway/1"

type Status struct {
	State  string
	Reason string
}
type Config struct {
	Address, ServerName, DeviceID, ClientID, Token string
	TLSConfig                                      *tls.Config
	OnStatus                                       func(Status)
}
type admission struct {
	Version  int    `json:"version"`
	DeviceID string `json:"deviceId"`
	ClientID string `json:"clientId"`
	Token    string `json:"token"`
}
type accepted struct {
	Version  int  `json:"version"`
	Accepted bool `json:"accepted"`
}
type Connector struct {
	config   Config
	ctx      context.Context
	cancel   context.CancelFunc
	incoming chan net.Conn
	mu       sync.Mutex
	running  bool
	session  *yamux.Session
	memory   memoryBudget
}

func New(c Config) (*Connector, error) {
	if c.DeviceID == "" || c.ClientID == "" || c.Token == "" || c.ServerName == "" {
		return nil, errors.New("hosted gateway activation required")
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return nil, errors.New("invalid gateway address")
	}
	if len(c.Token) > 2048 || len(c.DeviceID) > 128 || len(c.ClientID) > 128 {
		return nil, errors.New("invalid activation credentials")
	}
	if c.TLSConfig != nil {
		if c.TLSConfig.InsecureSkipVerify {
			return nil, errors.New("unverified broker TLS prohibited")
		}
		c.TLSConfig = c.TLSConfig.Clone()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Connector{config: c, ctx: ctx, cancel: cancel, incoming: make(chan net.Conn)}, nil
}
func (c *Connector) report(state, reason string) {
	if c.config.OnStatus != nil {
		c.config.OnStatus(Status{state, reason})
	}
}
func (c *Connector) Accept() (net.Conn, error) {
	select {
	case <-c.ctx.Done():
		return nil, net.ErrClosed
	case conn := <-c.incoming:
		if c.ctx.Err() != nil {
			conn.Close()
			return nil, net.ErrClosed
		}
		return conn, nil
	}
}
func (c *Connector) Addr() net.Addr { return address(c.config.Address) }

type address string

func (a address) Network() string { return "hostedgateway" }
func (a address) String() string  { return string(a) }
func (c *Connector) Close() error {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}
func (c *Connector) Run(parent context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("connector already running")
	}
	c.running = true
	c.mu.Unlock()
	stop := context.AfterFunc(parent, func() { c.Close() })
	defer stop()
	defer c.Close()
	retry := time.Second
	for c.ctx.Err() == nil {
		c.report("connecting", "")
		err := c.attach()
		if c.ctx.Err() != nil {
			break
		}
		reason := "gateway connection unavailable"
		if errors.Is(err, errRejected) {
			reason = "gateway activation rejected"
		}
		c.report("disconnected", reason)
		timer := time.NewTimer(retry + time.Duration(rand.Int64N(int64(retry/2))))
		select {
		case <-c.ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if retry < 15*time.Second {
			retry *= 2
		}
	}
	c.report("disconnected", "")
	return c.ctx.Err()
}

var errRejected = errors.New("gateway rejected admission")

func (c *Connector) attach() error {
	tc := &tls.Config{MinVersion: tls.VersionTLS13}
	if c.config.TLSConfig != nil {
		tc = c.config.TLSConfig.Clone()
	}
	tc.MinVersion = tls.VersionTLS13
	if tc.InsecureSkipVerify {
		return errors.New("unverified broker TLS prohibited")
	}
	tc.ServerName = c.config.ServerName
	tc.NextProtos = []string{ALPN}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}, Config: tc}
	conn, err := d.DialContext(c.ctx, "tcp", c.config.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if conn.(*tls.Conn).ConnectionState().NegotiatedProtocol != ALPN {
		return errRejected
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	stop := context.AfterFunc(c.ctx, func() { conn.Close() })
	defer stop()
	if err = writeFrame(conn, admission{1, c.config.DeviceID, c.config.ClientID, c.config.Token}); err != nil {
		return err
	}
	var ack accepted
	if err = readFrame(conn, &ack); err != nil {
		return err
	}
	if ack.Version != 1 || !ack.Accepted {
		return errRejected
	}
	conn.SetDeadline(time.Time{})
	yc := yamux.DefaultConfig()
	yc.LogOutput = io.Discard
	yc.MaxIncomingStreams = ^uint32(0)
	yc.MaxStreamWindowSize = 256 * 1024
	yc.AcceptBacklog = 16
	session, err := yamux.Server(conn, yc, func() (yamux.MemoryManager, error) { return &memorySpan{budget: &c.memory}, nil })
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	defer func() {
		session.Close()
		c.mu.Lock()
		if c.session == session {
			c.session = nil
		}
		c.mu.Unlock()
	}()
	c.report("connected", "")
	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		select {
		case c.incoming <- stream:
		case <-c.ctx.Done():
			stream.Close()
			return c.ctx.Err()
		case <-session.CloseChan():
			stream.Close()
			return net.ErrClosed
		}
	}
}
func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 4096 {
		return errors.New("admission too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err = w.Write(h[:]); err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(b))
	return err
}
func readFrame(r io.Reader, v any) error {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > 4096 {
		return errors.New("invalid admission length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing admission data")
	}
	return nil
}
