package nodeservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
)

type EnrollmentTransport interface {
	Submit(context.Context, clientcontrol.Invitation, clientcontrol.Bootstrap) (clientcontrol.Enrollment, error)
	Poll(context.Context, clientcontrol.Invitation, clientcontrol.Bootstrap) (clientcontrol.Enrollment, error)
	Report(context.Context, relayclient.Identity, string, uint64, string) error
}
type RelayEnrollmentTransport struct{}

func (RelayEnrollmentTransport) Submit(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return relayclient.SubmitClientBootstrap(ctx, invitation, bootstrap)
}
func (RelayEnrollmentTransport) Report(ctx context.Context, identity relayclient.Identity, id string, generation uint64, version string) error {
	return relayclient.ReportClientStatus(ctx, identity, id, generation, version)
}

func (RelayEnrollmentTransport) Poll(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return relayclient.PollClientEnrollment(ctx, invitation, bootstrap)
}

// StandaloneStatus is intentionally independent of the secret-bearing repository.
type StandaloneStatus struct {
	Phase        string `json:"phase"`
	Message      string `json:"message"`
	Running      bool   `json:"running"`
	Connected    bool   `json:"connected"`
	SOCKSAddress string `json:"socksAddress"`
	HTTPAddress  string `json:"httpAddress"`
	Generation   uint64 `json:"generation"`
	Version      string `json:"version"`
}

// Standalone owns enrollment, secure state and one existing proxy/tunnel service.
// Its public methods are called only by the authenticated local IPC server.
type Standalone struct {
	repository                      *Repository
	dialer                          Dialer
	enrollment                      EnrollmentTransport
	platform, architecture, version string
	opMu                            sync.Mutex
	mu                              sync.RWMutex
	status                          StandaloneStatus
	service                         *Service
	stop                            context.CancelFunc
	done                            chan error
	lastReport                      time.Time
	submittedID                     string
	denied                          atomic.Bool
	wake                            chan struct{}
}

func NewStandalone(repository *Repository, dialer Dialer, enrollment EnrollmentTransport, platform, architecture, version string) *Standalone {
	return &Standalone{repository: repository, dialer: dialer, enrollment: enrollment, platform: platform, architecture: architecture, version: version, wake: make(chan struct{}, 1), status: StandaloneStatus{Phase: "waiting", Message: "Paste an invitation from your controller to pair this Client.", Version: version}}
}
func (manager *Standalone) repositoryBootstrap(ctx context.Context) error {
	_, err := manager.repository.Bootstrap(ctx)
	return err
}
func (manager *Standalone) Run(ctx context.Context) error {
	if err := manager.repositoryBootstrap(ctx); err != nil {
		return errors.New("protected Client state is unavailable; repair the Client installation")
	}
	defer func() { manager.opMu.Lock(); defer manager.opMu.Unlock(); manager.stopService() }()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		manager.step(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-manager.wake:
		}
	}
}
func (manager *Standalone) signal() {
	select {
	case manager.wake <- struct{}{}:
	default:
	}
}
func (manager *Standalone) setProgress(phase, message string) {
	manager.mu.Lock()
	manager.status.Phase = phase
	manager.status.Message = message
	manager.mu.Unlock()
}
func (manager *Standalone) Status() StandaloneStatus {
	manager.mu.RLock()
	status := manager.status
	service := manager.service
	manager.mu.RUnlock()
	if service != nil {
		current := service.Status()
		status.Running = current.Running
		status.Connected = current.Connected
		status.SOCKSAddress = current.Address
		status.HTTPAddress = current.HTTPAddress
	}
	return status
}
func (manager *Standalone) Pair(ctx context.Context, bundle string) error {
	invitation, err := clientcontrol.DecodeInvitation(bundle)
	if err != nil {
		return errors.New("Invitation is invalid. Copy the complete invitation from your controller.")
	}
	manager.opMu.Lock()
	defer manager.opMu.Unlock()
	if !time.Now().Before(invitation.ExpiresAt) {
		pending, err := manager.repository.Pairing(ctx)
		if err != nil || pending.EnrollmentID != invitation.ID || !manager.hasBoundPendingPairing(ctx, pending) {
			return errors.New("Invitation has expired. Create a new invitation in your controller.")
		}
		// BeginPairing still requires the exact saved authority and expiry;
		// only the relay origin may change. The relay decides if this existing
		// key binding is approved and recoverable after capability expiry.
	}
	if err := manager.repository.BeginPairing(ctx, invitation); err != nil {
		return errors.New("Pairing could not be saved. Check Client status and repair the installation if needed.")
	}
	manager.setProgress("pairing", "Pairing saved. Keep your controller open while configuration is prepared.")
	manager.signal()
	return nil
}

func (manager *Standalone) hasBoundPendingPairing(ctx context.Context, pending PairingState) bool {
	if pending.Revoked || pending.Invitation == nil || pending.Bootstrap == nil {
		return false
	}
	if _, err := manager.repository.Runtime(ctx); !errors.Is(err, ErrNotConfigured) {
		return false
	}
	public, err := manager.repository.Bootstrap(ctx)
	return err == nil && pending.Bootstrap.CSRPEM == public.CSRPEM && pending.Bootstrap.ConfigurationPublicKey == public.ConfigurationPublicKey
}

func (manager *Standalone) Import(ctx context.Context, bundle string) error {
	update, err := clientcontrol.DecodeEndpointUpdate(bundle)
	if err != nil {
		return errors.New("Connection update is invalid. Copy the complete update from your controller.")
	}
	manager.opMu.Lock()
	defer manager.opMu.Unlock()
	if manager.denied.Load() {
		return errors.New("This Client is no longer authorized.")
	}
	pairing, err := manager.repository.Pairing(ctx)
	if err != nil || update.NodeID != pairing.NodeID || update.EnrollmentID != pairing.EnrollmentID {
		return errors.New("Connection update belongs to a different Client.")
	}
	envelope, err := decodeEnvelope(update.Envelope)
	if err != nil {
		return errors.New("Connection update is invalid.")
	}
	if err := manager.repository.ApplyPaired(ctx, envelope); err != nil {
		return errors.New("Connection update was rejected. It may be stale or belong to a different Client.")
	}
	manager.stopService()
	manager.lastReport = time.Time{}
	manager.setProgress("connecting", "Connection update saved. Connecting to the relay.")
	manager.signal()
	return nil
}

func (manager *Standalone) Proxy(ctx context.Context, kind string) (string, error) {
	if kind != "http" && kind != "socks" {
		return "", errors.New("Unknown proxy format.")
	}
	pairing, pairErr := manager.repository.Pairing(ctx)
	if pairErr != nil || pairing.Revoked || manager.denied.Load() {
		return "", errors.New("This Client is not authorized.")
	}
	runtime, err := manager.repository.Runtime(ctx)
	if err != nil {
		return "", errors.New("Pair this Client before copying its proxy.")
	}
	// Credential-bearing output is returned only by this explicit copy operation.
	if kind == "http" {
		return fmt.Sprintf("%s:%d:%s:%s", proxyendpoint.Host, proxyendpoint.HTTPConnectPort, runtime.Username, runtime.Password), nil
	}
	proxy := url.URL{Scheme: "socks5", Host: proxyendpoint.SOCKSAddress(), User: url.UserPassword(runtime.Username, runtime.Password)}
	return proxy.String(), nil
}

func (manager *Standalone) step(ctx context.Context) {
	manager.opMu.Lock()
	defer manager.opMu.Unlock()
	pairing, pairErr := manager.repository.Pairing(ctx)
	if manager.denied.Load() || (pairErr == nil && pairing.Revoked) {
		manager.stopService()
		manager.setProgress("revoked", "This Client is no longer authorized. Contact the controller owner.")
		return
	}
	runtime, runtimeErr := manager.repository.Runtime(ctx)
	if runtimeErr != nil && !errors.Is(runtimeErr, ErrNotConfigured) {
		manager.stopService()
		manager.setProgress("error", "Protected Client configuration is unavailable or expired. Repair the installation or contact the controller owner.")
		return
	}
	if runtimeErr != nil {
		if pairErr != nil || pairing.Invitation == nil {
			manager.setProgress("waiting", "Paste an invitation from your controller to pair this Client.")
			return
		}
		expired := !time.Now().Before(pairing.Invitation.ExpiresAt)
		if expired && !manager.hasBoundPendingPairing(ctx, pairing) {
			manager.setProgress("expired", "Invitation expired. Create a new invitation in your controller.")
			return
		}
		public, err := manager.repository.PairingBootstrap(ctx, manager.platform, manager.architecture, manager.version)
		if err != nil {
			manager.setProgress("error", "Protected Client state is unavailable. Repair the installation.")
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var enrollment clientcontrol.Enrollment
		if expired || manager.submittedID == pairing.EnrollmentID {
			enrollment, err = manager.enrollment.Poll(requestCtx, *pairing.Invitation, public)
		} else {
			enrollment, err = manager.enrollment.Submit(requestCtx, *pairing.Invitation, public)
		}
		cancel()
		if err != nil {
			manager.setProgress("pairing", "Waiting for the controller and relay. Pairing will retry automatically; create a new invitation if this one was canceled.")
			return
		}
		manager.submittedID = pairing.EnrollmentID
		if enrollment.State == "canceled" || enrollment.State == "expired" {
			manager.setProgress("expired", "Invitation is no longer active. Create a new invitation in your controller.")
			return
		}
		if enrollment.Configuration == nil {
			manager.setProgress("pairing", "Waiting for your controller to prepare configuration. Keep it open.")
			return
		}
		envelope, err := decodeEnvelope(enrollment.Configuration.Envelope)
		if err != nil || manager.repository.ApplyPaired(ctx, envelope) != nil {
			manager.setProgress("error", "Configuration could not be verified or saved. Retry from your controller.")
			return
		}
		runtime, runtimeErr = manager.repository.Runtime(ctx)
		if runtimeErr != nil {
			return
		}
	}
	if pairErr != nil {
		manager.setProgress("error", "This Client uses AWS management. Use its existing service installation.")
		return
	}
	manager.mu.Lock()
	manager.status.Generation = runtime.Generation
	manager.mu.Unlock()
	if manager.service == nil {
		serviceCtx, cancel := context.WithCancel(ctx)
		service := NewService(manager.repository, manager.dialer)
		manager.mu.Lock()
		manager.service = service
		manager.mu.Unlock()
		manager.stop = cancel
		manager.done = make(chan error, 1)
		done := manager.done
		go func() { done <- service.Run(serviceCtx) }()
		manager.setProgress("connecting", "Starting local proxies and connecting to your relay.")
		return
	}
	select {
	case err := <-manager.done:
		manager.stop()
		manager.stop = nil
		manager.done = nil
		manager.mu.Lock()
		manager.service = nil
		manager.mu.Unlock()
		if errors.Is(err, relayclient.ErrClientUnauthorized) {
			manager.denied.Store(true)
			_ = manager.repository.Revoke(ctx)
			manager.setProgress("revoked", "This Client is no longer authorized. Contact the controller owner.")
			return
		}
		if err != nil {
			manager.setProgress("error", fmt.Sprintf("Could not start local proxies at %s and %s. Close the application using these ports, then retry or repair this Client.", proxyendpoint.SOCKSAddress(), proxyendpoint.HTTPConnectAddress()))
		}
		return
	default:
	}
	status := manager.service.Status()
	if !status.Running || !status.Connected {
		manager.setProgress("connecting", "Waiting for the relay. Connection retries automatically.")
		return
	}
	if time.Since(manager.lastReport) < 30*time.Second {
		return
	}
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := manager.enrollment.Report(reportCtx, runtime.Identity, pairing.EnrollmentID, runtime.Generation, manager.version)
	cancel()
	if err != nil {
		var rejection *relayclient.ClientControlError
		if errors.As(err, &rejection) && (rejection.StatusCode == 401 || rejection.StatusCode == 403) {
			manager.denied.Store(true)
			manager.stopService()
			_ = manager.repository.Revoke(ctx)
			manager.setProgress("revoked", "This Client is no longer authorized. Contact the controller owner.")
			return
		}
		manager.setProgress("acknowledging", "Connected. Waiting for the controller to confirm this configuration.")
		return
	}
	if err := manager.repository.Acknowledged(ctx, runtime.Generation); err != nil {
		manager.setProgress("error", "Configuration receipt could not be saved. Repair the Client installation.")
		return
	}
	manager.lastReport = time.Now()
	manager.setProgress("ready", "Client connected. Copy a proxy to use it in your application.")
}

func (manager *Standalone) stopService() {
	if manager.stop != nil {
		manager.stop()
		if manager.done != nil {
			<-manager.done
		}
	}
	manager.stop = nil
	manager.done = nil
	manager.mu.Lock()
	manager.service = nil
	manager.mu.Unlock()
}
func decodeEnvelope(raw []byte) (sealedconfig.Envelope, error) {
	var envelope sealedconfig.Envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return envelope, errors.New("invalid envelope")
	}
	return envelope, nil
}
