package nodeservice

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

func TestBootstrapIsIdempotentAndReturnsOnlyPublicMaterial(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	first, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.CSRPEM == "" || first.ConfigurationPublicKey == "" || first != second {
		t.Fatalf("bootstrap responses = %#v/%#v", first, second)
	}
	requestBlock, _ := pem.Decode([]byte(first.CSRPEM))
	if requestBlock == nil {
		t.Fatal("bootstrap CSR is not PEM")
	}
	request, err := x509.ParseCertificateRequest(requestBlock.Bytes)
	if err != nil || request.CheckSignature() != nil {
		t.Fatalf("bootstrap CSR is invalid: %v", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private", "password", "username", "credential", "nonce", "ciphertext"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("bootstrap response exposed %q: %s", forbidden, encoded)
		}
	}
}

func TestApplySealedConfigurationRejectsReplayTamperAndWrongKey(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user-secret", "password-secret")
	plaintext, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), envelope); err != nil {
		t.Fatalf("Apply() returned an error: %v", err)
	}
	runtime, err := repository.Runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Identity.Role != "client" || runtime.Identity.RelayURL != config.RelayURL || runtime.Identity.Serial != config.Serial || runtime.Identity.PrivateKeyPEM == "" {
		t.Fatalf("runtime identity is incomplete: %#v", runtime.Identity)
	}
	if runtime.Username != config.SOCKSUsername || runtime.Password != config.SOCKSPassword || runtime.Port != 1080 {
		t.Fatalf("runtime SOCKS configuration = %#v", runtime)
	}
	if err := repository.Apply(context.Background(), envelope); err == nil {
		t.Fatal("Apply() accepted a replayed envelope")
	}

	tampered := envelope
	replacement := byte('A')
	if tampered.Ciphertext[0] == replacement {
		replacement = 'B'
	}
	tampered.Ciphertext = string(replacement) + tampered.Ciphertext[1:]
	if err := repository.Apply(context.Background(), tampered); err == nil {
		t.Fatal("Apply() accepted tampered ciphertext")
	}
	_, wrongPublicKey, err := sealedconfig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongEnvelope, err := sealedconfig.Seal(wrongPublicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), wrongEnvelope); err == nil {
		t.Fatal("Apply() accepted an envelope for another node")
	}
}

func TestEndpointUpdateRetainsNodeIdentityKey(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := signedNodeConfig(t, bootstrap.CSRPEM, "https://old.example.ts.net:8443", "node-user", "node-password")
	applyNodeConfig(t, repository, bootstrap.ConfigurationPublicKey, first)
	before, err := repository.Runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.RelayURL = "https://new.example.ts.net:8443"
	second.Generation = 2
	applyNodeConfig(t, repository, bootstrap.ConfigurationPublicKey, second)
	after, err := repository.Runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Identity.PrivateKeyPEM != after.Identity.PrivateKeyPEM || before.Identity.Serial != after.Identity.Serial || before.Username != after.Username || before.Password != after.Password {
		t.Fatal("endpoint update replaced node identity or SOCKS credentials")
	}
	if after.Identity.RelayURL != second.RelayURL {
		t.Fatalf("endpoint update relay URL = %q", after.Identity.RelayURL)
	}
}

func TestApplyRejectsAnOlderEnvelopeAfterANewerEndpointUpdate(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := signedNodeConfig(t, bootstrap.CSRPEM, "https://old.example.ts.net:8443", "node-user", "node-password")
	firstPlaintext, _ := json.Marshal(first)
	firstEnvelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, firstPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), firstEnvelope); err != nil {
		t.Fatal(err)
	}
	second := first
	second.RelayURL = "https://new.example.ts.net:8443"
	second.Generation = 2
	secondPlaintext, _ := json.Marshal(second)
	secondEnvelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, secondPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), secondEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), firstEnvelope); err == nil {
		t.Fatal("Apply() accepted envelope A again after applying envelope B")
	}
}

func TestApplyAcceptsAResealedCurrentConfigurationButRejectsChangedContent(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "node-user", "node-password")
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	first, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), second); err != nil {
		t.Fatalf("Apply() rejected an idempotent resealed retry: %v", err)
	}
	if err := repository.Apply(context.Background(), second); err == nil {
		t.Fatal("Apply() accepted an exact envelope replay")
	}

	changed := configuration
	changed.RelayURL = "https://changed.example.ts.net:8443"
	changedPlaintext, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedEnvelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, changedPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), changedEnvelope); err == nil {
		t.Fatal("Apply() accepted changed content at the current generation")
	}
}

func TestApplyAllowsUnlimitedFreshRetriesOfTheExactCurrentConfiguration(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "node-user", "node-password")
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 64; attempt++ {
		envelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.Apply(context.Background(), envelope); err != nil {
			t.Fatalf("Apply() retry %d failed: %v", attempt+1, err)
		}
	}
}

func TestRepositoryMigratesVersionOneConfigurationGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	bootstrap, err := repository.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "node-user", "node-password")
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(ctx, envelope); err != nil {
		t.Fatal(err)
	}

	raw, err := store.Get(ctx, stateKey)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["version"] = float64(1)
	legacyConfiguration := legacy["configuration"].(map[string]any)
	legacyConfiguration["generation"] = float64(0)
	legacyEnvelopes := legacy["currentConfigurationEnvelopes"].([]any)
	legacy["lastConfigurationEnvelope"] = legacyEnvelopes[0]
	delete(legacy, "currentConfigurationEnvelopes")
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, stateKey, encoded); err != nil {
		t.Fatal(err)
	}

	migratedRepository := NewRepository(store)
	if _, err := migratedRepository.Runtime(ctx); err != nil {
		t.Fatalf("Runtime() did not migrate version-one state: %v", err)
	}
	if err := migratedRepository.Apply(ctx, envelope); err == nil {
		t.Fatal("migrated state forgot the previously applied envelope fingerprint")
	}
	migratedRaw, err := store.Get(ctx, stateKey)
	if err != nil {
		t.Fatal(err)
	}
	var migrated map[string]any
	if err := json.Unmarshal(migratedRaw, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated["version"] != float64(2) || migrated["configuration"].(map[string]any)["generation"] != float64(1) {
		t.Fatalf("migrated state = %#v", migrated)
	}
}

func applyNodeConfig(t *testing.T, repository *Repository, publicKey string, config Configuration) {
	t.Helper()
	plaintext, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := sealedconfig.Seal(publicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Apply(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
}

func signedNodeConfig(t *testing.T, csrPEM, relayURL, username, password string) Configuration {
	t.Helper()
	csrBlock, _ := pem.Decode([]byte(csrPEM))
	if csrBlock == nil {
		t.Fatal("invalid CSR")
	}
	request, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "mobile-egress-client"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, ca, request.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certificatePEM := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), caPEM...)
	return Configuration{
		Version: 1, Generation: 1, RelayURL: relayURL, Role: "client", Serial: "2A",
		CertificatePEM: string(certificatePEM), CACertificatePEM: string(caPEM),
		SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080,
	}
}
