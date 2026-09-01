package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
)

func TestAdminRotatePreservesAuthorityAndCommitsURLWithReplay(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	setupKey := adminReplayKey("62626262626262626262626262626262", relayadmin.OperationSetup, "endpoint-setup")
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, setupKey, AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if _, err := state.database.db.Exec(`INSERT INTO identities(serial, role, created_at, last_seen_at) VALUES
        ('A1', 'agent', 11, 12), ('C1', 'client', 21, 22)`); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, err := state.database.db.Exec(`INSERT INTO settings(key, value) VALUES ('unrelated', 'preserve-me')`); err != nil {
		state.Close()
		t.Fatal(err)
	}

	caCertificateBefore := readAdminStateFile(t, stateDir, caCertFilename)
	caKeyBefore := readAdminStateFile(t, stateDir, caKeyFilename)
	relayCertificateBefore := readAdminStateFile(t, stateDir, relayCertFilename)
	relayKeyBefore := readAdminStateFile(t, stateDir, relayKeyFilename)
	identitiesBefore := adminIdentityRows(t, state.database.db)
	settingsBefore := adminSettings(t, state.database.db)

	rotateKey := adminReplayKey("63636363636363636363636363636363", relayadmin.OperationRotate, "endpoint-rotate")
	response, result := executeAdminRotate(t, state, rotateKey, RotateEndpointOptions{
		StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
	})
	if result.PublicURL != "https://new.example.ts.net:8443" || result.Serial == "" {
		t.Fatalf("RotateEndpoint() result = %#v", result)
	}

	if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificateBefore) {
		t.Fatal("admin rotation changed the relay CA certificate")
	}
	if got := readAdminStateFile(t, stateDir, caKeyFilename); !bytes.Equal(got, caKeyBefore) {
		t.Fatal("admin rotation changed the relay CA private key")
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); bytes.Equal(got, relayCertificateBefore) {
		t.Fatal("admin rotation reused the old relay certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); bytes.Equal(got, relayKeyBefore) {
		t.Fatal("admin rotation reused the old relay private key")
	}
	if got := adminIdentityRows(t, state.database.db); !reflect.DeepEqual(got, identitiesBefore) {
		t.Fatalf("identity rows changed:\n before=%#v\n after=%#v", identitiesBefore, got)
	}
	wantSettings := make(map[string]string, len(settingsBefore))
	for key, value := range settingsBefore {
		wantSettings[key] = value
	}
	wantSettings["relay_url"] = result.PublicURL
	if got := adminSettings(t, state.database.db); !reflect.DeepEqual(got, wantSettings) {
		t.Fatalf("settings after rotation = %#v, want %#v", got, wantSettings)
	}

	pair, err := tls.LoadX509KeyPair(filepath.Join(stateDir, relayCertFilename), filepath.Join(stateDir, relayKeyFilename))
	if err != nil || len(pair.Certificate) == 0 {
		t.Fatalf("rotated TLS pair = %#v/%v", pair, err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.ToUpper(leaf.SerialNumber.Text(16)) != result.Serial {
		t.Fatalf("rotated certificate serial = %s, want %s", strings.ToUpper(leaf.SerialNumber.Text(16)), result.Serial)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertificateBefore) {
		t.Fatal("parse relay CA")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, DNSName: "new.example.ts.net", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("rotated relay certificate verification error = %v", err)
	}
	assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err := reopened.ReplayStore().Reserve(context.Background(), rotateKey)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("reopened rotate replay = (%#v, %v), want exact cached response", cached, err)
	}
}

func TestAdminRotatePrecommitFailureRestoresOldEndpoint(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("64646464646464646464646464646464", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	injected := errors.New("injected precommit failure")
	state.endpointFault = func(point adminEndpointFaultPoint) error {
		if point == adminEndpointBeforeCommit {
			return injected
		}
		return nil
	}
	key := adminReplayKey("65656565656565656565656565656565", relayadmin.OperationRotate, "precommit")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		})
		if rotateErr != nil {
			return nil, rotateErr
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("precommit failure did not restore the old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("precommit failure did not restore the old key")
	}
	if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://old.example.ts.net:8443" {
		t.Fatalf("relay_url = %q, want old endpoint", got)
	}
	assertNoAdminEndpointArtifacts(t, stateDir, key.RequestID)
}

func TestAdminRotateMismatchedTypedResponseRollsBack(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("68686868686868686868686868686868", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	key := adminReplayKey("69696969696969696969696969696969", relayadmin.OperationRotate, "mismatched-response")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if _, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		}); rotateErr != nil {
			return nil, rotateErr
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: "https://attacker.example.ts.net:8443", Serial: "DEADBEEF",
		})
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("mismatched response did not restore old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("mismatched response did not restore old key")
	}
	if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://old.example.ts.net:8443" {
		t.Fatalf("relay_url = %q, want old URL", got)
	}
	assertNoAdminEndpointArtifacts(t, stateDir, key.RequestID)
}

func TestAdminRotateDetectsAuthorityReplacementBeforeCommit(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	replacementCA, replacementKey, _, _, err := generateCertificateState("other.example.ts.net", state.now())
	if err != nil {
		t.Fatal(err)
	}
	state.endpointFault = func(point adminEndpointFaultPoint) error {
		if point == adminEndpointBeforeCommit {
			if err := os.WriteFile(filepath.Join(stateDir, caCertFilename), replacementCA, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(stateDir, caKeyFilename), replacementKey, 0o600); err != nil {
				return err
			}
		}
		return nil
	}
	key := adminReplayKey("6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b", relayadmin.OperationRotate, "authority-replacement")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		})
		if rotateErr != nil {
			return nil, rotateErr
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("authority replacement did not roll back relay certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("authority replacement did not roll back relay key")
	}
	if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://old.example.ts.net:8443" {
		t.Fatalf("relay_url = %q, want old URL", got)
	}
	if snapshot, err := state.Snapshot(context.Background()); err != nil || snapshot.Class != AdminStateIncompatible {
		t.Fatalf("Snapshot() = (%#v, %v), want degraded", snapshot, err)
	}
}

func TestAdminRotatePostcommitFailureKeepsCompletedNewEndpoint(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	finished := 0
	state, err := OpenAdminState(AdminStateOptions{
		StateDir: stateDir,
		MutationFinished: func(relayadmin.ReplayKey) {
			finished++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("66666666666666666666666666666666", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	state.endpointFault = func(point adminEndpointFaultPoint) error {
		if point == adminEndpointAfterCommit {
			return errors.New("injected postcommit failure")
		}
		return nil
	}
	key := adminReplayKey("67676767676767676767676767676767", relayadmin.OperationRotate, "postcommit")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	var exactResponse []byte
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		})
		if rotateErr != nil {
			return nil, rotateErr
		}
		exactResponse, rotateErr = relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
		return exactResponse, rotateErr
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if finished != 2 { // setup and rotate each notify exactly once
		t.Fatalf("MutationFinished calls = %d, want 2", finished)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); bytes.Equal(got, oldCertificate) {
		t.Fatal("postcommit failure restored the old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); bytes.Equal(got, oldKey) {
		t.Fatal("postcommit failure restored the old key")
	}
	if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://new.example.ts.net:8443" {
		t.Fatalf("relay_url = %q, want committed new endpoint", got)
	}
	if snapshot, err := state.Snapshot(context.Background()); err != nil || snapshot.Class != AdminStateIncompatible {
		t.Fatalf("Snapshot() = (%#v, %v), want degraded", snapshot, err)
	}
	paths := adminEndpointArtifactPaths(stateDir, key.RequestID)
	if got := readAdminStateFile(t, stateDir, filepath.Base(paths.certificateOld)); !bytes.Equal(got, oldCertificate) {
		t.Fatal("postcommit failure did not preserve the old certificate artifact")
	}
	if got := readAdminStateFile(t, stateDir, filepath.Base(paths.keyOld)); !bytes.Equal(got, oldKey) {
		t.Fatal("postcommit failure did not preserve the old key artifact")
	}
	cached, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, exactResponse) {
		t.Fatalf("same-ID retry = (%#v, %v), want exact cached response", cached, err)
	}
}

func TestRotationRecoveryRestoresSameHostnameAtEveryUnfinishedBoundary(t *testing.T) {
	points := []adminEndpointFaultPoint{
		adminEndpointBeforeNewCertificateWrite,
		adminEndpointAfterNewCertificateSync,
		adminEndpointAfterNewKeySync,
		adminEndpointAfterOldCertificateRename,
		adminEndpointAfterOldKeyRename,
		adminEndpointAfterNewCertificateRename,
		adminEndpointAfterNewKeyRename,
	}
	for index, point := range points {
		t.Run(string(point), func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey(fmt.Sprintf("%032x", 100+index), relayadmin.OperationSetup, string(point)+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			caCertificate := readAdminStateFile(t, stateDir, caCertFilename)
			caKey := readAdminStateFile(t, stateDir, caKeyFilename)
			identities := adminIdentityRows(t, state.database.db)
			settings := adminSettings(t, state.database.db)

			rotateKey := adminReplayKey(fmt.Sprintf("%032x", 200+index), relayadmin.OperationRotate, string(point))
			leaveUnfinishedAdminRotation(t, state, rotateKey, point, RotateEndpointOptions{
				StateDir: stateDir, PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443",
			})
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			beforeRepair, err := reopened.Snapshot(context.Background())
			wantClass := AdminStateIncompatible
			if point == adminEndpointBeforeNewCertificateWrite {
				wantClass = AdminStateReady
			}
			if err != nil || beforeRepair.Class != wantClass {
				t.Fatalf("Snapshot() before repair = (%#v, %v), want class %v", beforeRepair, err, wantClass)
			}
			if point != adminEndpointBeforeNewCertificateWrite &&
				(reopened.database == nil || beforeRepair.AdministrativeOwnerUID != 501 || !beforeRepair.OwnerUIDBound) {
				t.Fatalf("artifact-bearing reopen lost bound degraded state: snapshot=%#v database=%p", beforeRepair, reopened.database)
			}
			repairKey := adminReplayKey(fmt.Sprintf("%032x", 300+index), relayadmin.OperationRepair, string(point)+"-repair")
			executeAdminRepair(t, reopened, repairKey)
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
				t.Fatal("repair did not restore the exact old certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
				t.Fatal("repair did not restore the exact old key")
			}
			if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificate) {
				t.Fatal("repair changed the CA certificate")
			}
			if got := readAdminStateFile(t, stateDir, caKeyFilename); !bytes.Equal(got, caKey) {
				t.Fatal("repair changed the CA key")
			}
			if got := adminIdentityRows(t, reopened.database.db); !reflect.DeepEqual(got, identities) {
				t.Fatalf("repair changed identities: got %#v, want %#v", got, identities)
			}
			if got := adminSettings(t, reopened.database.db); !reflect.DeepEqual(got, settings) {
				t.Fatalf("repair changed settings: got %#v, want %#v", got, settings)
			}
			assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRotationRecoveryAcceptsEveryReachableRollbackMicrostate(t *testing.T) {
	tests := []struct {
		name   string
		point  adminEndpointFaultPoint
		mutate func(*testing.T, string, adminEndpointPaths)
	}{
		{
			name:  "F4 after old certificate restore",
			point: adminEndpointAfterOldKeyRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				renameAdminTestPath(t, stateDir, paths.certificateOld, filepath.Join(stateDir, relayCertFilename))
			},
		},
		{
			name:  "F5 after new certificate removal",
			point: adminEndpointAfterNewCertificateRename,
			mutate: func(t *testing.T, stateDir string, _ adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
			},
		},
		{
			name:  "F5 after old certificate restore",
			point: adminEndpointAfterNewCertificateRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
				renameAdminTestPath(t, stateDir, paths.certificateOld, filepath.Join(stateDir, relayCertFilename))
			},
		},
		{
			name:  "F5 after old key restore",
			point: adminEndpointAfterNewCertificateRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
				renameAdminTestPath(t, stateDir, paths.certificateOld, filepath.Join(stateDir, relayCertFilename))
				renameAdminTestPath(t, stateDir, paths.keyOld, filepath.Join(stateDir, relayKeyFilename))
			},
		},
		{
			name:  "F6 after new certificate removal",
			point: adminEndpointAfterNewKeyRename,
			mutate: func(t *testing.T, stateDir string, _ adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
			},
		},
		{
			name:  "F6 after old certificate restore",
			point: adminEndpointAfterNewKeyRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
				renameAdminTestPath(t, stateDir, paths.certificateOld, filepath.Join(stateDir, relayCertFilename))
			},
		},
		{
			name:  "F6 after new key removal",
			point: adminEndpointAfterNewKeyRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayCertFilename))
				renameAdminTestPath(t, stateDir, paths.certificateOld, filepath.Join(stateDir, relayCertFilename))
				removeAdminTestPath(t, stateDir, filepath.Join(stateDir, relayKeyFilename))
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey(fmt.Sprintf("%032x", 1200+index), relayadmin.OperationSetup, test.name+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			rotateKey := adminReplayKey(fmt.Sprintf("%032x", 1300+index), relayadmin.OperationRotate, test.name+"-rotate")
			leaveUnfinishedAdminRotation(t, state, rotateKey, test.point, RotateEndpointOptions{
				StateDir: stateDir, PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443",
			})
			test.mutate(t, stateDir, adminEndpointArtifactPaths(stateDir, rotateKey.RequestID))
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible || snapshot.AdministrativeOwnerUID != 501 || !snapshot.OwnerUIDBound {
				t.Fatalf("Snapshot() = (%#v, %v), want bound incompatible before repair", snapshot, err)
			}
			executeAdminRepair(t, reopened, adminReplayKey(fmt.Sprintf("%032x", 1400+index), relayadmin.OperationRepair, test.name+"-repair"))
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
				t.Fatal("repair did not restore exact old certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
				t.Fatal("repair did not restore exact old key")
			}
			assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
		})
	}
}

func TestAdminEndpointRecoveryLayoutAllowlistIsExact(t *testing.T) {
	unfinished := map[adminEndpointLayout]bool{
		adminEndpointForwardF1:                     true,
		adminEndpointForwardF2:                     true,
		adminEndpointForwardF3:                     true,
		adminEndpointForwardF4:                     true,
		adminEndpointForwardF5:                     true,
		adminEndpointForwardF6:                     true,
		adminEndpointRollbackF4CertificateRestored: true,
		adminEndpointRollbackF5CertificateRemoved:  true,
		adminEndpointRollbackF5CertificateRestored: true,
		adminEndpointRollbackF5KeyRestored:         true,
		adminEndpointRollbackF6CertificateRemoved:  true,
		adminEndpointRollbackF6CertificateRestored: true,
		adminEndpointRollbackF6KeyRemoved:          true,
	}
	completed := map[adminEndpointLayout]bool{
		adminEndpointForwardF6:              true,
		adminEndpointCompletedOldKeyRemoved: true,
	}
	for layout := adminEndpointLayout(0); layout < 1<<6; layout++ {
		if got := validUnfinishedAdminEndpointLayout(layout); got != unfinished[layout] {
			t.Errorf("validUnfinishedAdminEndpointLayout(%06b) = %t, want %t", layout, got, unfinished[layout])
		}
		if got := validCompletedAdminEndpointLayout(layout); got != completed[layout] {
			t.Errorf("validCompletedAdminEndpointLayout(%06b) = %t, want %t", layout, got, completed[layout])
		}
	}
}

func TestRotationRecoveryRejectsTransitionImpossibleLossLayoutsWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		point  adminEndpointFaultPoint
		mutate func(*testing.T, string, adminEndpointPaths)
	}{
		{
			name:  "missing primary certificate with only old certificate and old primary key",
			point: adminEndpointAfterOldCertificateRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, paths.certificateNew)
				removeAdminTestPath(t, stateDir, paths.keyNew)
			},
		},
		{
			name:  "missing both primaries with only old pair",
			point: adminEndpointAfterOldKeyRename,
			mutate: func(t *testing.T, stateDir string, paths adminEndpointPaths) {
				removeAdminTestPath(t, stateDir, paths.certificateNew)
				removeAdminTestPath(t, stateDir, paths.keyNew)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey(fmt.Sprintf("%032x", 1500+index), relayadmin.OperationSetup, test.name+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			rotateKey := adminReplayKey(fmt.Sprintf("%032x", 1600+index), relayadmin.OperationRotate, test.name+"-rotate")
			leaveUnfinishedAdminRotation(t, state, rotateKey, test.point, RotateEndpointOptions{
				StateDir: stateDir, PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443",
			})
			test.mutate(t, stateDir, adminEndpointArtifactPaths(stateDir, rotateKey.RequestID))
			before := captureAdminEndpointNamespace(t, stateDir, rotateKey.RequestID)
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			err = executeAdminRepairError(t, reopened, adminReplayKey(fmt.Sprintf("%032x", 1700+index), relayadmin.OperationRepair, test.name+"-repair"))
			if !errors.Is(err, ErrAdminStateIncompatible) {
				t.Fatalf("Repair() error = %v, want ErrAdminStateIncompatible", err)
			}
			if after := captureAdminEndpointNamespace(t, stateDir, rotateKey.RequestID); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed repair changed impossible evidence:\n before=%#v\n after=%#v", before, after)
			}
		})
	}
}

func TestAdminRepairFinalizesEveryCompletedCleanupBoundaryAndIsIdempotent(t *testing.T) {
	points := []adminEndpointFaultPoint{
		adminEndpointAfterCommit,
		adminEndpointAfterOldKeyCleanup,
		adminEndpointAfterOldCertificateCleanup,
	}
	for index, point := range points {
		t.Run(string(point), func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey(fmt.Sprintf("%032x", 400+index), relayadmin.OperationSetup, string(point)+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			caCertificate := readAdminStateFile(t, stateDir, caCertFilename)
			caKey := readAdminStateFile(t, stateDir, caKeyFilename)
			identities := adminIdentityRows(t, state.database.db)
			state.endpointFault = func(got adminEndpointFaultPoint) error {
				if got == point {
					return errors.New("simulated completed crash")
				}
				return nil
			}
			rotateKey := adminReplayKey(fmt.Sprintf("%032x", 500+index), relayadmin.OperationRotate, string(point)+"-rotate")
			reservation, err := state.ReplayStore().Reserve(context.Background(), rotateKey)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
			}
			var exactResponse []byte
			_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
					StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
				})
				if rotateErr != nil {
					return nil, rotateErr
				}
				exactResponse, rotateErr = relayadmin.MarshalSuccessResponse(rotateKey.RequestID, rotateKey.Operation, relayadmin.EndpointRotationResult{
					PublicURL: result.PublicURL, Serial: result.Serial,
				})
				return exactResponse, rotateErr
			})
			if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
				t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
			}
			newCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			newKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			beforeRepair, err := reopened.Snapshot(context.Background())
			wantClass := AdminStateIncompatible
			if point == adminEndpointAfterOldCertificateCleanup {
				wantClass = AdminStateReady
			}
			if err != nil || beforeRepair.Class != wantClass {
				t.Fatalf("Snapshot() before repair = (%#v, %v), want class %v", beforeRepair, err, wantClass)
			}
			if point != adminEndpointAfterOldCertificateCleanup &&
				(reopened.database == nil || beforeRepair.AdministrativeOwnerUID != 501 || !beforeRepair.OwnerUIDBound) {
				t.Fatalf("completed cleanup reopen lost bound degraded state: snapshot=%#v database=%p", beforeRepair, reopened.database)
			}
			executeAdminRepair(t, reopened, adminReplayKey(fmt.Sprintf("%032x", 600+index), relayadmin.OperationRepair, string(point)+"-repair"))
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, newCertificate) {
				t.Fatal("repair did not preserve the committed new certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, newKey) {
				t.Fatal("repair did not preserve the committed new key")
			}
			if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificate) {
				t.Fatal("repair changed the CA certificate")
			}
			if got := readAdminStateFile(t, stateDir, caKeyFilename); !bytes.Equal(got, caKey) {
				t.Fatal("repair changed the CA key")
			}
			if got := adminIdentityRows(t, reopened.database.db); !reflect.DeepEqual(got, identities) {
				t.Fatalf("repair changed identities: got %#v, want %#v", got, identities)
			}
			if got := adminSettings(t, reopened.database.db)["relay_url"]; got != "https://new.example.ts.net:8443" {
				t.Fatalf("relay_url = %q, want committed new URL", got)
			}
			assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
			cached, err := reopened.ReplayStore().Reserve(context.Background(), rotateKey)
			if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, exactResponse) {
				t.Fatalf("rotate retry = (%#v, %v), want exact cached response", cached, err)
			}
			executeAdminRepair(t, reopened, adminReplayKey(fmt.Sprintf("%032x", 700+index), relayadmin.OperationRepair, string(point)+"-second-repair"))
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, newCertificate) {
				t.Fatal("idempotent repair changed the certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, newKey) {
				t.Fatal("idempotent repair changed the key")
			}
		})
	}
}

func TestAdminRotateCommitUncertaintyRecoversFromDurableReplayState(t *testing.T) {
	for _, commitSucceeded := range []bool{false, true} {
		name := "rolled-back"
		if commitSucceeded {
			name = "committed"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey("6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c", relayadmin.OperationSetup, name+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			caCertificate := readAdminStateFile(t, stateDir, caCertFilename)
			state.commitAdminMutation = func(transaction *sql.Tx) error {
				if commitSucceeded {
					if err := transaction.Commit(); err != nil {
						return err
					}
				} else if err := transaction.Rollback(); err != nil {
					return err
				}
				return errors.New("simulated uncertain commit")
			}
			rotateKey := adminReplayKey("6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d", relayadmin.OperationRotate, name+"-rotate")
			reservation, err := state.ReplayStore().Reserve(context.Background(), rotateKey)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
			}
			var exactResponse []byte
			_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
					StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
				})
				if rotateErr != nil {
					return nil, rotateErr
				}
				exactResponse, rotateErr = relayadmin.MarshalSuccessResponse(rotateKey.RequestID, rotateKey.Operation, relayadmin.EndpointRotationResult{
					PublicURL: result.PublicURL, Serial: result.Serial,
				})
				return exactResponse, rotateErr
			})
			if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
				t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
			}
			newCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			newKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if commitSucceeded {
				cached, err := reopened.ReplayStore().Reserve(context.Background(), rotateKey)
				if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, exactResponse) {
					t.Fatalf("committed retry = (%#v, %v), want exact cache", cached, err)
				}
			} else {
				busy, err := reopened.ReplayStore().Reserve(context.Background(), rotateKey)
				if err != nil || busy.Decision != relayadmin.ReplayBusy {
					t.Fatalf("uncommitted retry = (%#v, %v), want busy", busy, err)
				}
			}
			executeAdminRepair(t, reopened, adminReplayKey("6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e", relayadmin.OperationRepair, name+"-repair"))
			wantCertificate, wantKey, wantURL := oldCertificate, oldKey, "https://old.example.ts.net:8443"
			if commitSucceeded {
				wantCertificate, wantKey, wantURL = newCertificate, newKey, "https://new.example.ts.net:8443"
			}
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, wantCertificate) {
				t.Fatal("repair chose endpoint contrary to durable replay state")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, wantKey) {
				t.Fatal("repair chose endpoint key contrary to durable replay state")
			}
			if got := adminSettings(t, reopened.database.db)["relay_url"]; got != wantURL {
				t.Fatalf("relay_url = %q, want %q", got, wantURL)
			}
			if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificate) {
				t.Fatal("commit uncertainty recovery changed CA")
			}
			assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
		})
	}
}

func TestAdminRotateLiveFailureRollsBackEveryFileBoundary(t *testing.T) {
	points := []adminEndpointFaultPoint{
		adminEndpointBeforeNewCertificateWrite,
		adminEndpointAfterNewCertificateSync,
		adminEndpointAfterNewKeySync,
		adminEndpointAfterOldCertificateRename,
		adminEndpointAfterOldKeyRename,
		adminEndpointAfterNewCertificateRename,
		adminEndpointAfterNewKeyRename,
	}
	for index, point := range points {
		t.Run(string(point), func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey(fmt.Sprintf("%032x", 900+index), relayadmin.OperationSetup, string(point)+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			injected := errors.New("injected live file failure")
			state.endpointFault = func(got adminEndpointFaultPoint) error {
				if got == point {
					return injected
				}
				return nil
			}
			key := adminReplayKey(fmt.Sprintf("%032x", 1000+index), relayadmin.OperationRotate, string(point)+"-rotate")
			reservation, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
			}
			_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
					StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
				})
				if rotateErr != nil {
					return nil, rotateErr
				}
				return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
					PublicURL: result.PublicURL, Serial: result.Serial,
				})
			})
			if !errors.Is(err, injected) {
				t.Fatalf("Execute() error = %v, want injected failure", err)
			}
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
				t.Fatal("live failure did not restore old certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
				t.Fatal("live failure did not restore old key")
			}
			if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://old.example.ts.net:8443" {
				t.Fatalf("relay_url = %q, want old URL", got)
			}
			assertNoAdminEndpointArtifacts(t, stateDir, key.RequestID)
		})
	}
}

func TestAdminRotateCancellationAfterPromotionRestoresOldEndpoint(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	key := adminReplayKey("80808080808080808080808080808080", relayadmin.OperationRotate, "cancel")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = reservation.Mutation.Execute(ctx, func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		})
		if rotateErr != nil {
			return nil, rotateErr
		}
		cancel()
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if !errors.Is(err, context.Canceled) && !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want cancellation/indeterminate", err)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("cancellation did not restore old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("cancellation did not restore old key")
	}
	if got := adminSettings(t, state.database.db)["relay_url"]; got != "https://old.example.ts.net:8443" {
		t.Fatalf("relay_url = %q, want old URL", got)
	}
	assertNoAdminEndpointArtifacts(t, stateDir, key.RequestID)
}

func TestAdminRotateInProcessSnapshotDoesNotPermanentlyDegradeSuccessfulRotation(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})

	var during AdminSnapshot
	var duringErr error
	state.endpointFault = func(point adminEndpointFaultPoint) error {
		if point == adminEndpointAfterOldCertificateRename {
			snapshotContext, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			during, duringErr = state.Snapshot(snapshotContext)
			cancel()
		}
		return nil
	}
	executeAdminRotate(t, state, adminReplayKey("8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e8e", relayadmin.OperationRotate, "snapshot"), RotateEndpointOptions{
		StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
	})
	if duringErr != nil || during.Class != AdminStateReady || during.AdministrativeOwnerUID != 501 || !during.OwnerUIDBound {
		t.Fatalf("Snapshot() during active rotation = (%#v, %v), want bound Ready snapshot", during, duringErr)
	}
	after, err := state.Snapshot(context.Background())
	if err != nil || after.Class != AdminStateReady || after.AdministrativeOwnerUID != 501 || !after.OwnerUIDBound {
		t.Fatalf("Snapshot() after successful rotation = (%#v, %v), want bound Ready snapshot", after, err)
	}
}

func TestAdminRotateRejectsArtifactsFromAnotherRequest(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("81818181818181818181818181818181", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	oldRotateKey := adminReplayKey("82828282828282828282828282828282", relayadmin.OperationRotate, "old-crash")
	leaveUnfinishedAdminRotation(t, state, oldRotateKey, adminEndpointAfterNewKeySync, RotateEndpointOptions{
		StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
	})
	oldPaths := adminEndpointArtifactPaths(stateDir, oldRotateKey.RequestID)
	oldStagedCertificate := readAdminStateFile(t, stateDir, filepath.Base(oldPaths.certificateNew))
	oldStagedKey := readAdminStateFile(t, stateDir, filepath.Base(oldPaths.keyNew))
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	newRotateKey := adminReplayKey("83838383838383838383838383838383", relayadmin.OperationRotate, "new-rotate")
	reservation, err := reopened.ReplayStore().Reserve(context.Background(), newRotateKey)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := reopened.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "other.example.ts.net", PublicURL: "https://other.example.ts.net:8443",
		})
		if rotateErr != nil {
			return nil, rotateErr
		}
		return relayadmin.MarshalSuccessResponse(newRotateKey.RequestID, newRotateKey.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if !errors.Is(err, ErrAdminStateIncompatible) {
		t.Fatalf("Execute() error = %v, want ErrAdminStateIncompatible", err)
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("rejected rotate changed primary certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("rejected rotate changed primary key")
	}
	if got := readAdminStateFile(t, stateDir, filepath.Base(oldPaths.certificateNew)); !bytes.Equal(got, oldStagedCertificate) {
		t.Fatal("rejected rotate changed prior certificate evidence")
	}
	if got := readAdminStateFile(t, stateDir, filepath.Base(oldPaths.keyNew)); !bytes.Equal(got, oldStagedKey) {
		t.Fatal("rejected rotate changed prior key evidence")
	}
	assertNoAdminEndpointArtifacts(t, stateDir, newRotateKey.RequestID)
}

func leaveUnfinishedAdminRotation(
	t *testing.T,
	state *AdminState,
	key relayadmin.ReplayKey,
	point adminEndpointFaultPoint,
	options RotateEndpointOptions,
) {
	t.Helper()
	reserved, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reserved.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reserved, err)
	}
	reservation, ok := reserved.Mutation.(*adminMutationReservation)
	if !ok {
		t.Fatalf("mutation type = %T", reserved.Mutation)
	}
	if _, err := state.database.db.Exec(`UPDATE admin_mutation_replay SET state = 'executing' WHERE request_id = ? AND state = 'reserved'`, key.RequestID); err != nil {
		t.Fatal(err)
	}
	reservation.started = true
	transaction := &adminMutationTransaction{state: state, reservation: reservation, database: state.database, key: key}
	if err := transaction.ensureTransaction(context.Background()); err != nil {
		t.Fatal(err)
	}
	state.endpointFault = func(got adminEndpointFaultPoint) error {
		if got == point {
			return errors.New("simulated crash")
		}
		return nil
	}
	if _, err := state.rotateEndpoint(context.Background(), transaction, options); err == nil {
		t.Fatalf("rotateEndpoint() at %s unexpectedly succeeded", point)
	}
	if err := transaction.tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	transaction.tx = nil
	state.endpointFault = nil
}

func executeAdminRepair(t *testing.T, state *AdminState, key relayadmin.ReplayKey) []byte {
	t.Helper()
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve(repair) = (%#v, %v), want executable repair", reservation, err)
	}
	response, err := reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if repairErr := state.Repair(ctx, transaction); repairErr != nil {
			return nil, repairErr
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.RepairResult{Ready: true, Restarting: true})
	})
	if err != nil {
		t.Fatalf("Execute(Repair) error = %v", err)
	}
	return response
}

func assertNoAdminEndpointArtifacts(t *testing.T, stateDir, requestID string) {
	t.Helper()
	for _, suffix := range []string{".crt.new", ".key.new", ".crt.old", ".key.old"} {
		path := filepath.Join(stateDir, ".relay-rotate-"+requestID+suffix)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rotation artifact %q remains: %v", filepath.Base(path), err)
		}
	}
}

func executeAdminRotate(t *testing.T, state *AdminState, key relayadmin.ReplayKey, options RotateEndpointOptions) ([]byte, RotateEndpointResult) {
	t.Helper()
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve() = (%#v, %v), want executable rotate", reservation, err)
	}
	var rotateResult RotateEndpointResult
	response, err := reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, rotateErr := state.RotateEndpoint(ctx, transaction, options)
		if rotateErr != nil {
			return nil, rotateErr
		}
		rotateResult = result
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if err != nil {
		t.Fatalf("Execute(RotateEndpoint) error = %v", err)
	}
	return response, rotateResult
}

type adminIdentityRow struct {
	serial     string
	role       string
	createdAt  int64
	lastSeenAt sql.NullInt64
	revokedAt  sql.NullInt64
}

func adminIdentityRows(t *testing.T, database *sql.DB) []adminIdentityRow {
	t.Helper()
	rows, err := database.Query(`SELECT serial, role, created_at, last_seen_at, revoked_at FROM identities ORDER BY serial`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []adminIdentityRow
	for rows.Next() {
		var row adminIdentityRow
		if err := rows.Scan(&row.serial, &row.role, &row.createdAt, &row.lastSeenAt, &row.revokedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func adminSettings(t *testing.T, database *sql.DB) map[string]string {
	t.Helper()
	rows, err := database.Query(`SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		result[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func readAdminStateFile(t *testing.T, stateDir, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(stateDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func removeAdminTestPath(t *testing.T, stateDir, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(stateDir); err != nil {
		t.Fatal(err)
	}
}

func renameAdminTestPath(t *testing.T, stateDir, source, destination string) {
	t.Helper()
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(stateDir); err != nil {
		t.Fatal(err)
	}
}
