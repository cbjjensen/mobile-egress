package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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

func TestAdminRepairAdoptsDatabaseAfterGuardRecoveryAndCachesItself(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	initial, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, initial, adminReplayKey("70707070707070707070707070707070", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	caCertificate := readAdminStateFile(t, stateDir, caCertFilename)
	caKey := readAdminStateFile(t, stateDir, caKeyFilename)
	relayCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	relayKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	identities := adminIdentityRows(t, initial.database.db)
	settings := adminSettings(t, initial.database.db)
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	guardFailure := errors.New("injected guard failure containing private path C:\\secret")
	guard := &recordingAdminPathGuard{validateErr: guardFailure}
	degraded, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, PathGuard: guard})
	if err != nil {
		t.Fatal(err)
	}
	defer degraded.Close()
	if degraded.database != nil {
		t.Fatal("guard-rejected open unexpectedly adopted the database")
	}
	guard.validateErr = nil
	repairKey := adminReplayKey("71717171717171717171717171717171", relayadmin.OperationRepair, "database-less-repair")
	response := executeAdminRepair(t, degraded, repairKey)
	if bytes.Contains(response, []byte("secret")) || bytes.Contains(response, []byte(stateDir)) {
		t.Fatalf("repair response leaked internal input: %s", response)
	}
	if guard.repairCalls != 1 || guard.validateCalls != 2 {
		t.Fatalf("guard calls = repair %d / validate %d, want 1 / 2", guard.repairCalls, guard.validateCalls)
	}
	if degraded.database == nil {
		t.Fatal("successful database-less repair did not adopt the database")
	}
	if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificate) {
		t.Fatal("repair changed CA certificate")
	}
	if got := readAdminStateFile(t, stateDir, caKeyFilename); !bytes.Equal(got, caKey) {
		t.Fatal("repair changed CA key")
	}
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, relayCertificate) {
		t.Fatal("repair changed relay certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, relayKey) {
		t.Fatal("repair changed relay key")
	}
	if got := adminIdentityRows(t, degraded.database.db); !reflect.DeepEqual(got, identities) {
		t.Fatalf("repair changed identities: got %#v, want %#v", got, identities)
	}
	if got := adminSettings(t, degraded.database.db); !reflect.DeepEqual(got, settings) {
		t.Fatalf("repair changed settings: got %#v, want %#v", got, settings)
	}
	if err := degraded.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err := reopened.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("reopened repair replay = (%#v, %v), want exact cached response", cached, err)
	}
	snapshot, err := reopened.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateReady || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("reopened Snapshot() = (%#v, %v), want ready UID 501", snapshot, err)
	}
}

func TestAdminRepairDatabaseLessAdoptionRecoversEndpointEvidence(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	initial, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, initial, adminReplayKey("84848484848484848484848484848484", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	rotateKey := adminReplayKey("85858585858585858585858585858585", relayadmin.OperationRotate, "database-less-crash")
	leaveUnfinishedAdminRotation(t, initial, rotateKey, adminEndpointAfterNewCertificateRename, RotateEndpointOptions{
		StateDir: stateDir, PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443",
	})
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	guard := &recordingAdminPathGuard{validateErr: errors.New("injected startup rejection")}
	degraded, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, PathGuard: guard})
	if err != nil {
		t.Fatal(err)
	}
	defer degraded.Close()
	if degraded.database != nil {
		t.Fatal("guard-rejected state unexpectedly opened its database")
	}
	guard.validateErr = nil
	repairKey := adminReplayKey("86868686868686868686868686868686", relayadmin.OperationRepair, "database-less-endpoint-repair")
	response := executeAdminRepair(t, degraded, repairKey)
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("database-less repair did not restore old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("database-less repair did not restore old key")
	}
	assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
	if err := degraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err := reopened.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("repair retry = (%#v, %v), want exact cache", cached, err)
	}
}

func TestAdminStateOpenClassifiesEndpointInventoryBeforeReady(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{
			name: "malformed artifact name",
			prepare: func(t *testing.T, stateDir string) {
				if err := os.WriteFile(filepath.Join(stateDir, ".relay-rotate-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.crt.new"), []byte("malformed"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "orphan exact artifact",
			prepare: func(t *testing.T, stateDir string) {
				path := adminEndpointArtifactPaths(stateDir, "8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f").certificateNew
				if err := os.WriteFile(path, []byte("orphan"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non-regular inventory entry",
			prepare: func(t *testing.T, stateDir string) {
				if err := os.Mkdir(filepath.Join(stateDir, "unexpected-directory"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			initial, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, initial, adminReplayKey("90909090909090909090909090909090", relayadmin.OperationSetup, test.name+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, stateDir)

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible || snapshot.AdministrativeOwnerUID != 501 || !snapshot.OwnerUIDBound {
				t.Fatalf("Snapshot() = (%#v, %v), want bound incompatible", snapshot, err)
			}
			if reopened.database == nil || !reopened.replayReady {
				t.Fatalf("inventory failure discarded durable database/replay: database=%p replayReady=%v", reopened.database, reopened.replayReady)
			}
		})
	}
}

func TestAdminRepairDatabaseLessAdoptionRejectsMigrationCandidateWithoutChangingIt(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	initial, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, initial, adminReplayKey("8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := openDatabase(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DROP TABLE admin_mutation_replay`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`PRAGMA user_version = 2`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	guard := &recordingAdminPathGuard{validateErr: errors.New("injected startup rejection")}
	degraded, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, PathGuard: guard})
	if err != nil {
		t.Fatal(err)
	}
	defer degraded.Close()
	guard.validateErr = nil
	err = executeAdminRepairError(t, degraded, adminReplayKey("8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b", relayadmin.OperationRepair, "reject-v2"))
	if !errors.Is(err, ErrAdminStateIncompatible) {
		t.Fatalf("Repair() error = %v, want ErrAdminStateIncompatible", err)
	}

	unchanged, err := openDatabase(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer unchanged.Close()
	var version int
	if err := unchanged.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("database version = %d, want unchanged version 2", version)
	}
	var journalTables int
	if err := unchanged.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'admin_mutation_replay'`).Scan(&journalTables); err != nil {
		t.Fatal(err)
	}
	if journalTables != 0 {
		t.Fatal("repair migrated the rejected database by creating admin_mutation_replay")
	}
}

func TestAdminRepairDatabaseLessCachedReplayDoesNotRecoverLaterRotation(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	initial, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, initial, adminReplayKey("91919191919191919191919191919191", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	repairKey := adminReplayKey("92929292929292929292929292929292", relayadmin.OperationRepair, "completed-repair")
	exactResponse := executeAdminRepair(t, initial, repairKey)
	rotateKey := adminReplayKey("93939393939393939393939393939393", relayadmin.OperationRotate, "later-rotation")
	leaveUnfinishedAdminRotation(t, initial, rotateKey, adminEndpointAfterNewKeyRename, RotateEndpointOptions{
		StateDir: stateDir, PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443",
	})
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	beforeNamespace := captureAdminEndpointNamespace(t, stateDir, rotateKey.RequestID)
	beforeDatabase := sha256.Sum256(readAdminStateFile(t, stateDir, databaseFilename))
	guard := &recordingAdminPathGuard{validateErr: errors.New("injected one-shot startup rejection")}
	degraded, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, PathGuard: guard})
	if err != nil {
		t.Fatal(err)
	}
	defer degraded.Close()
	if degraded.database != nil {
		t.Fatal("guard-rejected reopen unexpectedly attached its database")
	}
	guard.validateErr = nil
	sideEffects := 0
	degraded.syncEndpointDirectory = func(path string) error {
		sideEffects++
		return syncDirectory(path)
	}
	reservation, err := degraded.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve(cached repair through degraded adoption) = (%#v, %v), want execute facade", reservation, err)
	}
	response, err := reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if repairErr := degraded.Repair(ctx, transaction); repairErr != nil {
			return nil, repairErr
		}
		return relayadmin.MarshalSuccessResponse(repairKey.RequestID, repairKey.Operation, relayadmin.RepairResult{Ready: true, Restarting: true})
	})
	if err != nil || !bytes.Equal(response, exactResponse) {
		t.Fatalf("cached repair replay = (%q, %v), want exact bytes %q", response, err, exactResponse)
	}
	if sideEffects != 0 {
		t.Fatalf("cached repair replay invoked endpoint recovery %d times", sideEffects)
	}
	if after := captureAdminEndpointNamespace(t, stateDir, rotateKey.RequestID); !reflect.DeepEqual(after, beforeNamespace) {
		t.Fatalf("cached repair replay changed endpoint namespace:\n before=%#v\n after=%#v", beforeNamespace, after)
	}
	if after := sha256.Sum256(readAdminStateFile(t, stateDir, databaseFilename)); after != beforeDatabase {
		t.Fatal("cached repair replay changed durable database bytes")
	}
	snapshot, err := degraded.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || snapshot.AdministrativeOwnerUID != 501 || !snapshot.OwnerUIDBound {
		t.Fatalf("Snapshot() after cached repair = (%#v, %v), want bound degraded", snapshot, err)
	}

	degraded.syncEndpointDirectory = syncDirectory
	executeAdminRepair(t, degraded, adminReplayKey("94949494949494949494949494949494", relayadmin.OperationRepair, "fresh-repair"))
	assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
}

func TestAdminRepairFsyncFailureIsRedactedAndRetryable(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("72727272727272727272727272727272", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "same.example.ts.net", PublicURL: "https://same.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	oldCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
	oldKey := readAdminStateFile(t, stateDir, relayKeyFilename)
	rotateKey := adminReplayKey("73737373737373737373737373737373", relayadmin.OperationRotate, "fsync-crash")
	leaveUnfinishedAdminRotation(t, state, rotateKey, adminEndpointAfterNewKeyRename, RotateEndpointOptions{
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
	injected := errors.New("injected fsync failure C:\\private\\relay")
	calls := 0
	reopened.syncEndpointDirectory = func(string) error {
		calls++
		if calls == 1 {
			return injected
		}
		return nil
	}
	repairKey := adminReplayKey("74747474747474747474747474747474", relayadmin.OperationRepair, "fsync-repair")
	reservation, err := reopened.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if repairErr := reopened.Repair(ctx, transaction); repairErr != nil {
			return nil, repairErr
		}
		return relayadmin.MarshalSuccessResponse(repairKey.RequestID, repairKey.Operation, relayadmin.RepairResult{Ready: true, Restarting: true})
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte("private")) || bytes.Contains([]byte(err.Error()), []byte(stateDir)) {
		t.Fatalf("repair error leaked raw fsync detail: %v", err)
	}
	reopened.syncEndpointDirectory = syncDirectory
	executeAdminRepair(t, reopened, adminReplayKey("75757575757575757575757575757575", relayadmin.OperationRepair, "fsync-retry"))
	if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, oldCertificate) {
		t.Fatal("retry did not restore old certificate")
	}
	if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, oldKey) {
		t.Fatal("retry did not restore old key")
	}
	assertNoAdminEndpointArtifacts(t, stateDir, rotateKey.RequestID)
}

func TestAdminRepairRejectsNonRestartingResponseAndRetainsReplayFailClosed(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("87878787878787878787878787878787", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})

	repairKey := adminReplayKey("88888888888888888888888888888888", relayadmin.OperationRepair, "non-restarting-response")
	reservation, err := state.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v), want executable repair", reservation, err)
	}
	badResponse, err := reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if repairErr := state.Repair(ctx, transaction); repairErr != nil {
			return nil, repairErr
		}
		return relayadmin.MarshalSuccessResponse(repairKey.RequestID, repairKey.Operation, relayadmin.RepairResult{Ready: true, Restarting: false})
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) || len(badResponse) != 0 {
		t.Fatalf("Execute() = (%q, %v), want empty ErrMutationIndeterminate", badResponse, err)
	}
	var replayState string
	var response []byte
	if err := state.database.db.QueryRow(`SELECT state, response FROM admin_mutation_replay WHERE request_id = ?`, repairKey.RequestID).Scan(&replayState, &response); err != nil {
		t.Fatal(err)
	}
	if replayState != "indeterminate" || len(response) != 0 {
		t.Fatalf("repair replay = (%q, %q), want indeterminate with no cached response", replayState, response)
	}
	busy, err := state.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || busy.Decision != relayadmin.ReplayBusy {
		t.Fatalf("same-ID retry = (%#v, %v), want busy", busy, err)
	}
	executeAdminRepair(t, state, adminReplayKey("89898989898989898989898989898989", relayadmin.OperationRepair, "valid-retry"))
}

func TestAdminRepairRejectsUnknownAmbiguousAndUnsafeEvidenceWithoutDeletion(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *AdminState) []string
	}{
		{
			name: "unknown file",
			prepare: func(t *testing.T, state *AdminState) []string {
				path := filepath.Join(state.stateDir, "unexpected-private-evidence")
				if err := os.WriteFile(path, []byte("raw-secret-material"), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "orphan exact artifact",
			prepare: func(t *testing.T, state *AdminState) []string {
				path := adminEndpointArtifactPaths(state.stateDir, "76767676767676767676767676767676").certificateNew
				if err := os.WriteFile(path, readAdminStateFile(t, state.stateDir, relayCertFilename), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "multiple request IDs",
			prepare: func(t *testing.T, state *AdminState) []string {
				first := adminEndpointArtifactPaths(state.stateDir, "77777777777777777777777777777777").certificateNew
				second := adminEndpointArtifactPaths(state.stateDir, "78787878787878787878787878787878").keyNew
				if err := os.WriteFile(first, readAdminStateFile(t, state.stateDir, relayCertFilename), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(second, readAdminStateFile(t, state.stateDir, relayKeyFilename), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{first, second}
			},
		},
		{
			name: "malformed artifact name",
			prepare: func(t *testing.T, state *AdminState) []string {
				path := filepath.Join(state.stateDir, ".relay-rotate-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.crt.new")
				if err := os.WriteFile(path, readAdminStateFile(t, state.stateDir, relayCertFilename), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "hardlink alias",
			prepare: func(t *testing.T, state *AdminState) []string {
				path := adminEndpointArtifactPaths(state.stateDir, "79797979797979797979797979797979").certificateNew
				if err := os.Link(filepath.Join(state.stateDir, relayCertFilename), path); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
				return []string{path}
			},
		},
		{
			name: "symlink artifact",
			prepare: func(t *testing.T, state *AdminState) []string {
				path := adminEndpointArtifactPaths(state.stateDir, "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a").certificateNew
				if err := os.Symlink(filepath.Join(state.stateDir, relayCertFilename), path); err != nil {
					t.Skipf("symbolic links unavailable: %v", err)
				}
				return []string{path}
			},
		},
		{
			name: "invalid tied artifact",
			prepare: func(t *testing.T, state *AdminState) []string {
				key := adminReplayKey("7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b", relayadmin.OperationRotate, "invalid-artifact")
				if _, err := state.database.db.Exec(`INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, 'rotate', 'executing', NULL, 1)`, key.RequestID, key.Digest[:]); err != nil {
					t.Fatal(err)
				}
				path := adminEndpointArtifactPaths(state.stateDir, key.RequestID).certificateNew
				if err := os.WriteFile(path, []byte("raw-secret-invalid-certificate"), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "CA certificate copied as new leaf",
			prepare: func(t *testing.T, state *AdminState) []string {
				key := adminReplayKey("7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", relayadmin.OperationRotate, "ca-as-leaf")
				if _, err := state.database.db.Exec(`INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, 'rotate', 'executing', NULL, 1)`, key.RequestID, key.Digest[:]); err != nil {
					t.Fatal(err)
				}
				path := adminEndpointArtifactPaths(state.stateDir, key.RequestID).certificateNew
				if err := os.WriteFile(path, readAdminStateFile(t, state.stateDir, caCertFilename), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "new leaf signed by wrong CA",
			prepare: func(t *testing.T, state *AdminState) []string {
				key := adminReplayKey("83838383838383838383838383838383", relayadmin.OperationRotate, "wrong-ca-signature")
				if _, err := state.database.db.Exec(`INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, 'rotate', 'executing', NULL, 1)`, key.RequestID, key.Digest[:]); err != nil {
					t.Fatal(err)
				}
				_, _, foreignCertificate, _, err := generateCertificateState("relay.example.ts.net", state.now())
				if err != nil {
					t.Fatal(err)
				}
				path := adminEndpointArtifactPaths(state.stateDir, key.RequestID).certificateNew
				if err := os.WriteFile(path, foreignCertificate, 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{path}
			},
		},
		{
			name: "new leaf missing ServerAuth",
			prepare: func(t *testing.T, state *AdminState) []string {
				key := adminReplayKey("82828282828282828282828282828282", relayadmin.OperationRotate, "missing-server-auth")
				if _, err := state.database.db.Exec(`INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, 'rotate', 'executing', NULL, 1)`, key.RequestID, key.Digest[:]); err != nil {
					t.Fatal(err)
				}
				certificatePEM, privateKeyPEM := newAdminLeafWithoutExtendedKeyUsage(t, state)
				paths := adminEndpointArtifactPaths(state.stateDir, key.RequestID)
				if err := os.WriteFile(paths.certificateNew, certificatePEM, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.keyNew, privateKeyPEM, 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{paths.certificateNew, paths.keyNew}
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
			defer state.Close()
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey("7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c", relayadmin.OperationSetup, test.name+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			caCertificate := readAdminStateFile(t, stateDir, caCertFilename)
			caKey := readAdminStateFile(t, stateDir, caKeyFilename)
			relayCertificate := readAdminStateFile(t, stateDir, relayCertFilename)
			relayKey := readAdminStateFile(t, stateDir, relayKeyFilename)
			identities := adminIdentityRows(t, state.database.db)
			settings := adminSettings(t, state.database.db)
			evidence := test.prepare(t, state)
			repairKey := adminReplayKey(fmt.Sprintf("%032x", 800+index), relayadmin.OperationRepair, test.name+"-repair")
			err = executeAdminRepairError(t, state, repairKey)
			if !errors.Is(err, ErrAdminStateIncompatible) {
				t.Fatalf("Repair() error = %v, want ErrAdminStateIncompatible", err)
			}
			if bytes.Contains([]byte(err.Error()), []byte("raw-secret")) || bytes.Contains([]byte(err.Error()), []byte(stateDir)) {
				t.Fatalf("repair error leaked evidence: %v", err)
			}
			for _, path := range evidence {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("repair removed evidence %q: %v", filepath.Base(path), err)
				}
			}
			if got := readAdminStateFile(t, stateDir, caCertFilename); !bytes.Equal(got, caCertificate) {
				t.Fatal("failed repair changed CA certificate")
			}
			if got := readAdminStateFile(t, stateDir, caKeyFilename); !bytes.Equal(got, caKey) {
				t.Fatal("failed repair changed CA key")
			}
			if got := readAdminStateFile(t, stateDir, relayCertFilename); !bytes.Equal(got, relayCertificate) {
				t.Fatal("failed repair changed relay certificate")
			}
			if got := readAdminStateFile(t, stateDir, relayKeyFilename); !bytes.Equal(got, relayKey) {
				t.Fatal("failed repair changed relay key")
			}
			if got := adminIdentityRows(t, state.database.db); !reflect.DeepEqual(got, identities) {
				t.Fatalf("failed repair changed identities: got %#v, want %#v", got, identities)
			}
			if got := adminSettings(t, state.database.db); !reflect.DeepEqual(got, settings) {
				t.Fatalf("failed repair changed settings: got %#v, want %#v", got, settings)
			}
		})
	}
}

func TestAdminRepairRedactsPathGuardFailures(t *testing.T) {
	for _, phase := range []string{"repair", "validate"} {
		t.Run(phase, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			guard := &recordingAdminPathGuard{}
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, PathGuard: guard})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, adminReplayKey("7d7d7d7d7d7d7d7d7d7d7d7d7d7d7d7d", relayadmin.OperationSetup, phase+"-setup"), AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			raw := errors.New("raw guard error C:\\private\\owner-501")
			if phase == "repair" {
				guard.repairErr = raw
			} else {
				guard.validateErr = raw
			}
			err = executeAdminRepairError(t, state, adminReplayKey("7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e", relayadmin.OperationRepair, phase+"-repair"))
			want := ErrAdminStateIncompatible
			if phase == "repair" {
				want = relayadmin.ErrMutationIndeterminate
			}
			if !errors.Is(err, want) {
				t.Fatalf("Repair() error = %v, want %v", err, want)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "501") {
				t.Fatalf("Repair() error leaked guard detail: %v", err)
			}
		})
	}
}

func executeAdminRepairError(t *testing.T, state *AdminState, key relayadmin.ReplayKey) error {
	t.Helper()
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve(repair) = (%#v, %v), want executable repair", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		if repairErr := state.Repair(ctx, transaction); repairErr != nil {
			return nil, repairErr
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.RepairResult{Ready: true, Restarting: true})
	})
	return err
}

func newAdminLeafWithoutExtendedKeyUsage(t *testing.T, state *AdminState) ([]byte, []byte) {
	t.Helper()
	caCertificatePEM := readAdminStateFile(t, state.stateDir, caCertFilename)
	caPrivateKeyPEM := readAdminStateFile(t, state.stateDir, caKeyFilename)
	caCertificate, caPrivateKey, err := parseCertificateAuthorityState(caCertificatePEM, caPrivateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serialNumber, err := randomSerial()
	if err != nil {
		t.Fatal(err)
	}
	now := state.now()
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: "relay.example.ts.net"},
		DNSNames:     []string{"relay.example.ts.net"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}, caCertificate, privateKey.Public(), caPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
}

func captureAdminEndpointNamespace(t *testing.T, stateDir, requestID string) map[string][]byte {
	t.Helper()
	paths := adminEndpointArtifactPaths(stateDir, requestID)
	result := make(map[string][]byte)
	for _, path := range []string{
		filepath.Join(stateDir, relayCertFilename),
		filepath.Join(stateDir, relayKeyFilename),
		paths.certificateNew,
		paths.keyNew,
		paths.certificateOld,
		paths.keyOld,
	} {
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result[filepath.Base(path)] = contents
	}
	return result
}
