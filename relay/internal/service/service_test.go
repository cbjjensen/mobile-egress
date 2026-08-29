package service

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestInitializePersistsPrivateCAAndOwnerCapabilityAtomically(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	capability, err := Initialize(context.Background(), InitOptions{
		StateDir:   stateDir,
		PublicName: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}
	if len(capability) < 32 {
		t.Fatalf("Initialize() returned a short owner capability: %d characters", len(capability))
	}

	for _, name := range []string{"ca.crt", "ca.key", "relay.crt", "relay.key", "state.db"} {
		path := filepath.Join(stateDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("initialized state is missing %s: %v", name, err)
		}
		if info.Size() == 0 {
			t.Fatalf("initialized state file %s is empty", name)
		}
	}

	privateKeyInfo, err := os.Stat(filepath.Join(stateDir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && privateKeyInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("CA private key permissions are %o, want no group/other access", privateKeyInfo.Mode().Perm())
	}

	store, err := openStore(filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatalf("openStore() returned an error: %v", err)
	}
	defer store.Close()

	capabilities, err := store.capabilityCount(context.Background(), "owner")
	if err != nil {
		t.Fatalf("capabilityCount() returned an error: %v", err)
	}
	if capabilities != 1 {
		t.Fatalf("persisted owner capability count = %d, want 1", capabilities)
	}

	if _, err := Initialize(context.Background(), InitOptions{StateDir: stateDir, PublicName: "127.0.0.1"}); err == nil {
		t.Fatal("Initialize() overwrote an initialized state directory")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ca.crt")); err != nil {
		t.Fatalf("failed re-initialization damaged the original state: %v", err)
	}
}

func TestOpenRejectsMissingOrInvalidInitializedState(t *testing.T) {
	t.Parallel()

	if _, err := Open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Open() accepted a missing state directory")
	}

	invalidState := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(invalidState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidState, "ca.crt"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(invalidState); err == nil {
		t.Fatal("Open() accepted invalid initialized state")
	}

	missingDatabase := filepath.Join(t.TempDir(), "state")
	if _, err := Initialize(context.Background(), InitOptions{StateDir: missingDatabase, PublicName: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(missingDatabase, databaseFilename)); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(missingDatabase)
	if err == nil {
		_ = opened.Close()
		t.Fatal("Open() recreated and accepted a missing SQLite state file")
	}
}

func TestHealthzOverTLSReturnsOnlyRedactedAggregateReadiness(t *testing.T) {
	t.Parallel()

	relay, server := newTLSTestRelay(t)
	defer relay.Close()
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz returned an error: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", contentType)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("GET /healthz returned invalid JSON: %v", err)
	}
	wantKeys := []string{
		"activeStreams", "agentConnected", "byteCount", "connectedClients",
		"errorCounts", "readiness", "totalStreams",
	}
	gotKeys := make([]string, 0, len(health))
	for key := range health {
		gotKeys = append(gotKeys, key)
	}
	slicesSort(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("GET /healthz keys = %v, want exactly %v", gotKeys, wantKeys)
	}
	if health["readiness"] != true || health["agentConnected"] != false {
		t.Fatalf("GET /healthz readiness state = %#v", health)
	}
	if string(body) == "" || containsSensitiveMetricText(string(body)) {
		t.Fatalf("GET /healthz leaked forbidden metric detail: %s", body)
	}
}

func TestHealthzFailureStillReturnsOnlyRedactedAggregateFields(t *testing.T) {
	t.Parallel()

	relay, server := newTLSTestRelay(t)
	defer server.Close()
	defer relay.Close()
	if err := relay.store.Close(); err != nil {
		t.Fatal(err)
	}

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed readiness status = %d, want 503", response.StatusCode)
	}
	var health map[string]any
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatalf("failed readiness body is not aggregate JSON: %v", err)
	}
	wantKeys := []string{
		"activeStreams", "agentConnected", "byteCount", "connectedClients",
		"errorCounts", "readiness", "totalStreams",
	}
	gotKeys := make([]string, 0, len(health))
	for key := range health {
		gotKeys = append(gotKeys, key)
	}
	slicesSort(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) || health["readiness"] != false {
		t.Fatalf("failed readiness response = %#v, want only redacted aggregate fields", health)
	}
}

func newTLSTestRelay(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()

	stateDir := filepath.Join(t.TempDir(), "state")
	if _, err := Initialize(context.Background(), InitOptions{StateDir: stateDir, PublicName: "127.0.0.1"}); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}
	relay, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() returned an error: %v", err)
	}

	server := httptest.NewUnstartedServer(relay.Handler())
	server.TLS = relay.TLSConfig()
	server.StartTLS()

	caPEM, err := os.ReadFile(filepath.Join(stateDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to trust initialized CA")
	}
	server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	server.Client().Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = false

	return relay, server
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func containsSensitiveMetricText(body string) bool {
	for _, forbidden := range []string{"hostname", "destination", "payload", "certificate", "pairing", "code"} {
		if stringContainsFold(body, forbidden) {
			return true
		}
	}
	return false
}

func stringContainsFold(value, substring string) bool {
	if len(substring) == 0 {
		return true
	}
	for index := 0; index+len(substring) <= len(value); index++ {
		match := true
		for offset := range substring {
			left := value[index+offset]
			right := substring[offset]
			if left >= 'A' && left <= 'Z' {
				left += 'a' - 'A'
			}
			if left != right {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
