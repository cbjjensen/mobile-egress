package desktop

import (
	"context"
	"errors"
	"sync"
	"time"

	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/relayservice"
	"mobile-egress/windows-client/internal/tailscale"
)

type statusComponent = string

const (
	componentTailscale statusComponent = "tailscale"
	componentHelper    statusComponent = "helper"
	componentRelay     statusComponent = "relay"
	componentMetadata  statusComponent = "metadata"
)

var controllerComponents = []statusComponent{componentTailscale, componentHelper, componentRelay, componentMetadata}

// ComponentStatus contains only display-safe freshness and failure information.
type ComponentStatus struct {
	Checking    bool   `json:"checking"`
	Stale       bool   `json:"stale"`
	Error       string `json:"error,omitempty"`
	LastSuccess string `json:"lastSuccess,omitempty"`
}
type ControllerSnapshot struct {
	Bridge              BridgeView                          `json:"bridge"`
	Nodes               []cloud.ManagedNodeView             `json:"nodes"`
	PendingReservations []string                            `json:"pendingReservations"`
	Components          map[statusComponent]ComponentStatus `json:"components"`
}

type componentResult struct {
	tailscale    tailscale.Status
	helper       relayServiceState
	ownerReady   bool
	ownerURL     string
	relayReady   bool
	nodes        []cloud.ManagedNodeView
	reservations []string
}
type monitorChecks map[statusComponent]func(context.Context) (componentResult, error)
type monitoredComponent struct {
	valid      bool
	result     componentResult
	completed  bool
	success    time.Time
	err        string
	generation uint64
	running    bool
	pending    bool
	holds      int
	cancel     context.CancelFunc
}
type statusMonitor struct {
	createdAt  time.Time
	mu         sync.Mutex
	platform   desktopPlatform
	checks     monitorChecks
	components map[statusComponent]*monitoredComponent
	now        func() time.Time
	ctx        context.Context
	cancel     context.CancelFunc
	stopped    bool
	workers    sync.WaitGroup
	done       chan struct{}
	cleanup    func()
}

func newStatusMonitor(platform desktopPlatform, checks monitorChecks) *statusMonitor {
	m := &statusMonitor{createdAt: time.Now(), platform: platform, checks: checks, components: make(map[statusComponent]*monitoredComponent), now: time.Now, done: make(chan struct{})}
	for _, key := range controllerComponents {
		m.components[key] = &monitoredComponent{}
	}
	if platform == platformWindows {
		m.components[componentHelper].result.helper = relayServiceNotRequired
	} else {
		m.components[componentHelper].result.helper = relayServiceNotRegistered
	}
	return m
}

func (m *statusMonitor) start(parent context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil || m.stopped {
		return
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	// The scheduler is counted before any stop can begin waiting.
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		m.request(controllerComponents...)
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.request(controllerComponents...)
			}
		}
	}()
}

func (m *statusMonitor) request(keys ...statusComponent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	for _, key := range keys {
		c := m.components[key]
		c.pending = true
		m.launchLocked(key, c)
	}
}
func (m *statusMonitor) launchLocked(key statusComponent, c *monitoredComponent) {
	if m.ctx == nil || m.ctx.Err() != nil || m.stopped || c.running || c.holds > 0 || !c.pending {
		return
	}
	check := m.checks[key]
	if check == nil {
		return
	}
	timeout := 5 * time.Second
	if key == componentTailscale {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	c.cancel = cancel
	c.running = true
	c.pending = false
	generation := c.generation
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		result, err := check(ctx)
		if err == nil {
			err = ctx.Err()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		c.running = false
		c.cancel = nil
		if !m.stopped && m.ctx.Err() == nil && generation == c.generation {
			m.publishLocked(key, result, err)
		}
		// A canceled native operation retains its slot until it actually returns.
		m.launchLocked(key, c)
	}()
}

func (m *statusMonitor) publish(key statusComponent, result componentResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped {
		m.publishLocked(key, result, err)
	}
}
func (m *statusMonitor) publishLocked(key statusComponent, result componentResult, err error) {
	c := m.components[key]
	c.completed = true
	c.valid = err == nil
	if err != nil {
		if c.success.IsZero() {
			c.result = result
		}
		c.err = "Unable to check " + string(key) + " status."
		return
	}
	c.result = result
	c.success = m.now()
	c.err = ""
}

// beginAction invalidates old work and pauses the affected checks throughout an
// action. Nested/concurrent actions share the pause; only the last one resumes.
func (m *statusMonitor) beginAction(keys ...statusComponent) func() {
	m.mu.Lock()
	for _, key := range keys {
		c := m.components[key]
		c.generation++
		c.holds++
		c.valid = false
		if c.cancel != nil {
			c.cancel()
		}
	}
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, key := range keys {
				c := m.components[key]
				c.generation++
				c.holds--
				c.pending = true
				m.launchLocked(key, c)
			}
		})
	}
}

func (m *statusMonitor) snapshot() ControllerSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	s := ControllerSnapshot{Components: make(map[statusComponent]ComponentStatus), Nodes: []cloud.ManagedNodeView{}, PendingReservations: []string{}}
	usable := !m.stopped
	for _, key := range controllerComponents {
		c := m.components[key]
		lastSuccess := c.success
		if lastSuccess.IsZero() {
			lastSuccess = m.createdAt
		}
		state := ComponentStatus{Checking: !c.completed || (!c.valid && c.err == ""), Error: c.err, Stale: now.Sub(lastSuccess) >= 12*time.Second}
		if !c.success.IsZero() {
			state.LastSuccess = c.success.UTC().Format(time.RFC3339Nano)
		}
		s.Components[key] = state
		// Managed-node storage has its own freshness; it does not determine
		// whether the local bridge can forward traffic or admit node setup.
		if key != componentMetadata && (state.Checking || state.Stale || state.Error != "" || !c.valid || c.holds > 0 || c.success.IsZero()) {
			usable = false
		}
	}
	ts := m.components[componentTailscale].result.tailscale
	relay := m.components[componentRelay].result
	helper := m.components[componentHelper].result.helper
	s.Bridge = BridgeView{Platform: string(m.platform), RelayServiceState: string(helper), TailscaleInstalled: ts.Installed, TailscaleOnline: ts.Online, FunnelReady: ts.FunnelReady, FQDN: ts.FQDN, PublicURL: ts.PublicURL, OwnerReady: relay.ownerReady, RelayReady: relay.relayReady, TailscaleError: s.Components[componentTailscale].Error}
	s.Bridge.Checking = s.Components[componentTailscale].Checking || s.Components[componentHelper].Checking || s.Components[componentRelay].Checking
	s.Bridge.Stale = s.Components[componentTailscale].Stale || s.Components[componentHelper].Stale || s.Components[componentRelay].Stale
	s.Bridge.NeedsRotation = relay.ownerReady && ts.Online && relay.ownerURL != ts.PublicURL
	s.Bridge.Ready = usable && bridgeReady(m.platform, helper, s.Bridge)
	metadata := m.components[componentMetadata].result
	s.Nodes = append(s.Nodes, metadata.nodes...)
	s.PendingReservations = append(s.PendingReservations, metadata.reservations...)
	return s
}

func (m *statusMonitor) beginStop() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		if m.cancel != nil {
			m.cancel()
		}
		for _, c := range m.components {
			c.generation++
			c.pending = false
			if c.cancel != nil {
				c.cancel()
			}
		}
		go func() {
			m.workers.Wait()
			if m.cleanup != nil {
				m.cleanup()
			}
			close(m.done)
		}()
	}
	m.mu.Unlock()
}

func (m *statusMonitor) stop(timeout time.Duration) {
	m.beginStop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-m.done:
	case <-timer.C:
	}
}

func (app *DesktopApp) newStatusMonitor() *statusMonitor {
	checks := monitorChecks{}
	checks[componentTailscale] = func(ctx context.Context) (componentResult, error) {
		if app.tailscale == nil {
			return componentResult{}, nil
		}
		status, err := app.tailscale.Inspect(ctx)
		if errors.Is(err, tailscale.ErrNotInstalled) || errors.Is(err, tailscale.ErrNotOnline) {
			err = nil
		}
		return componentResult{tailscale: status}, err
	}
	checks[componentHelper] = func(ctx context.Context) (componentResult, error) {
		state := app.currentRelayServiceState()
		if app.desktopPlatform() == platformMacOS && app.relayService != nil {
			observation := app.relayService.Observe(ctx)
			if observation.State == relayservice.StateUnavailable {
				return componentResult{helper: relayServiceUnavailable}, errors.New("helper unavailable")
			}
			state = relayStateFromObservation(observation)
		}
		return componentResult{helper: state}, nil
	}
	health := &controllerHealth{create: func(identity relayclient.Identity) (controllerHealthClient, error) {
		return relayclient.NewHealthClient(identity)
	}}
	checks[componentRelay] = func(ctx context.Context) (componentResult, error) {
		if app.core == nil {
			return health.read(ctx, relayclient.Identity{}, false)
		}
		identity, ready := app.core.OwnerSnapshot()
		return health.read(ctx, identity, ready)
	}
	checks[componentMetadata] = func(ctx context.Context) (componentResult, error) {
		if app.cloudRepository == nil {
			return componentResult{}, nil
		}
		nodes, reservations, err := app.cloudRepository.ControllerMetadata(ctx)
		return componentResult{nodes: nodes, reservations: reservations}, err
	}
	m := newStatusMonitor(app.desktopPlatform(), checks)
	m.cleanup = func() {
		health.close()
		if app.tailscale != nil {
			_ = app.tailscale.Close()
		}
	}
	return m
}

func (app *DesktopApp) GetControllerSnapshot() ControllerSnapshot {
	if app.monitor == nil {
		return newStatusMonitor(app.desktopPlatform(), nil).snapshot()
	}
	return app.monitor.snapshot()
}
func (app *DesktopApp) monitorAction(keys ...statusComponent) func() {
	if app.monitor == nil {
		return func() {}
	}
	return app.monitor.beginAction(keys...)
}
