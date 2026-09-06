package service

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestOwnerHealthPairingReflectsEnrollmentAndRevocationOnly(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	key, csr := newDeviceCSR(t)
	status, owner := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", csr)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	ownerClient := fixture.authenticatedClient(t, key, owner.CertificatePEM)
	read := func(client *http.Client) map[string]any {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, fixture.server.URL+"/healthz", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Mobile-Egress-Health-Agent-Pairing", "1")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := read(ownerClient)["agentPaired"]; got != false {
		t.Fatalf("before pairing = %v", got)
	}
	legacyResponse, err := ownerClient.Get(fixture.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer legacyResponse.Body.Close()
	var legacy map[string]any
	if err := json.NewDecoder(legacyResponse.Body).Decode(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, exists := legacy["agentPaired"]; exists {
		t.Fatal("legacy Owner received unknown health field")
	}
	status, pairing := postPairing(t, ownerClient, fixture.server.URL, "agent")
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	if got := read(ownerClient)["agentPaired"]; got != false {
		t.Fatal("issuing QR reported pairing success")
	}
	agentKey, agentCSR := newDeviceCSR(t)
	status, agent := postEnrollment(t, fixture.server.Client(), fixture.server.URL, pairing.Code, "agent", agentCSR)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	if got := read(ownerClient); got["agentPaired"] != true || got["agentConnected"] != false {
		t.Fatalf("enrolled offline agent = %v", got)
	}
	for _, client := range []*http.Client{fixture.server.Client(), fixture.authenticatedClient(t, agentKey, agent.CertificatePEM)} {
		if _, exists := read(client)["agentPaired"]; exists {
			t.Fatal("pairing history exposed without active Owner")
		}
	}
	if status := postRevocation(t, ownerClient, fixture.server.URL, agent.Serial); status != http.StatusNoContent {
		t.Fatal(status)
	}
	if got := read(ownerClient)["agentPaired"]; got != false {
		t.Fatal("revoked agent reported paired")
	}
}
