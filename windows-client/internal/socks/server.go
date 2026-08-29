// Package socks provides the authenticated loopback-only SOCKS5 listener.
package socks

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var ErrRelayUnavailable = errors.New("healthy relay agent unavailable")

type StreamOpener interface {
	Healthy() bool
	OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error)
}

type Config struct {
	Username    string
	Password    string
	Opener      StreamOpener
	OpenTimeout time.Duration
}

type Status struct {
	Running       bool
	ActiveStreams int
	BytesUp       int64
	BytesDown     int64
}

type trackedConnection struct {
	mu     sync.Mutex
	client net.Conn
	stream io.ReadWriteCloser
	closed bool
}

type Server struct {
	config Config

	mu          sync.Mutex
	listener    *net.TCPListener
	connections map[*trackedConnection]struct{}
	active      int
	context     context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	bytesUp     atomic.Int64
	bytesDown   atomic.Int64
}

func NewServer(config Config) *Server {
	if config.OpenTimeout <= 0 {
		config.OpenTimeout = 30 * time.Second
	}
	return &Server{config: config, connections: make(map[*trackedConnection]struct{})}
}

func (server *Server) Start(port uint16) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener != nil {
		return errors.New("SOCKS proxy is already running")
	}
	if server.config.Opener == nil || server.config.Username == "" || server.config.Password == "" {
		return errors.New("SOCKS proxy configuration is incomplete")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		return err
	}
	server.context, server.cancel = context.WithCancel(context.Background())
	server.listener = listener
	server.wg.Add(1)
	go server.acceptLoop(listener)
	return nil
}

func (server *Server) Addr() *net.TCPAddr {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return nil
	}
	address := server.listener.Addr().(*net.TCPAddr)
	return &net.TCPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone}
}

func (server *Server) Stop() error {
	server.mu.Lock()
	listener := server.listener
	if listener == nil {
		server.mu.Unlock()
		return nil
	}
	server.listener = nil
	if server.cancel != nil {
		server.cancel()
	}
	connections := make([]*trackedConnection, 0, len(server.connections))
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.mu.Unlock()

	err := listener.Close()
	for _, connection := range connections {
		connection.close()
	}
	server.wg.Wait()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (server *Server) Status() Status {
	server.mu.Lock()
	status := Status{Running: server.listener != nil, ActiveStreams: server.active}
	server.mu.Unlock()
	status.BytesUp = server.bytesUp.Load()
	status.BytesDown = server.bytesDown.Load()
	return status
}

func (server *Server) acceptLoop(listener *net.TCPListener) {
	defer server.wg.Done()
	for {
		connection, err := listener.AcceptTCP()
		if err != nil {
			return
		}
		tracked := &trackedConnection{client: connection}
		if !server.registerAccepted(listener, tracked) {
			return
		}
		go server.handleConnection(tracked)
	}
}

func (server *Server) registerAccepted(listener *net.TCPListener, tracked *trackedConnection) bool {
	server.mu.Lock()
	if server.listener != listener {
		server.mu.Unlock()
		tracked.close()
		return false
	}
	server.connections[tracked] = struct{}{}
	server.wg.Add(1)
	server.mu.Unlock()
	return true
}

func (server *Server) handleConnection(tracked *trackedConnection) {
	defer server.wg.Done()
	defer func() {
		tracked.close()
		server.mu.Lock()
		delete(server.connections, tracked)
		server.mu.Unlock()
	}()
	if !server.authenticate(tracked.client) {
		return
	}
	command, host, port, err := readRequest(tracked.client)
	if err != nil {
		return
	}
	if command != 1 {
		writeReply(tracked.client, 7)
		return
	}
	if !server.config.Opener.Healthy() || !server.reserveStream() {
		writeReply(tracked.client, 1)
		return
	}
	released := false
	release := func() {
		if !released {
			server.releaseStream()
			released = true
		}
	}
	defer release()

	openContext, cancelOpen := context.WithTimeout(server.context, server.config.OpenTimeout)
	openingComplete := make(chan struct{})
	clientState := make(chan preOpenResult, 1)
	go watchClientDuringOpen(openContext, cancelOpen, tracked.client, openingComplete, clientState)
	stream, err := server.config.Opener.OpenStream(openContext, host, port)
	close(openingComplete)
	state := <-clientState
	cancelOpen()
	if err != nil || state.err != nil {
		if stream != nil {
			_ = stream.Close()
		}
		release()
		if state.err == nil || errors.Is(state.err, context.DeadlineExceeded) {
			_ = writeReply(tracked.client, 1)
		}
		return
	}
	if !tracked.setStream(stream) {
		return
	}
	if writeReply(tracked.client, 0) != nil {
		return
	}
	if len(state.buffer) > 0 {
		written, writeErr := stream.Write(state.buffer)
		server.bytesUp.Add(int64(written))
		if writeErr != nil || written != len(state.buffer) {
			return
		}
	}

	completed := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(countingWriter{Writer: stream, count: &server.bytesUp}, tracked.client)
		completed <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(countingWriter{Writer: tracked.client, count: &server.bytesDown}, stream)
		completed <- struct{}{}
	}()
	<-completed
	_ = tracked.client.Close()
	_ = stream.Close()
	<-completed
}

type preOpenResult struct {
	buffer []byte
	err    error
}

func watchClientDuringOpen(ctx context.Context, cancel context.CancelFunc, connection net.Conn, openingComplete <-chan struct{}, result chan<- preOpenResult) {
	defer connection.SetReadDeadline(time.Time{})
	var buffered bytes.Buffer
	readBuffer := make([]byte, 8<<10)
	for {
		select {
		case <-openingComplete:
			result <- preOpenResult{buffer: append([]byte(nil), buffered.Bytes()...)}
			return
		case <-ctx.Done():
			result <- preOpenResult{err: ctx.Err()}
			return
		default:
		}
		_ = connection.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		read, err := connection.Read(readBuffer)
		if read > 0 {
			if buffered.Len()+read > 64<<10 {
				cancel()
				result <- preOpenResult{err: errors.New("pre-open client data limit exceeded")}
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
			result <- preOpenResult{err: err}
			return
		}
	}
}

func (tracked *trackedConnection) setStream(stream io.ReadWriteCloser) bool {
	tracked.mu.Lock()
	if tracked.closed {
		tracked.mu.Unlock()
		_ = stream.Close()
		return false
	}
	tracked.stream = stream
	tracked.mu.Unlock()
	return true
}

func (tracked *trackedConnection) close() {
	tracked.mu.Lock()
	if tracked.closed {
		tracked.mu.Unlock()
		return
	}
	tracked.closed = true
	client := tracked.client
	stream := tracked.stream
	tracked.mu.Unlock()
	_ = client.Close()
	if stream != nil {
		_ = stream.Close()
	}
}

func (server *Server) authenticate(connection net.Conn) bool {
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 || header[1] == 0 {
		return false
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return false
	}
	accepted := false
	for _, method := range methods {
		accepted = accepted || method == 2
	}
	if !accepted {
		_, _ = connection.Write([]byte{5, 0xff})
		return false
	}
	if _, err := connection.Write([]byte{5, 2}); err != nil {
		return false
	}
	authHeader := make([]byte, 2)
	if _, err := io.ReadFull(connection, authHeader); err != nil || authHeader[0] != 1 || authHeader[1] == 0 {
		return false
	}
	username := make([]byte, int(authHeader[1]))
	if _, err := io.ReadFull(connection, username); err != nil {
		return false
	}
	passwordLength := make([]byte, 1)
	if _, err := io.ReadFull(connection, passwordLength); err != nil || passwordLength[0] == 0 {
		return false
	}
	password := make([]byte, int(passwordLength[0]))
	if _, err := io.ReadFull(connection, password); err != nil {
		return false
	}
	validUser := subtle.ConstantTimeCompare(username, []byte(server.config.Username))
	validPassword := subtle.ConstantTimeCompare(password, []byte(server.config.Password))
	if validUser&validPassword != 1 {
		_, _ = connection.Write([]byte{1, 1})
		return false
	}
	_, err := connection.Write([]byte{1, 0})
	return err == nil
}

func readRequest(reader io.Reader) (byte, string, uint16, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, "", 0, err
	}
	if header[0] != 5 || header[2] != 0 {
		return 0, "", 0, errors.New("invalid SOCKS request")
	}
	var host string
	switch header[3] {
	case 1:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return 0, "", 0, err
		}
		host = net.IP(value).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil || length[0] == 0 {
			return 0, "", 0, errors.New("invalid domain address")
		}
		value := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, value); err != nil {
			return 0, "", 0, err
		}
		host = string(value)
	case 4:
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return 0, "", 0, err
		}
		host = net.IP(value).String()
	default:
		return 0, "", 0, errors.New("unsupported address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return 0, "", 0, err
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		return 0, "", 0, errors.New("invalid port")
	}
	return header[1], host, port, nil
}

func writeReply(writer io.Writer, reply byte) error {
	address := []byte{0, 0, 0, 0}
	if reply == 0 {
		address = []byte{127, 0, 0, 1}
	}
	response := []byte{5, reply, 0, 1}
	response = append(response, address...)
	response = append(response, 0, 0)
	_, err := writer.Write(response)
	return err
}

func (server *Server) reserveStream() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.active >= 4 || server.listener == nil {
		return false
	}
	server.active++
	return true
}

func (server *Server) releaseStream() {
	server.mu.Lock()
	if server.active > 0 {
		server.active--
	}
	server.mu.Unlock()
}

type countingWriter struct {
	io.Writer
	count *atomic.Int64
}

func (writer countingWriter) Write(value []byte) (int, error) {
	written, err := writer.Writer.Write(value)
	writer.count.Add(int64(written))
	return written, err
}

func loopbackAddress(port uint16) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
}
