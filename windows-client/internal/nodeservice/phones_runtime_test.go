package nodeservice

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/internal/tunnelwire"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/securestore"
)

type phoneWireFixture struct {
	id                                            string
	config                                        *tls.Config
	conn                                          *websocket.Conn
	socksAddress, httpAddress, username, password string
}

func TestPhoneOccupiedPortRollsBackOnlyItsPairAndRetriesStablePorts(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	// Slot 9 avoids an installed first-phone Client. Never stop existing services;
	// a machine already using this last slot must run this check in isolation.
	next := m.cloneLocked()
	next.Phones[0].Slot = 9
	if err := m.saveLocked(ctx, next); err != nil {
		t.Fatal(err)
	}
	id := m.state.Phones[0].ID
	check, err := net.Listen("tcp", proxyendpoint.Address(1098))
	if err != nil {
		t.Skip("slot 9 SOCKS port already occupied by an existing process")
	}
	check.Close()
	blocker, err := net.Listen("tcp", proxyendpoint.Address(1099))
	if err != nil {
		t.Skip("slot 9 HTTP port already occupied by an existing process")
	}
	defer blocker.Close()
	sibling, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	m.opMu.Lock()
	p := phoneByID(m.state, sibling.PhoneID)
	rt := m.runtimeLocked(p.ID)
	sp, hp, err := newPhoneProxyPair(p, &rt.opener, 0, 0)
	if err != nil {
		m.opMu.Unlock()
		t.Fatal(err)
	}
	rt.socks = sp
	rt.http = hp
	m.runCtx = ctx
	m.status.Running = true
	m.opMu.Unlock()
	t.Cleanup(func() {
		m.opMu.Lock()
		defer m.opMu.Unlock()
		for _, runtime := range m.phones {
			m.stopPhoneProxyLocked(runtime)
		}
	})
	original, err := m.PhoneProxy(ctx, id, "http")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RetryPhoneProxy(ctx, id); err == nil {
		t.Fatal("occupied HTTP port reported success")
	}
	status, err := m.Phones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range status.Phones {
		if p.ID == id {
			if p.ProxyRunning || p.Phase != "proxy_error" || p.HTTPAddress != proxyendpoint.Address(1099) {
				t.Fatal("incorrect scoped port error", p)
			}
		} else if !p.ProxyRunning {
			t.Fatal("sibling proxy was stopped")
		}
	}
	if conn, err := net.DialTimeout("tcp", proxyendpoint.Address(1098), time.Second); err == nil {
		conn.Close()
		t.Fatal("partial proxy pair remained listening")
	}
	blocker.Close()
	if err := m.RetryPhoneProxy(ctx, id); err != nil {
		t.Fatal(err)
	}
	after, err := m.PhoneProxy(ctx, id, "http")
	if err != nil || after != original {
		t.Fatal("port retry renumbered proxy or credentials")
	}
	status, _ = m.Phones(ctx)
	for _, p := range status.Phones {
		if !p.ProxyRunning {
			t.Fatal("retry did not restore selected pair", p)
		}
	}
}

func connectEchoPhone(t *testing.T, m *Direct, endpoint string, config *tls.Config, label string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{TLSClientConfig: config, HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(endpoint, "https")+"/v2/direct/session?transport=2", http.Header{"X-Mobile-Egress-Protocol": []string{"direct/1"}})
	if err != nil {
		t.Fatal(err)
	}
	// Receive the real server's initial capability frame before returning.
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			e, err := tunnelwire.ParseEnvelope(raw)
			if err != nil {
				return
			}
			var replies []tunnelwire.Envelope
			switch e.Type {
			case tunnelwire.TypeOpen:
				replies = []tunnelwire.Envelope{{Version: 1, Type: tunnelwire.TypeOpened, StreamID: e.StreamID}}
			case tunnelwire.TypeData:
				replies = []tunnelwire.Envelope{{Version: 1, Type: tunnelwire.TypeData, StreamID: e.StreamID, Data: append([]byte(label+":"), e.Data...)}, {Version: 1, Type: tunnelwire.TypeClose, StreamID: e.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed"))}}
			}
			for _, reply := range replies {
				data, err := reply.MarshalForPeer(true)
				if err != nil || conn.WriteMessage(websocket.BinaryMessage, data) != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return conn
}

func phoneSOCKSExchange(address, user, password string, authorized bool) (string, error) {
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = conn.Write([]byte{5, 1, 2}); err != nil {
		return "", err
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil || !bytes.Equal(reply, []byte{5, 2}) {
		return "", fmt.Errorf("SOCKS method %v %v", reply, err)
	}
	auth := append([]byte{1, byte(len(user))}, []byte(user)...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	if _, err = conn.Write(auth); err != nil {
		return "", err
	}
	if _, err = io.ReadFull(conn, reply); err != nil {
		return "", err
	}
	if !authorized {
		if reply[1] == 0 {
			return "", fmt.Errorf("cross-phone credentials accepted")
		}
		return "rejected", nil
	}
	if reply[1] != 0 {
		return "", fmt.Errorf("SOCKS auth rejected")
	}
	if _, err = conn.Write([]byte{5, 1, 0, 1, 1, 1, 1, 1, 1, 187}); err != nil {
		return "", err
	}
	opened := make([]byte, 10)
	if _, err = io.ReadFull(conn, opened); err != nil || opened[1] != 0 {
		return "", fmt.Errorf("SOCKS open %v %v", opened, err)
	}
	if _, err = conn.Write([]byte("payload")); err != nil {
		return "", err
	}
	data, err := io.ReadAll(conn)
	return string(data), err
}
func phoneHTTPExchange(address, user, password string, authorized bool) (string, error) {
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(conn, "CONNECT 1.1.1.1:443 HTTP/1.1\r\nHost: 1.1.1.1:443\r\nProxy-Authorization: Basic %s\r\n\r\n", base64.StdEncoding.EncodeToString([]byte(user+":"+password)))
	if err != nil {
		return "", err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		return "", err
	}
	if !authorized {
		response.Body.Close()
		if response.StatusCode != 407 {
			return "", fmt.Errorf("cross-phone auth status %d", response.StatusCode)
		}
		return "rejected", nil
	}
	if response.StatusCode != 200 {
		return "", fmt.Errorf("CONNECT status %d", response.StatusCode)
	}
	if _, err = conn.Write([]byte("payload")); err != nil {
		return "", err
	}
	data, err := io.ReadAll(reader)
	return string(data), err
}

// All ten real TLS/WebSocket phone sessions share one authority/listener. The
// production proxy-pair constructor uses ephemeral ports here so the installed
// Client is never interrupted. Public address/slot arithmetic is tested separately.
func TestTenPhonesRealProxyRoutingCredentialIsolationAndRemoval(t *testing.T) {
	ctx := context.Background()
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	server := httptest.NewUnstartedServer(m.handler())
	endpoint := "https://" + server.Listener.Addr().String()
	if err := m.Configure(ctx, DirectConfiguration{Endpoint: endpoint, DisplayName: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	server.TLS = m.tlsConfig.Load().Clone()
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(m.state.CACertificatePEM))
	m.opMu.Lock()
	m.runCtx = ctx
	m.status.Running = true
	m.opMu.Unlock()
	t.Cleanup(func() {
		m.opMu.Lock()
		defer m.opMu.Unlock()
		for _, rt := range m.phones {
			m.stopPhoneProxyLocked(rt)
		}
	})
	var phones []phoneWireFixture
	for n := 0; n < 10; n++ {
		// Do not bind the installed Client's fixed ports while adding test records.
		m.opMu.Lock()
		m.runCtx = nil
		m.opMu.Unlock()
		added, err := m.AddPhone(ctx, fmt.Sprintf("Phone %d", n+1))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.RawURLEncoding.DecodeString(added.Bundle)
		var inv directInvitation
		_ = json.Unmarshal(raw, &inv)
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
		csr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
		identity := directTestEnroll(t, m, inv, csr)
		ack := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}
		if r := directAuthorityRequest(t, m, "POST", "/v2/direct/ack", server.Listener.Addr().String(), ack, identity); r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		private, _ := directKeyPEM(key)
		cert, err := tls.X509KeyPair([]byte(identity.CertificatePEM), []byte(private))
		if err != nil {
			t.Fatal(err)
		}
		config := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}}
		m.opMu.Lock()
		m.runCtx = ctx
		m.opMu.Unlock()
		conn := connectEchoPhone(t, m, endpoint, config, fmt.Sprintf("phone-%d", n))
		m.opMu.Lock()
		p := phoneByID(m.state, added.PhoneID)
		rt := m.runtimeLocked(p.ID)
		sp, hp, err := newPhoneProxyPair(p, &rt.opener, 0, 0)
		if err != nil {
			m.opMu.Unlock()
			t.Fatal(err)
		}
		rt.socks = sp
		rt.http = hp
		phones = append(phones, phoneWireFixture{id: p.ID, config: config, conn: conn, socksAddress: sp.Addr().String(), httpAddress: hp.Addr().String(), username: p.Username, password: p.Password})
		m.opMu.Unlock()
	}
	status, err := m.Phones(ctx)
	if err != nil || len(status.Phones) != 10 {
		t.Fatal(status, err)
	}
	for _, p := range status.Phones {
		if !p.Connected || !p.ProxyRunning {
			t.Fatal("phone not independently ready", p)
		}
	}
	var wg sync.WaitGroup
	for n, p := range phones {
		wg.Add(1)
		go func(n int, p phoneWireFixture) {
			defer wg.Done()
			other := phones[(n+1)%10]
			for _, test := range []struct {
				address  string
				exchange func(string, string, string, bool) (string, error)
			}{{p.socksAddress, phoneSOCKSExchange}, {p.httpAddress, phoneHTTPExchange}} {
				got, err := test.exchange(test.address, p.username, p.password, true)
				if err != nil || got != fmt.Sprintf("phone-%d:payload", n) {
					t.Errorf("phone %d routing/tail: %q %v", n, got, err)
				}
				if _, err := test.exchange(test.address, other.username, other.password, false); err != nil {
					t.Errorf("phone %d isolation: %v", n, err)
				}
			}
		}(n, p)
	}
	wg.Wait()
	// A replacement session closes only its own previous session.
	connectEchoPhone(t, m, endpoint, phones[3].config, "replacement")
	if got, err := phoneHTTPExchange(phones[3].httpAddress, phones[3].username, phones[3].password, true); err != nil || got != "replacement:payload" {
		t.Fatal(got, err)
	}
	if err := m.RevokePhone(ctx, phones[3].id); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", phones[3].httpAddress, time.Second); err == nil {
		t.Fatal("removed proxy still listening")
	}
	dialer := websocket.Dialer{TLSClientConfig: phones[3].config, HandshakeTimeout: time.Second}
	if conn, response, err := dialer.Dial("wss"+strings.TrimPrefix(endpoint, "https")+"/v2/direct/session?transport=2", http.Header{"X-Mobile-Egress-Protocol": []string{"direct/1"}}); err == nil {
		conn.Close()
		t.Fatal("removed phone reconnected")
	} else if response == nil || response.StatusCode != 401 {
		t.Fatal("wrong removal admission", response, err)
	}
	for n, p := range phones {
		if n == 3 {
			continue
		}
		got, err := phoneSOCKSExchange(p.socksAddress, p.username, p.password, true)
		if err != nil || got != fmt.Sprintf("phone-%d:payload", n) {
			t.Fatal("sibling disrupted", n, got, err)
		}
	}
}
