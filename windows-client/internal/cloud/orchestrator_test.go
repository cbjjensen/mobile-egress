package cloud

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/relayclient"
)

func TestInstallNodeUsesPublicBootstrapAndSealedConfigurationOnly(t *testing.T) {
	t.Parallel()

	bootstrapJSON, _ := json.Marshal(map[string]string{
		"csrPem":                 "-----BEGIN CERTIFICATE REQUEST-----\nPUBLIC-CSR\n-----END CERTIFICATE REQUEST-----\n",
		"configurationPublicKey": "uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0",
	})
	runner := &fakeCommandRunner{outputs: []string{string(bootstrapJSON), `{"configured":true}`}}
	issuer := &fakeIssuer{result: relayclient.ProvisionedIdentity{
		RelayURL: "https://bridge.tail123.ts.net:8443", Role: "client", Serial: "A1B2",
		CertificatePEM: "public-client-certificate", CACertificatePEM: "public-relay-ca",
	}}
	metadata := &memoryNodeStore{}
	orchestrator := NewOrchestrator(runner, issuer, metadata)
	node, err := orchestrator.Install(context.Background(), "i-0123456789abcdef0", NodeRelease{
		Version: "1.2.3", URL: "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SignerThumbprint: "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.InstanceID != "i-0123456789abcdef0" || node.ClientSerial != "A1B2" || node.ConfigurationPublicKey == "" || node.SOCKSUsername == "" || node.SOCKSPassword == "" || node.ServiceVersion != "1.2.3" {
		t.Fatalf("installed node metadata = %#v", node)
	}
	if issuer.csrPEM == "" || len(runner.scripts) != 2 || len(metadata.saves) != 2 || metadata.saves[0].Health != "configuring" || metadata.saved.InstanceID != node.InstanceID {
		t.Fatalf("orchestration calls = issuer %q, scripts %d, saved %#v", issuer.csrPEM, len(runner.scripts), metadata.saved)
	}
	joinedScripts := strings.Join(runner.scripts, "\n")
	for _, secret := range []string{node.SOCKSUsername, node.SOCKSPassword, issuer.result.CertificatePEM, issuer.result.CACertificatePEM} {
		if strings.Contains(joinedScripts, secret) {
			t.Fatalf("SSM command text exposed secret/public identity material %q", secret)
		}
	}
	if !strings.Contains(runner.scripts[0], "MobileEgressClient") || !strings.Contains(runner.scripts[0], "LocalSystem") || !strings.Contains(runner.scripts[0], "icacls.exe") {
		t.Fatalf("install script does not establish protected LocalSystem service: %s", runner.scripts[0])
	}
	if !strings.Contains(runner.scripts[1], "apply-config") || strings.Contains(runner.scripts[1], "socksPassword") || strings.Contains(runner.scripts[1], "certificatePem") {
		t.Fatalf("apply script is not ciphertext-only: %s", runner.scripts[1])
	}
	for _, forbidden := range []string{"New-EC2Instance", "AuthorizeSecurityGroupIngress", "New-EC2Address", "0.0.0.0/0"} {
		if strings.Contains(joinedScripts, forbidden) {
			t.Fatalf("SSM orchestration contains forbidden EC2 mutation %q", forbidden)
		}
	}
}

func TestInstallNodeRedactsRunnerErrors(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{err: sensitiveError("private-output-marker")}
	orchestrator := NewOrchestrator(runner, &fakeIssuer{}, &memoryNodeStore{})
	_, err := orchestrator.Install(context.Background(), "i-0123456789abcdef0", NodeRelease{
		Version: "1.2.3", URL: "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SignerThumbprint: "0123456789abcdef0123456789abcdef01234567",
	})
	if err == nil || strings.Contains(err.Error(), "private-output-marker") {
		t.Fatalf("Install() error was not redacted: %v", err)
	}
}

func TestEndpointUpdateSealsExistingIdentityAndCredentialsToNodeKey(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{outputs: []string{`{"configured":true}`}}
	store := &memoryNodeStore{}
	orchestrator := NewOrchestrator(runner, &fakeIssuer{}, store)
	node := ManagedNode{
		InstanceID: "i-0123456789abcdef0", ClientSerial: "A1", ConfigurationPublicKey: "uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0", ConfigurationGeneration: 1,
		ServiceVersion: "1.2.3", Health: "healthy", SOCKSUsername: "user-secret", SOCKSPassword: "password-secret", SOCKSPort: 1080,
		RelayURL: "https://old.tail123.ts.net:8443", CertificatePEM: "certificate-secret", CACertificatePEM: "ca-secret",
	}
	updated, err := orchestrator.UpdateEndpoint(context.Background(), node, "https://new.tail123.ts.net:8443")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RelayURL != "https://new.tail123.ts.net:8443" || store.saved.RelayURL != updated.RelayURL {
		t.Fatalf("updated node = %#v", updated)
	}
	if len(store.saves) != 2 || store.saves[0].RelayURL != updated.RelayURL || store.saves[0].ConfigurationGeneration != 2 || store.saves[0].Health != "configuring" {
		t.Fatalf("recoverable endpoint metadata was not saved before apply: %#v", store.saves)
	}
	if len(runner.scripts) != 1 || !strings.Contains(runner.scripts[0], "apply-config") {
		t.Fatalf("endpoint update scripts = %#v", runner.scripts)
	}
	for _, required := range []string{"Stop-Service -Name 'MobileEgressClient'", "WaitForStatus('Stopped'", "Start-Service -Name 'MobileEgressClient'", "WaitForStatus('Running'"} {
		if !strings.Contains(runner.scripts[0], required) {
			t.Fatalf("endpoint update does not restart the live Client after applying config; missing %q in %s", required, runner.scripts[0])
		}
	}
	if strings.Contains(runner.scripts[0], "sc.exe start") {
		t.Fatalf("endpoint update uses noisy sc.exe start instead of a controlled service restart: %s", runner.scripts[0])
	}
	for _, secret := range []string{node.SOCKSUsername, node.SOCKSPassword, node.CertificatePEM, node.CACertificatePEM} {
		if strings.Contains(runner.scripts[0], secret) {
			t.Fatalf("endpoint SSM update exposed %q", secret)
		}
	}
}

func TestRepairCompletesEndpointUpdateAfterTheRemoteApplySucceedsButFinalSaveFails(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{outputs: []string{`{"configured":true}`}}
	store := &memoryNodeStore{failAt: 2}
	orchestrator := NewOrchestrator(runner, &fakeIssuer{}, store)
	node := testManagedNode("i-0123456789abcdef0")
	node.ConfigurationPublicKey = "uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0"
	if _, err := orchestrator.UpdateEndpoint(context.Background(), node, "https://new.tail123.ts.net:8443"); err == nil {
		t.Fatal("UpdateEndpoint() succeeded despite the simulated metadata failure")
	}
	if store.saved.RelayURL != "https://new.tail123.ts.net:8443" || store.saved.ConfigurationGeneration != 2 || store.saved.Health != "configuring" {
		t.Fatalf("recoverable endpoint metadata = %#v", store.saved)
	}
	store.failAt = 0
	release := NodeRelease{
		Version: "1.2.4", URL: "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.4/mobile-egress-client.exe",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SignerThumbprint: "0123456789abcdef0123456789abcdef01234567",
	}
	repairRunner := &fakeCommandRunner{outputs: []string{`{"updated":true}`, `{"configured":true}`}}
	updated, err := NewOrchestrator(repairRunner, nil, store).Repair(context.Background(), store.saved, release)
	if err != nil {
		t.Fatalf("Repair() retry failed: %v", err)
	}
	if updated.ConfigurationGeneration != 2 || updated.RelayURL != "https://new.tail123.ts.net:8443" || len(repairRunner.scripts) != 2 {
		t.Fatalf("retry result = %#v, scripts = %d", updated, len(repairRunner.scripts))
	}
}

func TestRepairCompletesAnInstallAfterItsFinalMetadataSaveFails(t *testing.T) {
	t.Parallel()

	bootstrapJSON, _ := json.Marshal(map[string]string{
		"csrPem":                 "-----BEGIN CERTIFICATE REQUEST-----\nPUBLIC-CSR\n-----END CERTIFICATE REQUEST-----\n",
		"configurationPublicKey": "uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0",
	})
	release := NodeRelease{
		Version: "1.2.3", URL: "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SignerThumbprint: "0123456789abcdef0123456789abcdef01234567",
	}
	installRunner := &fakeCommandRunner{outputs: []string{string(bootstrapJSON), `{"configured":true}`}}
	issuer := &fakeIssuer{result: relayclient.ProvisionedIdentity{
		RelayURL: "https://bridge.tail123.ts.net:8443", Role: "client", Serial: "A1B2",
		CertificatePEM: "public-client-certificate", CACertificatePEM: "public-relay-ca",
	}}
	store := &memoryNodeStore{failAt: 2}
	if _, err := NewOrchestrator(installRunner, issuer, store).Install(context.Background(), "i-0123456789abcdef0", release); err == nil {
		t.Fatal("Install() succeeded despite the simulated final metadata failure")
	}
	if store.saved.Health != "configuring" || store.saved.ConfigurationGeneration != 1 {
		t.Fatalf("recoverable install metadata = %#v", store.saved)
	}

	store.failAt = 0
	repairRunner := &fakeCommandRunner{outputs: []string{`{"updated":true}`, `{"configured":true}`}}
	repaired, err := NewOrchestrator(repairRunner, nil, store).Repair(context.Background(), store.saved, release)
	if err != nil {
		t.Fatalf("Repair() could not finish the partial install: %v", err)
	}
	if repaired.Health != "installed" || repaired.ConfigurationGeneration != 1 {
		t.Fatalf("repaired node = %#v", repaired)
	}
}

func TestSignedNodeUpdateAndRepairNeverExposeConfiguration(t *testing.T) {
	t.Parallel()

	node := ManagedNode{
		InstanceID: "i-0123456789abcdef0", ClientSerial: "A1", ConfigurationPublicKey: "uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0", ConfigurationGeneration: 1,
		ServiceVersion: "1.2.3", Health: "healthy", SOCKSUsername: "user-secret", SOCKSPassword: "password-secret", SOCKSPort: 1080,
		RelayURL: "https://bridge.tail123.ts.net:8443", CertificatePEM: "certificate-secret", CACertificatePEM: "ca-secret",
	}
	release := NodeRelease{
		Version: "1.2.4", URL: "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.4/mobile-egress-client.exe",
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SignerThumbprint: "0123456789abcdef0123456789abcdef01234567",
	}
	runner := &fakeCommandRunner{outputs: []string{`{"updated":true}`, `{"configured":true}`}}
	store := &memoryNodeStore{}
	orchestrator := NewOrchestrator(runner, &fakeIssuer{}, store)
	updated, err := orchestrator.Repair(context.Background(), node, release)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ServiceVersion != release.Version || len(runner.scripts) != 2 {
		t.Fatalf("Repair() = %#v, scripts %d", updated, len(runner.scripts))
	}
	if !strings.Contains(runner.scripts[0], "Get-AuthenticodeSignature") || !strings.Contains(runner.scripts[0], "Stop-Service") {
		t.Fatalf("update did not verify and safely replace service: %s", runner.scripts[0])
	}
	if !strings.Contains(runner.scripts[0], "SignerCertificate.Thumbprint") || !strings.Contains(strings.ToLower(runner.scripts[0]), release.SignerThumbprint) {
		t.Fatalf("update did not pin the exact Client signer thumbprint: %s", runner.scripts[0])
	}
	for _, secret := range []string{node.SOCKSUsername, node.SOCKSPassword, node.CertificatePEM, node.CACertificatePEM} {
		if strings.Contains(strings.Join(runner.scripts, "\n"), secret) {
			t.Fatalf("repair SSM input exposed %q", secret)
		}
	}
}

type fakeCommandRunner struct {
	outputs []string
	scripts []string
	err     error
}

func (runner *fakeCommandRunner) RunPowerShell(_ context.Context, _ string, script string) (string, error) {
	runner.scripts = append(runner.scripts, script)
	if runner.err != nil {
		return "", runner.err
	}
	output := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	return output, nil
}

type fakeIssuer struct {
	csrPEM string
	result relayclient.ProvisionedIdentity
}

func (issuer *fakeIssuer) ProvisionClient(_ context.Context, csrPEM string) (relayclient.ProvisionedIdentity, error) {
	issuer.csrPEM = csrPEM
	return issuer.result, nil
}

type memoryNodeStore struct {
	saved  ManagedNode
	saves  []ManagedNode
	calls  int
	failAt int
}

func (store *memoryNodeStore) SaveNode(_ context.Context, node ManagedNode) error {
	store.calls++
	if store.failAt == store.calls {
		return sensitiveError("simulated encrypted metadata write failure")
	}
	store.saved = node
	store.saves = append(store.saves, node)
	return nil
}

type sensitiveError string

func (err sensitiveError) Error() string { return string(err) }
