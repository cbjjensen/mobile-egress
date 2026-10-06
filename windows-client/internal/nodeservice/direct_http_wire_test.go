package nodeservice

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The phone consumes HTTP/1.1 bytes directly. A ResponseRecorder does not apply
// net/http's response framing, so it cannot expose a consumer that only accepts
// Content-Length and rejects the actual chunked enrollment response.
func TestDirectRealTLSChunkedEnrollmentRecoveryAndAcknowledgement(t *testing.T) {
	m, invitation, _ := directTestConfigured(t)
	server := httptest.NewUnstartedServer(m.handler())
	server.TLS = m.tlsConfig.Load().Clone()
	server.StartTLS()
	defer server.Close()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(invitation.CACertificatePEM)) {
		t.Fatal("test authority could not be loaded")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "client.example", NextProtos: []string{"http/1.1"}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	enrollment := directEnrollBody(invitation, csrPEM)
	first := directTLSWireExchange(t, server.Listener.Addr().String(), config, http.MethodPost, "/v2/direct/enroll", enrollment)
	if first.response.StatusCode != http.StatusCreated || len(first.body) <= 2048 {
		t.Fatalf("enrollment status=%d decoded bytes=%d; expected a certificate response exceeding the automatic length buffer", first.response.StatusCode, len(first.body))
	}
	if first.response.ContentLength != -1 || len(first.response.TransferEncoding) != 1 || first.response.TransferEncoding[0] != "chunked" {
		t.Fatalf("enrollment framing: content length=%d transfer encoding=%v", first.response.ContentLength, first.response.TransferEncoding)
	}
	if !bytes.Contains(first.wire, []byte("\r\nTransfer-Encoding: chunked\r\n")) || !bytes.HasSuffix(first.wire, []byte("\r\n0\r\n\r\n")) {
		t.Fatal("raw enrollment response does not contain complete chunked HTTP/1.1 framing")
	}
	var identity directIdentityResponse
	if err := json.Unmarshal(first.body, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.ClientID != invitation.ClientID || identity.PairingID == "" || identity.Role != "agent" {
		t.Fatal("decoded enrollment has the wrong identity")
	}
	if status := m.Status(); status.Paired || status.Phase != "acknowledging" || status.Connected {
		t.Fatalf("issuing credentials must leave pairing pending: paired=%t phase=%s connected=%t", status.Paired, status.Phase, status.Connected)
	}

	// Model a phone retrying after it rejected/lost the first HTTP response.
	// The same key must recover the existing identity, without an implicit ACK.
	retry := directTLSWireExchange(t, server.Listener.Addr().String(), config, http.MethodPost, "/v2/direct/enroll", enrollment)
	if retry.response.StatusCode != http.StatusCreated || !bytes.Equal(retry.body, first.body) || m.Status().Paired {
		t.Fatal("same-key retry did not retain the unacknowledged issued identity")
	}
	private, err := directKeyPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair([]byte(identity.CertificatePEM), []byte(private))
	if err != nil {
		t.Fatal(err)
	}
	authenticated := config.Clone()
	authenticated.Certificates = []tls.Certificate{certificate}
	ackBody := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}
	unauthenticated := directTLSWireExchange(t, server.Listener.Addr().String(), config, http.MethodPost, "/v2/direct/ack", ackBody)
	if unauthenticated.response.StatusCode != http.StatusUnauthorized || m.Status().Paired {
		t.Fatal("ACK without the issued TLS identity completed pairing")
	}
	ack := directTLSWireExchange(t, server.Listener.Addr().String(), authenticated, http.MethodPost, "/v2/direct/ack", ackBody)
	if ack.response.StatusCode != http.StatusOK || string(ack.body) != "{\"status\":\"paired\"}\n" || !m.Status().Paired || m.Status().UpdatePending {
		t.Fatalf("authenticated ACK did not complete pairing: status=%d paired=%t updatePending=%t", ack.response.StatusCode, m.Status().Paired, m.Status().UpdatePending)
	}
	configuration := directTLSWireExchange(t, server.Listener.Addr().String(), authenticated, http.MethodGet, "/v2/direct/config", nil)
	if configuration.response.StatusCode != http.StatusOK || string(configuration.body) != "{\"update\":\"\"}\n" {
		t.Fatalf("paired phone could not retrieve its acknowledged configuration: status=%d", configuration.response.StatusCode)
	}
	t.Logf("real TLS 1.3 HTTP/1.1 enrollment: decoded=%d wire=%d Content-Length=%d Transfer-Encoding=%v; same-key retry preserved identity; authenticated ACK paired; config current", len(first.body), len(first.wire), first.response.ContentLength, first.response.TransferEncoding)

	// Optional interoperability evidence contains only disposable public test
	// certificates, IDs, and HTTP responses. No private keys or user state.
	if directory := os.Getenv("MOBILE_EGRESS_HTTP_WIRE_EVIDENCE"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for name, raw := range map[string][]byte{
			"enrollment.response.http":       first.wire,
			"enrollment.body.json":           first.body,
			"enrollment-retry.response.http": retry.wire,
			"ack.response.http":              ack.wire,
			"config.response.http":           configuration.wire,
		} {
			if err := os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type directTLSWireResponse struct {
	response *http.Response
	wire     []byte
	body     []byte
}

func directTLSWireExchange(t *testing.T, address string, config *tls.Config, method, path string, value any) directTLSWireResponse {
	t.Helper()
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", address, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "http/1.1" || len(state.VerifiedChains) == 0 {
		t.Fatal("exchange did not negotiate authenticated TLS 1.3 HTTP/1.1")
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Match the phone's one-request connection, including Connection: close.
	request := []byte(fmt.Sprintf("%s %s HTTP/1.1\r\nHost: client.example:8443\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", method, path, len(body)))
	request = append(request, body...)
	if _, err := io.Copy(conn, bytes.NewReader(request)); err != nil {
		t.Fatal(err)
	}
	const maximumWireBytes = 512 << 10
	wire, err := io.ReadAll(io.LimitReader(conn, maximumWireBytes+1))
	if err != nil || len(wire) > maximumWireBytes {
		t.Fatalf("reading bounded HTTP response: bytes=%d err=%v", len(wire), err)
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return directTLSWireResponse{response: response, wire: wire, body: decoded}
}
