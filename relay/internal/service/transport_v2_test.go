package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestAlternateAddressesNegotiatedOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "enhanced"}[enabled], func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("1.1.1.1"),
				}, nil
			}
			client := newDormantSession(fixture.service, "client", enrollment.RoleClient)
			agent := newDormantSession(fixture.service, "agent", enrollment.RoleAgent)
			agent.binaryData = enabled
			registerTestSessions(fixture.service, client, agent)
			defer closeTestSessions(client, agent)
			fixture.service.handleClientOpen(client, openEnvelope("candidates", "example.test", 443))
			waitForAdmittedStream(t, fixture.service, "candidates")
			item, ok := agent.outbound.poll()
			if !ok {
				t.Fatal("open was not queued")
			}
			defer item.complete()
			data, err := item.envelope.DecodePayload()
			if err != nil {
				t.Fatal(err)
			}
			var target struct {
				IP   string   `json:"ip"`
				Port int      `json:"port"`
				IPs  []string `json:"ips"`
			}
			if err = json.Unmarshal(data, &target); err != nil {
				t.Fatal(err)
			}
			if target.IP != "1.1.1.1" || target.Port != 443 {
				t.Fatalf("wrong target: %+v", target)
			}
			if enabled {
				if strings.Join(target.IPs, ",") != "1.1.1.1,2606:4700:4700::1111,8.8.8.8" {
					t.Fatalf("wrong candidate ordering/deduplication: %v", target.IPs)
				}
			} else if bytes.Contains(data, []byte(`"ips"`)) {
				t.Fatal("legacy Agent received unknown field")
			}
		})
	}
}

func TestLegacySessionRejectsUnnegotiatedBinaryData(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client")
	conn := mustDialSession(t, fixture, devices[0].client)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{2, 4, 0, 1, 's', 1}); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("unnegotiated binary did not close session: %v", err)
	}
}

func TestCandidateListBoundAndPolicyValidation(t *testing.T) {
	addresses := []netip.Addr{}
	for i := 1; i <= 12; i++ {
		addresses = append(addresses, netip.AddrFrom4([4]byte{1, 1, 1, byte(i)}))
	}
	if got := orderedCandidates(addresses); len(got) != 8 || got[0] != "1.1.1.1" || got[7] != "1.1.1.8" {
		t.Fatalf("candidate bound/order: %v", got)
	}
	fixture := newRelayFixture(t)
	defer fixture.Close()
	addresses = append(addresses, netip.MustParseAddr("127.0.0.1"))
	fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) { return addresses, nil }
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := dialTransport2(t, fixture, devices[1].client)
	defer agent.Close()
	writeEnvelope(t, client, openEnvelope("policy", "example.test", 443))
	result := readEnvelope(t, client)
	if result.Type != protocol.TypeRejected || decodedErrorCode(t, result) != "policy_denied" {
		t.Fatalf("unvalidated tail escaped policy: %+v", result)
	}
}

func TestBinaryMailboxChargesUntilCompletion(t *testing.T) {
	budget := newOutboundDataBudget(2, 3)
	mailbox := newOutboundMailbox(2, 2, budget)
	envelope := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: "s", Data: []byte{1, 2, 3}}
	if mailbox.enqueue(envelope) != outboundAdmitted {
		t.Fatal("first frame rejected")
	}
	item, ok := mailbox.poll()
	if !ok {
		t.Fatal("missing frame")
	}
	if mailbox.enqueue(envelope) != outboundDataSaturated {
		t.Fatal("in-flight binary debt was refunded prematurely")
	}
	item.complete()
	if mailbox.enqueue(envelope) != outboundAdmitted {
		t.Fatal("completed binary debt was not refunded")
	}
	mailbox.close()
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.frames != 0 || budget.bytes != 0 {
		t.Fatal("binary debt leaked on close")
	}
}

func dialTransport2(t *testing.T, fixture *relayFixture, client *http.Client) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{TLSClientConfig: client.Transport.(*http.Transport).TLSClientConfig.Clone()}
	conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(fixture.server.URL, "https")+"/v1/session?transport=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := readEnvelope(t, conn)
	payload, err := first.DecodePayload()
	if err != nil || first.Type != protocol.TypePing || string(payload) != "mobile-egress.transport.v2" {
		t.Fatalf("capability must be first frame: %+v", first)
	}
	return conn
}

func TestRelayTranslatesMixedTransportData(t *testing.T) {
	for _, newClient := range []bool{false, true} {
		t.Run(map[bool]string{false: "newAgent", true: "newClient"}[newClient], func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
			}
			_, devices := enrollDevices(t, fixture, "client", "agent")
			var client, agent *websocket.Conn
			if newClient {
				client = dialTransport2(t, fixture, devices[0].client)
				agent = mustDialSession(t, fixture, devices[1].client)
			} else {
				client = mustDialSession(t, fixture, devices[0].client)
				agent = dialTransport2(t, fixture, devices[1].client)
			}
			defer client.Close()
			defer agent.Close()
			writeEnvelope(t, client, openEnvelope("mixed", "example.test", 443))
			_ = readEnvelope(t, agent)
			writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "mixed"})
			_ = readEnvelope(t, client)
			for _, direction := range []struct {
				from, to *websocket.Conn
				binary   bool
			}{{client, agent, newClient}, {agent, client, !newClient}} {
				raw, err := (protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: "mixed", Data: []byte{0, 255, 128}}).MarshalForPeer(direction.binary)
				if err != nil {
					t.Fatal(err)
				}
				if err = direction.from.WriteMessage(websocket.BinaryMessage, raw); err != nil {
					t.Fatal(err)
				}
				got := readEnvelope(t, direction.to)
				payload, err := got.DecodePayload()
				if err != nil || !bytes.Equal(payload, []byte{0, 255, 128}) || (got.Data != nil) == direction.binary {
					t.Fatalf("incorrect translation: %+v, %v", got, err)
				}
			}
		})
	}
}
