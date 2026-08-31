// Package httpconnect provides an authenticated loopback-only HTTP forward and CONNECT proxy.
package httpconnect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
)

const maxPreOpenBytes = 64 << 10

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
	httpServer  *http.Server
	connections map[*trackedConnection]struct{}
	context     context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
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
		return errors.New("HTTP proxy is already running")
	}
	if server.config.Opener == nil || server.config.Username == "" || server.config.Password == "" {
		return errors.New("HTTP proxy configuration is incomplete")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		return err
	}
	server.context, server.cancel = context.WithCancel(context.Background())
	httpServer := &http.Server{
		Handler:        http.HandlerFunc(server.handle),
		MaxHeaderBytes: 1 << 20,
		BaseContext: func(net.Listener) context.Context {
			return server.context
		},
	}
	server.listener = listener
	server.httpServer = httpServer
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		_ = httpServer.Serve(listener)
	}()
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
	httpServer := server.httpServer
	if listener == nil {
		server.mu.Unlock()
		return nil
	}
	server.listener = nil
	server.httpServer = nil
	if server.cancel != nil {
		server.cancel()
	}
	connections := make([]*trackedConnection, 0, len(server.connections))
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.mu.Unlock()

	err := httpServer.Close()
	for _, connection := range connections {
		connection.close()
	}
	server.wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) handle(writer http.ResponseWriter, request *http.Request) {
	if !server.authenticate(request.Header.Get("Proxy-Authorization")) {
		writer.Header().Set("Proxy-Authenticate", `Basic realm="Mobile Egress"`)
		writer.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	if request.Method == http.MethodConnect {
		server.handleConnect(writer, request)
		return
	}
	server.handleForwardHTTP(writer, request)
}

func (server *Server) handleConnect(writer http.ResponseWriter, request *http.Request) {
	host, port, err := connectTarget(request.Host)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if !server.config.Opener.Healthy() {
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	tracked := &trackedConnection{client: client}
	if !server.register(tracked) {
		return
	}
	defer func() {
		tracked.close()
		server.mu.Lock()
		delete(server.connections, tracked)
		server.mu.Unlock()
	}()

	openContext, cancelOpen := context.WithTimeout(server.context, server.config.OpenTimeout)
	openingComplete := make(chan struct{})
	clientState := make(chan preOpenResult, 1)
	go watchClientDuringOpen(openContext, cancelOpen, client, buffered.Reader, openingComplete, clientState)
	stream, openErr := server.config.Opener.OpenStream(openContext, host, port)
	close(openingComplete)
	state := <-clientState
	cancelOpen()
	if openErr != nil || state.err != nil {
		if stream != nil {
			_ = stream.Close()
		}
		if state.err == nil || errors.Is(state.err, context.DeadlineExceeded) {
			_ = writeResponse(buffered.Writer, openFailureStatus(openErr))
		}
		return
	}
	if !tracked.setStream(stream) {
		return
	}
	if writeResponse(buffered.Writer, http.StatusOK) != nil {
		return
	}
	if len(state.buffer) > 0 {
		if written, err := stream.Write(state.buffer); err != nil || written != len(state.buffer) {
			return
		}
	}

	completed := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stream, buffered.Reader)
		completed <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, stream)
		completed <- struct{}{}
	}()
	<-completed
	_ = client.Close()
	_ = stream.Close()
	<-completed
}

func (server *Server) handleForwardHTTP(writer http.ResponseWriter, request *http.Request) {
	host, port, err := forwardTarget(request)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if !server.config.Opener.Healthy() {
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	openContext, cancelOpen := context.WithTimeout(request.Context(), server.config.OpenTimeout)
	stream, err := server.config.Opener.OpenStream(openContext, host, port)
	cancelOpen()
	if err != nil {
		writer.WriteHeader(openFailureStatus(err))
		return
	}
	tracked := &trackedConnection{stream: stream}
	if !server.register(tracked) {
		return
	}
	defer func() {
		tracked.close()
		server.mu.Lock()
		delete(server.connections, tracked)
		server.mu.Unlock()
	}()

	forwarded := request.Clone(request.Context())
	forwarded.RequestURI = ""
	forwarded.Close = true
	removeHopByHopHeaders(forwarded.Header)
	if err := forwarded.Write(stream); err != nil {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(stream), forwarded)
	if err != nil {
		writer.WriteHeader(http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	removeHopByHopHeaders(response.Header)
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func openFailureStatus(err error) int {
	if errors.Is(err, relayclient.ErrRelayUnavailable) || errors.Is(err, relayclient.ErrStreamLimit) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func (server *Server) register(connection *trackedConnection) bool {
	server.mu.Lock()
	if server.listener == nil {
		server.mu.Unlock()
		connection.close()
		return false
	}
	server.connections[connection] = struct{}{}
	server.mu.Unlock()
	return true
}

func (server *Server) authenticate(value string) bool {
	scheme, encoded, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") || encoded == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	defer clear(decoded)
	username, password, ok := bytes.Cut(decoded, []byte{':'})
	if !ok {
		return false
	}
	validUser := subtle.ConstantTimeCompare(username, []byte(server.config.Username))
	validPassword := subtle.ConstantTimeCompare(password, []byte(server.config.Password))
	return validUser&validPassword == 1
}

func connectTarget(value string) (string, uint16, error) {
	host, portText, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", 0, errors.New("invalid CONNECT target")
	}
	portValue, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || portValue == 0 {
		return "", 0, errors.New("invalid CONNECT target")
	}
	return host, uint16(portValue), nil
}

func forwardTarget(request *http.Request) (string, uint16, error) {
	if request.URL == nil || !strings.EqualFold(request.URL.Scheme, "http") || request.URL.User != nil {
		return "", 0, errors.New("invalid forward target")
	}
	host := request.URL.Hostname()
	if host == "" {
		return "", 0, errors.New("invalid forward target")
	}
	port := uint64(80)
	if portText := request.URL.Port(); portText != "" {
		parsed, err := strconv.ParseUint(portText, 10, 16)
		if err != nil || parsed == 0 {
			return "", 0, errors.New("invalid forward target")
		}
		port = parsed
	}
	return host, uint16(port), nil
}

func removeHopByHopHeaders(header http.Header) {
	for _, name := range strings.Split(header.Get("Connection"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			header.Del(name)
		}
	}
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

type preOpenResult struct {
	buffer []byte
	err    error
}

func watchClientDuringOpen(ctx context.Context, cancel context.CancelFunc, connection net.Conn, reader *bufio.Reader, openingComplete <-chan struct{}, result chan<- preOpenResult) {
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
		read, err := reader.Read(readBuffer)
		if read > 0 {
			if buffered.Len()+read > maxPreOpenBytes {
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

func writeResponse(writer *bufio.Writer, status int) error {
	reason := http.StatusText(status)
	if status == http.StatusOK {
		reason = "Connection Established"
	}
	if _, err := fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", status, reason); err != nil {
		return err
	}
	return writer.Flush()
}

func (connection *trackedConnection) setStream(stream io.ReadWriteCloser) bool {
	connection.mu.Lock()
	if connection.closed {
		connection.mu.Unlock()
		_ = stream.Close()
		return false
	}
	connection.stream = stream
	connection.mu.Unlock()
	return true
}

func (connection *trackedConnection) close() {
	connection.mu.Lock()
	if connection.closed {
		connection.mu.Unlock()
		return
	}
	connection.closed = true
	client := connection.client
	stream := connection.stream
	connection.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	if stream != nil {
		_ = stream.Close()
	}
}
