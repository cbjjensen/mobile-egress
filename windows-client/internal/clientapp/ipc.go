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
	Proxy(context.Context, string) (string, error)
}
type DirectService interface {
	Service
	Configure(context.Context, nodeservice.DirectConfiguration) error
	IssueInvitation(context.Context) (string, error)
	CancelInvitation(context.Context) error
	ExportEndpointUpdate(context.Context) (string, error)
	Revoke(context.Context) error
}
type Request struct {
	Method string `json:"method"`
	Value  string `json:"value,omitempty"`
}
type Response struct {
	Activation *nodeservice.ActivationView   `json:"activation,omitempty"`
	Status     *nodeservice.StandaloneStatus `json:"status,omitempty"`
	Firewall   *FirewallStatus               `json:"firewall,omitempty"`
	Value      string                        `json:"value,omitempty"`
	Error      string                        `json:"error,omitempty"`
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
		case "start-hosted-activation", "resume-hosted-activation", "cancel-hosted-activation":
			if hosted, ok := service.(HostedService); ok {
				if request.Method != "start-hosted-activation" && request.Value != "" {
					err = errors.New("Activation control accepts no credentials or settings.")
				} else {
					switch request.Method {
					case "start-hosted-activation":
						var view nodeservice.ActivationView
						view, err = hosted.StartHostedActivation(requestCtx, request.Value)
						if err == nil {
							response.Activation = &view
						}
					case "resume-hosted-activation":
						var view nodeservice.ActivationView
						view, err = hosted.ResumeHostedActivation(requestCtx)
						if err == nil {
							response.Activation = &view
						}
					case "cancel-hosted-activation":
						err = hosted.CancelHostedActivation(requestCtx)
					}
				}
			} else {
				err = errors.New("Update the Client service to activate hosted connectivity.")
			}
		case "check-firewall", "retry-firewall":
			if request.Value != "" {
				err = errors.New("Firewall checks accept no program paths or settings.")
			} else if firewall, ok := service.(FirewallService); ok {
				var status FirewallStatus
				if request.Method == "retry-firewall" {
					status, err = firewall.RetryFirewall(requestCtx)
				} else {
					status, err = firewall.CheckFirewall(requestCtx)
				}
				if err == nil {
					response.Firewall = &status
				}
			} else {
				err = errors.New("Update the Client service to check its firewall access.")
			}
		case "status":
			status := service.Status()
			response.Status = &status
		case "configure":
			configuration, decodeErr := nodeservice.DecodeDirectConfiguration(request.Value)
			if decodeErr != nil {
				err = errors.New("Invalid endpoint configuration.")
			} else if direct, ok := service.(interface {
				Configure(context.Context, nodeservice.DirectConfiguration) error
			}); ok {
				err = direct.Configure(requestCtx, configuration)
			} else {
				err = errors.New("Update the Client service to configure a direct endpoint.")
			}
		case "issue-invitation":
			if direct, ok := service.(interface {
				IssueInvitation(context.Context) (string, error)
			}); ok {
				response.Value, err = direct.IssueInvitation(requestCtx)
			} else {
				err = errors.New("Update the Client service to pair a phone.")
			}
		case "cancel-invitation":
			if direct, ok := service.(interface{ CancelInvitation(context.Context) error }); ok {
				err = direct.CancelInvitation(requestCtx)
			} else {
				err = errors.New("Update the Client service to cancel pairing.")
			}
		case "export-update":
			if direct, ok := service.(interface {
				ExportEndpointUpdate(context.Context) (string, error)
			}); ok {
				response.Value, err = direct.ExportEndpointUpdate(requestCtx)
			} else {
				err = errors.New("Update the Client service to export a connection update.")
			}
		case "revoke":
			if direct, ok := service.(interface{ Revoke(context.Context) error }); ok {
				err = direct.Revoke(requestCtx)
			} else {
				err = errors.New("Update the Client service to remove a phone.")
			}
		case "pair", "import":
			err = errors.New("Relay invitations and updates are unsupported. Configure this Client's endpoint and pair your phone again.")
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
func (client LocalClient) Configure(ctx context.Context, configuration nodeservice.DirectConfiguration) error {
	raw, err := json.Marshal(configuration)
	if err != nil {
		return errors.New("Invalid endpoint configuration.")
	}
	_, err = client.call(ctx, "configure", string(raw))
	return err
}
func (client LocalClient) IssueInvitation(ctx context.Context) (string, error) {
	response, err := client.call(ctx, "issue-invitation", "")
	return response.Value, err
}
func (client LocalClient) CancelInvitation(ctx context.Context) error {
	_, err := client.call(ctx, "cancel-invitation", "")
	return err
}
func (client LocalClient) ExportEndpointUpdate(ctx context.Context) (string, error) {
	response, err := client.call(ctx, "export-update", "")
	return response.Value, err
}
func (client LocalClient) Revoke(ctx context.Context) error {
	_, err := client.call(ctx, "revoke", "")
	return err
}
func (client LocalClient) Proxy(ctx context.Context, kind string) (string, error) {
	response, err := client.call(ctx, "proxy", kind)
	return response.Value, err
}
