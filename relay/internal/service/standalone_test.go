package service

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
)

func restartStandaloneFixture(t *testing.T, fixture *relayFixture) {
	t.Helper()
	fixture.server.Close()
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	fixture.service, err = Open(fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture.server = httptest.NewUnstartedServer(fixture.service.Handler())
	fixture.server.TLS = fixture.service.TLSConfig()
	fixture.server.StartTLS()
	roots := x509.NewCertPool()
	roots.AddCert(fixture.service.caCert)
	transport := fixture.server.Client().Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = roots
	transport.TLSClientConfig.InsecureSkipVerify = false
}

func standalonePost(t *testing.T, client *http.Client, origin, path string, input, output any) int {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(origin+"/v1/client-enrollments"+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if output != nil && response.StatusCode < 300 {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode
}

func TestStandaloneBindingIssuanceRetryDeliveryReceiptAndLiveStatus(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, _ := enrollDevices(t, fixture)
	post := func(client *http.Client, path string, input, output any, want int) {
		t.Helper()
		if got := standalonePost(t, client, fixture.server.URL, path, input, output); got != want {
			t.Fatalf("%s status=%d want=%d", path, got, want)
		}
	}
	var invite struct {
		ID, Capability string
		ExpiresAt      time.Time
	}
	post(owner.client, "", map[string]string{"nodeId": "paired-one", "displayName": "My Mac"}, &invite, 201)
	if invite.ID == "" || len(invite.Capability) < 32 || time.Until(invite.ExpiresAt) > 10*time.Minute {
		t.Fatal("invalid invitation")
	}
	var retryInvite struct {
		ID, Capability string
		ExpiresAt      time.Time
	}
	restartStandaloneFixture(t, fixture)
	post(owner.client, "", map[string]string{"nodeId": "paired-one", "displayName": "My Mac"}, &retryInvite, 201)
	if retryInvite.ID != invite.ID || retryInvite.Capability != invite.Capability || !retryInvite.ExpiresAt.Equal(invite.ExpiresAt) {
		t.Fatal("lost invitation response retry changed reservation")
	}
	key, csr := newDeviceCSR(t)
	encryptionKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := map[string]string{"csrPem": csr, "configurationPublicKey": base64.RawURLEncoding.EncodeToString(encryptionKey.PublicKey().Bytes()), "platform": "macos", "architecture": "arm64", "serviceVersion": "0.1.0"}
	request := map[string]any{"id": invite.ID, "capability": invite.Capability, "bootstrap": bootstrap}
	post(fixture.server.Client(), "/bootstrap", request, nil, 200)
	restartStandaloneFixture(t, fixture)
	post(fixture.server.Client(), "/bootstrap", request, nil, 200)
	competing := map[string]string{}
	for k, v := range bootstrap {
		competing[k] = v
	}
	_, competing["csrPem"] = newDeviceCSR(t)
	post(fixture.server.Client(), "/bootstrap", map[string]any{"id": invite.ID, "capability": invite.Capability, "bootstrap": competing}, nil, 409)
	var issued struct{ ClientSerial, CertificatePEM, State string }
	post(owner.client, "/approve", map[string]string{"id": invite.ID}, &issued, 200)
	restartStandaloneFixture(t, fixture)
	var repeated struct{ ClientSerial, CertificatePEM, State string }
	post(owner.client, "/approve", map[string]string{"id": invite.ID}, &repeated, 200)
	if issued.ClientSerial == "" || issued.ClientSerial != repeated.ClientSerial || issued.CertificatePEM != repeated.CertificatePEM {
		t.Fatal("issuance retry changed identity")
	}
	count, err := fixture.service.store.activeIdentityCount(context.Background(), "client")
	if err != nil || count != 1 {
		t.Fatalf("client identities=%d: %v", count, err)
	}
	envelope := map[string]any{"version": 1, "ephemeralPublicKey": bootstrap["configurationPublicKey"], "nonce": base64.RawURLEncoding.EncodeToString(make([]byte, 12)), "ciphertext": base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	post(owner.client, "/configuration", map[string]any{"id": invite.ID, "generation": uint64(1), "envelope": envelope}, nil, 204)
	restartStandaloneFixture(t, fixture)
	post(owner.client, "/configuration", map[string]any{"id": invite.ID, "generation": uint64(1), "envelope": envelope}, nil, 204)
	var delivered struct {
		State         string
		Configuration struct {
			Generation uint64
			Envelope   json.RawMessage
		}
	}
	post(fixture.server.Client(), "/poll", request, &delivered, 200)
	if delivered.State != "delivered" || delivered.Configuration.Generation != 1 || len(delivered.Configuration.Envelope) == 0 {
		t.Fatal("configuration not delivered")
	}
	client := fixture.authenticatedClient(t, key, issued.CertificatePEM)
	receipt := map[string]any{"id": invite.ID, "generation": uint64(1), "serviceVersion": "0.1.0"}
	post(owner.client, "/acknowledge", receipt, nil, 403)
	post(client, "/acknowledge", receipt, nil, 204)
	restartStandaloneFixture(t, fixture)
	post(client, "/acknowledge", receipt, nil, 204)
	receipt["generation"] = uint64(4)
	receipt["serviceVersion"] = "0.2.0"
	post(client, "/acknowledge", receipt, nil, 204)
	var statuses []struct {
		NodeID, ClientSerial, ServiceVersion string
		Connected                            bool
		AppliedGeneration                    uint64
	}
	post(owner.client, "/statuses", struct{}{}, &statuses, 200)
	if len(statuses) != 1 || statuses[0].Connected || statuses[0].AppliedGeneration != 4 || statuses[0].ServiceVersion != "0.2.0" {
		t.Fatalf("bad offline receipt status %#v", statuses)
	}
	connection := mustDialSession(t, fixture, client)
	defer connection.Close()
	post(owner.client, "/statuses", struct{}{}, &statuses, 200)
	if !statuses[0].Connected {
		t.Fatal("connected session reported offline")
	}
	expireStandaloneInvitation(t, fixture, invite.ID)
	// Completed enrollments have mTLS authority and no longer need capability recovery.
	post(fixture.server.Client(), "/poll", request, nil, 401)
	post(owner.client, "/cancel", map[string]string{"id": invite.ID}, nil, 204)
	_, revoked, err := fixture.service.store.identityStatus(context.Background(), issued.ClientSerial)
	if err != nil || !revoked {
		t.Fatal("cancel did not revoke issued identity")
	}
	post(fixture.server.Client(), "/poll", request, nil, 401)
	post(client, "/acknowledge", receipt, nil, 401)
}

func TestStandaloneReservationsShareAdmissionWithLegacyProvisioning(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, _ := enrollDevices(t, fixture)
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		var invitation struct{ ID string }
		if got := standalonePost(t, owner.client, fixture.server.URL, "", map[string]string{"nodeId": fmt.Sprintf("paired-%d", i), "displayName": "PC"}, &invitation); got != 201 {
			t.Fatalf("invite %d status %d", i, got)
		}
		ids = append(ids, invitation.ID)
	}
	if got := standalonePost(t, owner.client, fixture.server.URL, "", map[string]string{"nodeId": "overflow", "displayName": "PC"}, nil); got != 409 {
		t.Fatalf("eleventh invitation status %d", got)
	}
	_, csr := newDeviceCSR(t)
	body, _ := json.Marshal(map[string]string{"csrPem": csr})
	response, err := owner.client.Post(fixture.server.URL+"/v1/clients", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatalf("legacy bypassed reservation limit: %d", response.StatusCode)
	}
	if got := standalonePost(t, owner.client, fixture.server.URL, "/cancel", map[string]string{"id": ids[0]}, nil); got != 204 {
		t.Fatal(got)
	}
	response, err = owner.client.Post(fixture.server.URL+"/v1/clients", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("cancellation did not release slot: %d", response.StatusCode)
	}
}

func TestStandaloneExpiredAndWrongCapabilitiesCannotClaimOrApprove(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, _ := enrollDevices(t, fixture)
	var invitation clientcontrol.Invitation
	if status := standalonePost(t, owner.client, fixture.server.URL, "", map[string]string{"nodeId": "expiry", "displayName": "PC"}, &invitation); status != 201 {
		t.Fatal(status)
	}
	_, csr := newDeviceCSR(t)
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := clientcontrol.Bootstrap{CSRPEM: csr, ConfigurationPublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Platform: "windows", Architecture: "amd64", ServiceVersion: "1.0.0"}
	request := map[string]any{"id": invitation.ID, "capability": "wrong", "bootstrap": bootstrap}
	if status := standalonePost(t, fixture.server.Client(), fixture.server.URL, "/bootstrap", request, nil); status != 401 {
		t.Fatalf("wrong capability status %d", status)
	}
	request["capability"] = invitation.Capability
	if status := standalonePost(t, fixture.server.Client(), fixture.server.URL, "/bootstrap", request, nil); status != 200 {
		t.Fatal(status)
	}
	_, err = fixture.service.store.mutateClientEnrollment(context.Background(), invitation.ID, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		value.ExpiresAt = time.Now().Add(-time.Second)
		_, err := tx.Exec(`UPDATE client_enrollments SET expires_at = ? WHERE id = ?`, value.ExpiresAt.Unix(), value.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := standalonePost(t, fixture.server.Client(), fixture.server.URL, "/poll", request, nil); status != 401 {
		t.Fatalf("expired polling status %d", status)
	}
	if status := standalonePost(t, owner.client, fixture.server.URL, "/approve", map[string]string{"id": invitation.ID}, nil); status != 401 {
		t.Fatalf("expired approval status %d", status)
	}
	tx, err := fixture.service.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	count, err := countClientAdmissions(context.Background(), tx, time.Now())
	tx.Rollback()
	if err != nil || count != 0 {
		t.Fatalf("expired reservation still consumes slot: %d %v", count, err)
	}
}

func expireStandaloneInvitation(t *testing.T, fixture *relayFixture, id string) {
	t.Helper()
	_, err := fixture.service.store.mutateClientEnrollment(context.Background(), id, func(tx *sql.Tx, value *clientcontrol.Enrollment, _ []byte) error {
		value.ExpiresAt = time.Now().Add(-time.Minute)
		_, err := tx.Exec(`UPDATE client_enrollments SET expires_at = ? WHERE id = ?`, value.ExpiresAt.Unix(), value.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneExpiredApprovedHandoffPollRequiresExactActiveBinding(t *testing.T) {
	for _, stage := range []string{"approved", "delivered"} {
		for _, terminal := range []string{"canceled", "revoked"} {
			t.Run(stage+"/"+terminal, func(t *testing.T) {
				fixture := newRelayFixture(t)
				defer fixture.Close()
				owner, _ := enrollDevices(t, fixture)
				post := func(client *http.Client, path string, input, output any, want int) {
					t.Helper()
					if got := standalonePost(t, client, fixture.server.URL, path, input, output); got != want {
						t.Fatalf("%s status=%d want=%d", path, got, want)
					}
				}
				var invitation clientcontrol.Invitation
				post(owner.client, "", map[string]string{"nodeId": "late-pair", "displayName": "PC"}, &invitation, 201)
				_, csr := newDeviceCSR(t)
				key, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				bootstrap := clientcontrol.Bootstrap{CSRPEM: csr, ConfigurationPublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Platform: "windows", Architecture: "amd64", ServiceVersion: "1.0.0"}
				request := clientBootstrapRequest{ID: invitation.ID, Capability: invitation.Capability, Bootstrap: bootstrap}
				post(fixture.server.Client(), "/bootstrap", request, nil, 200)
				var approved clientcontrol.Enrollment
				post(owner.client, "/approve", clientIDRequest{ID: invitation.ID}, &approved, 200)
				envelope, _ := json.Marshal(map[string]any{"version": 1, "ephemeralPublicKey": bootstrap.ConfigurationPublicKey, "nonce": base64.RawURLEncoding.EncodeToString(make([]byte, 12)), "ciphertext": base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
				if stage == "delivered" {
					post(owner.client, "/configuration", clientConfigurationRequest{ID: invitation.ID, Generation: 1, Envelope: envelope}, nil, 204)
				}
				expireStandaloneInvitation(t, fixture, invitation.ID)
				restartStandaloneFixture(t, fixture)
				var recovered clientcontrol.Enrollment
				post(fixture.server.Client(), "/poll", request, &recovered, 200)
				if recovered.ClientSerial != approved.ClientSerial || recovered.CertificatePEM != approved.CertificatePEM || recovered.State != stage {
					t.Fatal("late recovery changed approved identity")
				}
				if stage == "delivered" && (recovered.Configuration == nil || recovered.Configuration.Generation != 1 || !bytes.Equal(recovered.Configuration.Envelope, envelope)) {
					t.Fatal("late recovery changed sealed envelope")
				}
				post(fixture.server.Client(), "/bootstrap", request, nil, 401)
				wrong := request
				wrong.Capability = "wrong-capability"
				post(fixture.server.Client(), "/poll", wrong, nil, 401)
				wrong = request
				_, wrong.Bootstrap.CSRPEM = newDeviceCSR(t)
				post(fixture.server.Client(), "/poll", wrong, nil, 401)
				wrong = request
				other, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				wrong.Bootstrap.ConfigurationPublicKey = base64.RawURLEncoding.EncodeToString(other.PublicKey().Bytes())
				post(fixture.server.Client(), "/poll", wrong, nil, 401)
				if terminal == "canceled" {
					post(owner.client, "/cancel", clientIDRequest{ID: invitation.ID}, nil, 204)
				} else if status := postRevocation(t, owner.client, fixture.server.URL, approved.ClientSerial); status != 204 {
					t.Fatal(status)
				}
				post(fixture.server.Client(), "/poll", request, nil, 401)
			})
		}
	}
}

func TestStandaloneExpiredUnapprovedInvitationsCannotPollOrRedeem(t *testing.T) {
	for _, stage := range []string{"invited", "submitted"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			owner, _ := enrollDevices(t, fixture)
			var invitation clientcontrol.Invitation
			if status := standalonePost(t, owner.client, fixture.server.URL, "", map[string]string{"nodeId": "not-approved", "displayName": "PC"}, &invitation); status != 201 {
				t.Fatal(status)
			}
			_, csr := newDeviceCSR(t)
			key, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			request := clientBootstrapRequest{ID: invitation.ID, Capability: invitation.Capability, Bootstrap: clientcontrol.Bootstrap{CSRPEM: csr, ConfigurationPublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Platform: "windows", Architecture: "amd64", ServiceVersion: "1.0.0"}}
			if stage == "submitted" {
				if status := standalonePost(t, fixture.server.Client(), fixture.server.URL, "/bootstrap", request, nil); status != 200 {
					t.Fatal(status)
				}
			}
			expireStandaloneInvitation(t, fixture, invitation.ID)
			for _, path := range []string{"/poll", "/bootstrap"} {
				if status := standalonePost(t, fixture.server.Client(), fixture.server.URL, path, request, nil); status != 401 {
					t.Fatalf("%s expired %s status=%d", stage, path, status)
				}
			}
			if status := standalonePost(t, owner.client, fixture.server.URL, "/approve", clientIDRequest{ID: invitation.ID}, nil); status != 401 {
				t.Fatalf("expired approval status=%d", status)
			}
			count, err := fixture.service.store.activeIdentityCount(context.Background(), "client")
			if err != nil || count != 0 {
				t.Fatalf("expired invitation issued identity: %d %v", count, err)
			}
		})
	}
}

func TestStandaloneConcurrentLegacyAndPairedAdmissionNeverExceedsTen(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, _ := enrollDevices(t, fixture)
	_, csr := newDeviceCSR(t)
	type outcome struct {
		status int
		err    error
	}
	results := make(chan outcome, 30)
	var workers sync.WaitGroup
	for i := 0; i < 30; i++ {
		path := "/v1/clients"
		body, _ := json.Marshal(map[string]string{"csrPem": csr})
		client := owner.client
		if i%3 == 0 {
			path = "/v1/client-enrollments"
			body, _ = json.Marshal(map[string]string{"nodeId": fmt.Sprintf("race-%d", i), "displayName": "PC"})
		}
		if i%3 == 1 {
			code, hash, err := newCapability()
			if err != nil {
				t.Fatal(err)
			}
			if err = fixture.service.store.insertCapability(context.Background(), hash, "client", time.Now(), time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			path = "/v1/enroll"
			body, _ = json.Marshal(map[string]string{"code": code, "role": "client", "csrPem": csr})
			client = fixture.server.Client()
		}
		workers.Add(1)
		go func(path string, body []byte, client *http.Client) {
			defer workers.Done()
			response, err := client.Post(fixture.server.URL+path, "application/json", bytes.NewReader(body))
			if err != nil {
				results <- outcome{err: err}
				return
			}
			response.Body.Close()
			results <- outcome{status: response.StatusCode}
		}(path, body, client)
	}
	workers.Wait()
	close(results)
	created := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.status {
		case 201:
			created++
		case 409:
		default:
			t.Fatalf("admission HTTP %d", result.status)
		}
	}
	if created != 10 {
		t.Fatalf("admitted %d Clients; want 10", created)
	}
	tx, err := fixture.service.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	count, err := countClientAdmissions(context.Background(), tx, time.Now())
	tx.Rollback()
	if err != nil || count != 10 {
		t.Fatalf("combined slots %d %v", count, err)
	}
}

func TestStandaloneStatusRetainsOldActiveClientAmongExpiredInvitations(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := fixture.service.store.createIdentity(ctx, "ABC", "client", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		value := clientcontrol.Enrollment{ID: fmt.Sprintf("old-%d", i), NodeID: fmt.Sprintf("paired-%d", i), DisplayName: "PC", State: "invited", ExpiresAt: now.Add(-time.Minute)}
		if i == 0 {
			value.State = "acknowledged"
			value.ClientSerial = "ABC"
			value.AppliedGeneration = 7
			value.ServiceVersion = "1.2.0"
			value.ExpiresAt = now.Add(-24 * time.Hour)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fixture.service.store.db.Exec(`INSERT INTO client_enrollments(id,node_id,capability_hash,expires_at,client_serial,state,record) VALUES(?,?,?,?,?,?,?)`, value.ID, value.NodeID, make([]byte, 32), value.ExpiresAt.Unix(), value.ClientSerial, value.State, raw); err != nil {
			t.Fatal(err)
		}
	}
	statuses, err := fixture.service.store.clientStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].NodeID != "paired-0" || statuses[0].AppliedGeneration != 7 {
		t.Fatalf("old active metadata hidden by expired invites: %#v", statuses)
	}
}

func TestStandaloneSchemaRejectsWeakenedCapabilityBinding(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	if _, err := fixture.service.store.db.Exec(`DROP TABLE client_enrollments`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.store.db.Exec(strings.Replace(standaloneEnrollmentSchema, "CHECK(length(capability_hash) = 32)", "", 1)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.store.validSchema(context.Background()); err == nil {
		t.Fatal("weakened enrollment table accepted")
	}
}
