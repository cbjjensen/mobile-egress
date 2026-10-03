// Package clientapp contains the standalone graphical Client and its local OS
// IPC boundary. It never opens the service's secure store.
package clientapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/nodeservice"
)

const maximumIPCBytes = 3 << 20

type Service interface {
	Status() nodeservice.StandaloneStatus
	Pair(context.Context, string) error
	Import(context.Context, string) error
	Proxy(context.Context, string) (string, error)
}
type Request struct {
	Method string `json:"method"`
	Value  string `json:"value,omitempty"`
}
type Response struct {
	Status *nodeservice.StandaloneStatus `json:"status,omitempty"`
	Value  string                        `json:"value,omitempty"`
	Error  string                        `json:"error,omitempty"`
}

// Serve accepts only a platform listener which authenticates local peers before
// accepting. There is deliberately no TCP administration listener.
func Serve(ctx context.Context, listener net.Listener, service Service) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-done:
		}
	}()
	defer listener.Close()
	var clients sync.WaitGroup
	defer clients.Wait()
	// Bound concurrent authenticated callers to avoid unbounded pending requests.
	slots := make(chan struct{}, 16)
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("local Client control listener stopped")
		}
		select {
		case slots <- struct{}{}:
		default:
			connection.Close()
			continue
		}
		clients.Add(1)
		go func() { defer clients.Done(); defer func() { <-slots }(); serveConnection(ctx, connection, service) }()
	}
}
func serveConnection(ctx context.Context, connection net.Conn, service Service) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	decoder := json.NewDecoder(io.LimitReader(connection, maximumIPCBytes))
	decoder.DisallowUnknownFields()
	var request Request
	response := Response{}
	if err := decoder.Decode(&request); err != nil {
		response.Error = "Invalid local Client request."
	} else {
		requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		var err error
		switch request.Method {
		case "status":
			status := service.Status()
			response.Status = &status
		case "pair":
			err = service.Pair(requestCtx, request.Value)
		case "import":
			err = service.Import(requestCtx, request.Value)
		case "proxy":
			if request.Value != "http" && request.Value != "socks" {
				err = errors.New("Unknown proxy format.")
			} else {
				response.Value, err = service.Proxy(requestCtx, request.Value)
			}
		default:
			err = errors.New("Unknown local Client request.")
		}
		if err != nil {
			response.Error = err.Error()
		}
	}
	_ = json.NewEncoder(connection).Encode(response)
}

type LocalClient struct{}

func (LocalClient) call(ctx context.Context, method, value string) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	connection, err := dialLocal(ctx)
	if err != nil {
		return Response{}, errors.New("Client service is unavailable. Install or repair Mobile Egress Client, then reopen this app.")
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(connection).Encode(Request{Method: method, Value: value}); err != nil {
		return Response{}, errors.New("Client service connection was interrupted.")
	}
	var response Response
	decoder := json.NewDecoder(io.LimitReader(connection, maximumIPCBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, errors.New("Client service returned an invalid response.")
	}
	if response.Error != "" {
		return Response{}, errors.New(response.Error)
	}
	return response, nil
}
func (client LocalClient) Status() nodeservice.StandaloneStatus {
	response, err := client.call(context.Background(), "status", "")
	if err != nil {
		return nodeservice.StandaloneStatus{Phase: "unavailable", Message: err.Error()}
	}
	if response.Status == nil {
		return nodeservice.StandaloneStatus{Phase: "unavailable", Message: "Client service status is unavailable."}
	}
	return *response.Status
}
func (client LocalClient) Pair(ctx context.Context, value string) error {
	_, err := client.call(ctx, "pair", value)
	return err
}
func (client LocalClient) Import(ctx context.Context, value string) error {
	_, err := client.call(ctx, "import", value)
	return err
}
func (client LocalClient) Proxy(ctx context.Context, kind string) (string, error) {
	response, err := client.call(ctx, "proxy", kind)
	return response.Value, err
}
