package relayclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/internal/capacity"
	"mobile-egress/internal/destinationpolicy"
	"mobile-egress/internal/tunnelwire"
)

// AcceptAgent adapts an already authenticated direct phone WebSocket into the
// existing local proxy Tunnel. It never dials or queries a relay.
func AcceptAgent(connection *websocket.Conn) (*Session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{conn: connection, ctx: ctx, cancel: cancel, streams: make(map[string]*relayStream), draining: make(map[string]*relayStream), closedStreams: make(map[string]struct{}), inboundBudget: newInboundBudget(capacity.DataFramesPerLane, capacity.DataBytesPerLane), connected: true, agent: true, openPayload: newDirectResolver(net.DefaultResolver.LookupNetIP), direct: true, controlSlots: make(chan struct{}, capacity.ControlFramesPerSession)}
	s.binaryData.Store(true)
	if err := s.send(wireEnvelope{Version: 1, Type: "ping", Payload: base64.RawURLEncoding.EncodeToString([]byte(tunnelwire.Capability))}); err != nil {
		s.Close()
		return nil, errors.New("phone tunnel unavailable")
	}
	s.lastReceive.Store(time.Now().UnixNano())
	go s.readLoop()
	go s.directKeepalive()
	return s, nil
}

// This permit protects pending DNS work, not active streams or traffic rate.
var directResolverSlots = make(chan struct{}, capacity.ResolverWorkers)

func newDirectResolver(lookup func(context.Context, string, string) ([]netip.Addr, error)) func(context.Context, string, uint16) ([]byte, error) {
	return func(ctx context.Context, host string, port uint16) ([]byte, error) {
		if len(host) > 253 {
			return nil, errors.New("invalid target")
		}
		select {
		case directResolverSlots <- struct{}{}:
			defer func() { <-directResolverSlots }()
		default:
			return nil, errors.New("destination resolver busy")
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		addresses, err := lookup(ctx, "ip", host)
		if err != nil || ctx.Err() != nil || len(addresses) == 0 {
			return nil, errors.New("destination resolution failed")
		}
		for _, address := range addresses {
			if destinationpolicy.ValidatePublicTCPAddress(address.Unmap(), int(port)) != nil {
				return nil, errors.New("destination is not public")
			}
		}
		var families [2][]string
		seen := map[netip.Addr]bool{}
		first := 0
		for i, address := range addresses {
			address = address.Unmap()
			family := 0
			if address.Is6() {
				family = 1
			}
			if i == 0 {
				first = family
			}
			if seen[address] {
				continue
			}
			seen[address] = true
			families[family] = append(families[family], address.String())
		}
		candidates := make([]string, 0, 8)
		family := first
		for len(candidates) < 8 && len(families[0])+len(families[1]) > 0 {
			if len(families[family]) == 0 {
				family = 1 - family
			}
			candidates = append(candidates, families[family][0])
			families[family] = families[family][1:]
			family = 1 - family
		}
		return json.Marshal(struct {
			IP   string   `json:"ip"`
			Port uint16   `json:"port"`
			IPs  []string `json:"ips"`
		}{candidates[0], port, candidates})
	}
}
func (s *Session) directKeepalive() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, s.lastReceive.Load())) > 60*time.Second || s.send(wireEnvelope{Version: 1, Type: "ping"}) != nil {
				s.Close()
				return
			}
		}
	}
}
