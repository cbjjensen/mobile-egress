package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/pairing"
)

const maximumAdminEndpointArtifactSize = 512 * 1024

type adminEndpointEvidence struct {
	requestID string
	paths     adminEndpointPaths
	present   map[string]bool
}

type adminRecoveryInvariant struct {
	caCertificatePEM []byte
	caPrivateKeyPEM  []byte
	databaseHash     [sha256.Size]byte
}

type adminRepairStage struct {
	state     *AdminState
	requestID string
	invariant adminRecoveryInvariant
}

func (state *AdminState) repairAdminState(ctx context.Context, transaction *adminMutationTransaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.pathGuard != nil {
		if err := state.pathGuard.Repair(ctx); err != nil {
			state.markAdminDegraded()
			return relayadmin.ErrMutationIndeterminate
		}
		if err := state.pathGuard.Validate(ctx); err != nil {
			state.markAdminDegraded()
			return ErrAdminStateIncompatible
		}
	}
	if transaction.database == nil || transaction.tx == nil {
		if err := state.adoptDatabaseForRepair(ctx, transaction); err != nil {
			return err
		}
		if len(transaction.cached) != 0 {
			// Exact replay is side-effect free. Attaching a previously rejected
			// but now guarded database must not let an old successful repair
			// recover evidence created by a later operation.
			return nil
		}
	}
	return state.repairAdminDatabase(ctx, transaction)
}

func (state *AdminState) repairAdminDatabase(ctx context.Context, transaction *adminMutationTransaction) error {
	if err := validSchemaFromQuery(ctx, transaction.tx); err != nil {
		state.markAdminDegraded()
		return ErrAdminStateIncompatible
	}
	invariant, err := captureAdminRecoveryInvariant(ctx, transaction.tx, state.stateDir, transaction.key.RequestID)
	if err != nil {
		state.markAdminDegraded()
		return classifyAdminRecoveryError(ctx, err)
	}
	if err := recoverAdminEndpoint(ctx, transaction.tx, state.stateDir, state.syncEndpointDirectory); err != nil {
		state.markAdminDegraded()
		return classifyAdminRecoveryError(ctx, err)
	}
	after, err := captureAdminRecoveryInvariant(ctx, transaction.tx, state.stateDir, transaction.key.RequestID)
	if err != nil || !equalAdminRecoveryInvariant(invariant, after) {
		state.markAdminDegraded()
		return ErrAdminStateIncompatible
	}
	snapshot, err := adminSnapshotFromQuery(ctx, transaction.tx, state.stateDir)
	if err != nil || snapshot.Class != AdminStateReady {
		state.markAdminDegraded()
		return ErrAdminStateIncompatible
	}
	transaction.repair = &adminRepairStage{state: state, requestID: transaction.key.RequestID, invariant: invariant}
	return nil
}

func (state *AdminState) adoptDatabaseForRepair(ctx context.Context, transaction *adminMutationTransaction) error {
	if transaction == nil || transaction.reservation == nil || transaction.key.Operation != relayadmin.OperationRepair {
		return relayadmin.ErrReplayState
	}
	database, err := openExistingAdminRepairStore(ctx, filepath.Join(state.stateDir, databaseFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	attached := false
	defer func() {
		if !attached {
			_ = database.Close()
		}
	}()
	outcome, err := reserveDurableAdminMutation(ctx, database, state.mutationCapacity, transaction.key)
	if err != nil {
		return relayadmin.ErrMutationIndeterminate
	}
	switch outcome.decision {
	case relayadmin.ReplayExecute:
		result, err := database.db.ExecContext(ctx, `UPDATE admin_mutation_replay SET state = 'executing' WHERE request_id = ? AND digest = ? AND operation = ? AND state = 'reserved'`,
			transaction.key.RequestID, transaction.key.Digest[:], string(transaction.key.Operation))
		if err != nil {
			return relayadmin.ErrMutationIndeterminate
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return relayadmin.ErrMutationIndeterminate
		}
	case relayadmin.ReplayCached:
		if !validAdminRepairSuccessResponse(transaction.key, outcome.response) {
			return ErrAdminStateIncompatible
		}
		transaction.cached = append([]byte(nil), outcome.response...)
	default:
		return relayadmin.ErrReplayState
	}
	keys, err := loadAdminMutationKeys(ctx, database)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	binding := canonicalAdminBinding(ctx, database.db)
	if !binding.OwnerUIDBound || binding.AdministrativeOwnerUID == 0 {
		return ErrAdminStateIncompatible
	}
	state.mu.Lock()
	if state.closed || state.database != nil || state.active[transaction.key.RequestID] != transaction.reservation {
		state.mu.Unlock()
		return relayadmin.ErrReplayState
	}
	state.database = database
	state.replayReady = true
	state.mutationKeys = keys
	state.presence = adminStatePresenceDegraded
	state.degraded = binding
	transaction.database = database
	transaction.reservation.database = database
	transaction.adopted = true
	state.mu.Unlock()
	attached = true
	if len(transaction.cached) != 0 {
		return nil
	}
	if err := transaction.ensureTransaction(ctx); err != nil {
		state.markAdminDegraded()
		return relayadmin.ErrMutationIndeterminate
	}
	return nil
}

func openExistingAdminRepairStore(ctx context.Context, path string) (*store, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 || !adminFileHasSingleLink(info) {
		return nil, ErrAdminStateIncompatible
	}
	database, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			_ = database.Close()
		}
	}()
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		return nil, ErrAdminStateIncompatible
	}
	if err := database.validSchema(ctx); err != nil {
		return nil, ErrAdminStateIncompatible
	}
	valid = true
	return database, nil
}

func (stage *adminRepairStage) finalize() error {
	if stage == nil || stage.state == nil {
		return relayadmin.ErrReplayState
	}
	state := stage.state
	state.mu.Lock()
	database := state.database
	closed := state.closed
	state.mu.Unlock()
	if closed || database == nil {
		return ErrAdminStateIncompatible
	}
	ctx := context.Background()
	if err := database.validSchema(ctx); err != nil {
		return ErrAdminStateIncompatible
	}
	after, err := captureAdminRecoveryInvariant(ctx, database.db, state.stateDir, stage.requestID)
	if err != nil || !equalAdminRecoveryInvariant(stage.invariant, after) {
		return ErrAdminStateIncompatible
	}
	snapshot, err := adminSnapshotFromQuery(ctx, database.db, state.stateDir)
	if err != nil || snapshot.Class != AdminStateReady {
		return ErrAdminStateIncompatible
	}
	keys, err := loadAdminMutationKeys(ctx, database)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	state.mu.Lock()
	if state.closed || state.database != database {
		state.mu.Unlock()
		return ErrAdminStateIncompatible
	}
	state.presence = adminStatePresenceReady
	state.replayReady = true
	snapshot.Class = AdminStateIncompatible
	state.degraded = snapshot
	state.mutationKeys = keys
	state.uncertain = make(map[string]relayadmin.ReplayKey)
	state.fallback = make(map[string]adminCompletedReplay)
	state.mu.Unlock()
	return nil
}

func recoverAdminEndpoint(ctx context.Context, queryer adminQueryer, stateDir string, syncer func(string) error) error {
	if syncer == nil {
		syncer = syncDirectory
	}
	evidence, err := scanAdminEndpointEvidence(stateDir)
	if err != nil {
		return err
	}
	if evidence.requestID == "" {
		return nil
	}
	var digest []byte
	var operation, replayState string
	var response []byte
	if err := queryer.QueryRowContext(ctx, `SELECT digest, operation, state, response FROM admin_mutation_replay WHERE request_id = ?`, evidence.requestID).
		Scan(&digest, &operation, &replayState, &response); err != nil {
		return ErrAdminStateIncompatible
	}
	key, err := persistedAdminReplayKey(evidence.requestID, digest, operation)
	if err != nil || key.Operation != relayadmin.OperationRotate {
		return ErrAdminStateIncompatible
	}
	caCertificatePEM, err := readRegularAdminStateFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	caPrivateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	caCertificate, _, err := parseCertificateAuthorityState(caCertificatePEM, caPrivateKeyPEM)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	switch replayState {
	case "completed":
		return finalizeCompletedAdminEndpoint(stateDir, key, response, evidence, caCertificate, queryer, ctx, syncer)
	case "reserved", "executing", "indeterminate":
		if len(response) != 0 {
			return ErrAdminStateIncompatible
		}
		return rollbackUnfinishedAdminEndpoint(stateDir, evidence, caCertificate, queryer, ctx, syncer)
	default:
		return ErrAdminStateIncompatible
	}
}

func finalizeCompletedAdminEndpoint(
	stateDir string,
	key relayadmin.ReplayKey,
	response []byte,
	evidence adminEndpointEvidence,
	caCertificate *x509.Certificate,
	queryer adminQueryer,
	ctx context.Context,
	syncer func(string) error,
) error {
	parsed, err := relayadmin.ParseResponse(response)
	if err != nil || parsed.RequestID != key.RequestID || parsed.Operation != relayadmin.OperationRotate || !parsed.OK {
		return ErrAdminStateIncompatible
	}
	result, ok := parsed.Result.(relayadmin.EndpointRotationResult)
	if !ok {
		return ErrAdminStateIncompatible
	}
	var relayURL string
	if err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'relay_url'`).Scan(&relayURL); err != nil {
		return ErrAdminStateIncompatible
	}
	origin, err := pairing.RelayOrigin(result.PublicURL)
	if err != nil || relayURL != result.PublicURL || result.PublicURL != origin.String() || origin.Hostname() == "" {
		return ErrAdminStateIncompatible
	}
	certificatePrimary := filepath.Join(stateDir, relayCertFilename)
	keyPrimary := filepath.Join(stateDir, relayKeyFilename)
	certificatePEM, certificatePrimaryPresent, err := readOptionalRegularAdminStateFile(certificatePrimary)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, keyPrimaryPresent, err := readOptionalRegularAdminStateFile(keyPrimary)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	certificateNew, certificateNewPresent, err := readOptionalRegularAdminStateFile(evidence.paths.certificateNew)
	if err != nil || certificateNewPresent && !bytes.Equal(certificateNew, certificatePEM) {
		return ErrAdminStateIncompatible
	}
	keyNew, keyNewPresent, err := readOptionalRegularAdminStateFile(evidence.paths.keyNew)
	if err != nil || keyNewPresent && !bytes.Equal(keyNew, privateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	certificateOld, certificateOldPresent, err := readOptionalRegularAdminStateFile(evidence.paths.certificateOld)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	keyOld, keyOldPresent, err := readOptionalRegularAdminStateFile(evidence.paths.keyOld)
	if err != nil || keyOldPresent && !certificateOldPresent {
		return ErrAdminStateIncompatible
	}
	if !validCompletedAdminEndpointLayout(adminEndpointLayoutFromPresence(
		certificatePrimaryPresent, keyPrimaryPresent, certificateOldPresent, keyOldPresent, certificateNewPresent, keyNewPresent,
	)) {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, caCertificate, origin.Hostname(), result.Serial); err != nil {
		return ErrAdminStateIncompatible
	}
	if certificateOldPresent && keyOldPresent {
		if _, err := validateRelayEndpointIdentity(certificateOld, keyOld, caCertificate, "", ""); err != nil {
			return ErrAdminStateIncompatible
		}
	} else if certificateOldPresent {
		if _, err := validateRelayEndpointCertificate(certificateOld, caCertificate, "", ""); err != nil {
			return ErrAdminStateIncompatible
		}
	}
	for _, artifact := range []struct {
		path     string
		contents []byte
		present  bool
	}{
		{evidence.paths.keyNew, keyNew, keyNewPresent},
		{evidence.paths.certificateNew, certificateNew, certificateNewPresent},
		{evidence.paths.keyOld, keyOld, keyOldPresent},
		{evidence.paths.certificateOld, certificateOld, certificateOldPresent},
	} {
		if artifact.present {
			if err := removeAdminEndpointArtifactExactUsing(stateDir, artifact.path, artifact.contents, syncer); err != nil {
				return err
			}
		}
	}
	if _, err := validateRelayEndpointIdentityFromFiles(certificatePrimary, keyPrimary, caCertificate, origin.Hostname(), result.Serial); err != nil {
		return ErrAdminStateIncompatible
	}
	return ensureNoAdminEndpointArtifacts(stateDir)
}

func rollbackUnfinishedAdminEndpoint(
	stateDir string,
	evidence adminEndpointEvidence,
	caCertificate *x509.Certificate,
	queryer adminQueryer,
	ctx context.Context,
	syncer func(string) error,
) error {
	var relayURL string
	if err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'relay_url'`).Scan(&relayURL); err != nil {
		return ErrAdminStateIncompatible
	}
	origin, err := pairing.RelayOrigin(relayURL)
	if err != nil || relayURL != origin.String() || origin.Hostname() == "" {
		return ErrAdminStateIncompatible
	}
	certificatePrimary := filepath.Join(stateDir, relayCertFilename)
	keyPrimary := filepath.Join(stateDir, relayKeyFilename)
	primaryCertificate, primaryCertificatePresent, err := readOptionalRegularAdminStateFile(certificatePrimary)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	primaryKey, primaryKeyPresent, err := readOptionalRegularAdminStateFile(keyPrimary)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	oldCertificate, oldCertificatePresent, err := readOptionalRegularAdminStateFile(evidence.paths.certificateOld)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	oldKey, oldKeyPresent, err := readOptionalRegularAdminStateFile(evidence.paths.keyOld)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	newCertificate, newCertificatePresent, err := readOptionalRegularAdminStateFile(evidence.paths.certificateNew)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	newKey, newKeyPresent, err := readOptionalRegularAdminStateFile(evidence.paths.keyNew)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	if !validUnfinishedAdminEndpointLayout(adminEndpointLayoutFromPresence(
		primaryCertificatePresent, primaryKeyPresent, oldCertificatePresent, oldKeyPresent, newCertificatePresent, newKeyPresent,
	)) {
		return ErrAdminStateIncompatible
	}

	oldCertificatePath := certificatePrimary
	oldCertificatePEM := primaryCertificate
	if oldCertificatePresent {
		oldCertificatePath = evidence.paths.certificateOld
		oldCertificatePEM = oldCertificate
	} else if !primaryCertificatePresent {
		return ErrAdminStateIncompatible
	}
	oldKeyPath := keyPrimary
	oldPrivateKeyPEM := primaryKey
	if oldKeyPresent {
		oldKeyPath = evidence.paths.keyOld
		oldPrivateKeyPEM = oldKey
	} else if !primaryKeyPresent {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(oldCertificatePEM, oldPrivateKeyPEM, caCertificate, origin.Hostname(), ""); err != nil {
		return ErrAdminStateIncompatible
	}

	type component struct {
		path string
		data []byte
	}
	var remainingCertificates, remainingKeys []component
	if primaryCertificatePresent && oldCertificatePath != certificatePrimary {
		remainingCertificates = append(remainingCertificates, component{certificatePrimary, primaryCertificate})
	}
	if newCertificatePresent {
		remainingCertificates = append(remainingCertificates, component{evidence.paths.certificateNew, newCertificate})
	}
	if primaryKeyPresent && oldKeyPath != keyPrimary {
		remainingKeys = append(remainingKeys, component{keyPrimary, primaryKey})
	}
	if newKeyPresent {
		remainingKeys = append(remainingKeys, component{evidence.paths.keyNew, newKey})
	}
	if len(remainingCertificates) > 1 || len(remainingKeys) > 1 {
		return ErrAdminStateIncompatible
	}
	if len(remainingCertificates) == 1 && bytes.Equal(remainingCertificates[0].data, oldCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	if len(remainingKeys) == 1 && bytes.Equal(remainingKeys[0].data, oldPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	switch {
	case len(remainingCertificates) == 1 && len(remainingKeys) == 1:
		if _, err := validateRelayEndpointIdentity(remainingCertificates[0].data, remainingKeys[0].data, caCertificate, "", ""); err != nil {
			return ErrAdminStateIncompatible
		}
	case len(remainingCertificates) == 1:
		if _, err := validateRelayEndpointCertificate(remainingCertificates[0].data, caCertificate, "", ""); err != nil {
			return ErrAdminStateIncompatible
		}
	case len(remainingKeys) == 1:
		if err := validateRelayEndpointPrivateKey(remainingKeys[0].data); err != nil {
			return ErrAdminStateIncompatible
		}
	}
	if err := restoreAdminEndpointSource(stateDir, certificatePrimary, oldCertificatePath, oldCertificatePEM, syncer); err != nil {
		return err
	}
	if err := restoreAdminEndpointSource(stateDir, keyPrimary, oldKeyPath, oldPrivateKeyPEM, syncer); err != nil {
		return err
	}
	for _, artifact := range []struct {
		path     string
		contents []byte
		present  bool
	}{
		{evidence.paths.keyNew, newKey, newKeyPresent},
		{evidence.paths.certificateNew, newCertificate, newCertificatePresent},
	} {
		if artifact.present {
			if err := removeAdminEndpointArtifactExactUsing(stateDir, artifact.path, artifact.contents, syncer); err != nil {
				return err
			}
		}
	}
	certificatePEM, err := readRegularAdminStateFile(certificatePrimary)
	if err != nil || !bytes.Equal(certificatePEM, oldCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, err := readRegularAdminStateFile(keyPrimary)
	if err != nil || !bytes.Equal(privateKeyPEM, oldPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, caCertificate, origin.Hostname(), ""); err != nil {
		return ErrAdminStateIncompatible
	}
	return ensureNoAdminEndpointArtifacts(stateDir)
}

type adminEndpointLayout uint8

const (
	adminEndpointPrimaryCertificate adminEndpointLayout = 1 << iota
	adminEndpointPrimaryKey
	adminEndpointOldCertificate
	adminEndpointOldKey
	adminEndpointNewCertificate
	adminEndpointNewKey

	adminEndpointForwardF1 = adminEndpointPrimaryCertificate | adminEndpointPrimaryKey | adminEndpointNewCertificate
	adminEndpointForwardF2 = adminEndpointForwardF1 | adminEndpointNewKey
	adminEndpointForwardF3 = adminEndpointPrimaryKey | adminEndpointOldCertificate | adminEndpointNewCertificate | adminEndpointNewKey
	adminEndpointForwardF4 = adminEndpointOldCertificate | adminEndpointOldKey | adminEndpointNewCertificate | adminEndpointNewKey
	adminEndpointForwardF5 = adminEndpointPrimaryCertificate | adminEndpointOldCertificate | adminEndpointOldKey | adminEndpointNewKey
	adminEndpointForwardF6 = adminEndpointPrimaryCertificate | adminEndpointPrimaryKey | adminEndpointOldCertificate | adminEndpointOldKey

	adminEndpointRollbackF4CertificateRestored = adminEndpointPrimaryCertificate | adminEndpointOldKey | adminEndpointNewCertificate | adminEndpointNewKey
	adminEndpointRollbackF5CertificateRemoved  = adminEndpointOldCertificate | adminEndpointOldKey | adminEndpointNewKey
	adminEndpointRollbackF5CertificateRestored = adminEndpointPrimaryCertificate | adminEndpointOldKey | adminEndpointNewKey
	adminEndpointRollbackF5KeyRestored         = adminEndpointPrimaryCertificate | adminEndpointPrimaryKey | adminEndpointNewKey
	adminEndpointRollbackF6CertificateRemoved  = adminEndpointPrimaryKey | adminEndpointOldCertificate | adminEndpointOldKey
	adminEndpointRollbackF6CertificateRestored = adminEndpointPrimaryCertificate | adminEndpointPrimaryKey | adminEndpointOldKey
	adminEndpointRollbackF6KeyRemoved          = adminEndpointPrimaryCertificate | adminEndpointOldKey

	adminEndpointCompletedOldKeyRemoved = adminEndpointPrimaryCertificate | adminEndpointPrimaryKey | adminEndpointOldCertificate
)

func adminEndpointLayoutFromPresence(primaryCertificate, primaryKey, oldCertificate, oldKey, newCertificate, newKey bool) adminEndpointLayout {
	var layout adminEndpointLayout
	if primaryCertificate {
		layout |= adminEndpointPrimaryCertificate
	}
	if primaryKey {
		layout |= adminEndpointPrimaryKey
	}
	if oldCertificate {
		layout |= adminEndpointOldCertificate
	}
	if oldKey {
		layout |= adminEndpointOldKey
	}
	if newCertificate {
		layout |= adminEndpointNewCertificate
	}
	if newKey {
		layout |= adminEndpointNewKey
	}
	return layout
}

func validUnfinishedAdminEndpointLayout(layout adminEndpointLayout) bool {
	switch layout {
	case adminEndpointForwardF1,
		adminEndpointForwardF2,
		adminEndpointForwardF3,
		adminEndpointForwardF4,
		adminEndpointForwardF5,
		adminEndpointForwardF6,
		adminEndpointRollbackF4CertificateRestored,
		adminEndpointRollbackF5CertificateRemoved,
		adminEndpointRollbackF5CertificateRestored,
		adminEndpointRollbackF5KeyRestored,
		adminEndpointRollbackF6CertificateRemoved,
		adminEndpointRollbackF6CertificateRestored,
		adminEndpointRollbackF6KeyRemoved:
		return true
	default:
		return false
	}
}

func validCompletedAdminEndpointLayout(layout adminEndpointLayout) bool {
	// The postcommit namespace starts with the promoted primary plus both old
	// components, then removes the old key before the old certificate.
	return layout == adminEndpointForwardF6 || layout == adminEndpointCompletedOldKeyRemoved
}

func restoreAdminEndpointSource(stateDir, primary, source string, expected []byte, syncer func(string) error) error {
	sourceContents, sourcePresent, err := readOptionalRegularAdminStateFile(source)
	if err != nil || !sourcePresent || !bytes.Equal(sourceContents, expected) {
		return ErrAdminStateIncompatible
	}
	if source == primary {
		return nil
	}
	if primaryContents, primaryPresent, err := readOptionalRegularAdminStateFile(primary); err != nil {
		return err
	} else if primaryPresent {
		if err := removeAdminEndpointArtifactExactUsing(stateDir, primary, primaryContents, syncer); err != nil {
			return err
		}
	}
	if err := os.Rename(source, primary); err != nil {
		return err
	}
	return syncer(stateDir)
}

func removeAdminEndpointArtifactExact(stateDir, path string, expected []byte) error {
	return removeAdminEndpointArtifactExactUsing(stateDir, path, expected, syncDirectory)
}

func removeAdminEndpointArtifactExactUsing(stateDir, path string, expected []byte, syncer func(string) error) error {
	contents, present, err := readOptionalRegularAdminStateFile(path)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if !bytes.Equal(contents, expected) {
		return ErrAdminStateIncompatible
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncer(stateDir)
}

func scanAdminEndpointEvidence(stateDir string) (adminEndpointEvidence, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return adminEndpointEvidence{}, err
	}
	known := map[string]bool{
		caCertFilename: true, caKeyFilename: true, relayCertFilename: true, relayKeyFilename: true,
		databaseFilename: true, databaseFilename + "-wal": true, databaseFilename + "-shm": true,
	}
	evidence := adminEndpointEvidence{present: make(map[string]bool)}
	allPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(stateDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !adminFileHasSingleLink(info) {
			return adminEndpointEvidence{}, ErrAdminStateIncompatible
		}
		allPaths = append(allPaths, path)
		if known[name] {
			continue
		}
		requestID, suffix, ok := parseAdminEndpointArtifactName(name)
		if !ok || info.Size() <= 0 || info.Size() > maximumAdminEndpointArtifactSize {
			return adminEndpointEvidence{}, ErrAdminStateIncompatible
		}
		if evidence.requestID != "" && evidence.requestID != requestID {
			return adminEndpointEvidence{}, ErrAdminStateIncompatible
		}
		evidence.requestID = requestID
		evidence.present[suffix] = true
	}
	for left := 0; left < len(allPaths); left++ {
		leftInfo, err := os.Lstat(allPaths[left])
		if err != nil {
			return adminEndpointEvidence{}, ErrAdminStateIncompatible
		}
		for right := left + 1; right < len(allPaths); right++ {
			rightInfo, err := os.Lstat(allPaths[right])
			if err != nil || os.SameFile(leftInfo, rightInfo) {
				return adminEndpointEvidence{}, ErrAdminStateIncompatible
			}
		}
	}
	if evidence.requestID != "" {
		evidence.paths = adminEndpointArtifactPaths(stateDir, evidence.requestID)
	}
	return evidence, nil
}

func adminFileHasSingleLink(info os.FileInfo) bool {
	if info == nil || info.Sys() == nil {
		return true
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return true
	}
	links := value.FieldByName("Nlink")
	if !links.IsValid() {
		return true
	}
	switch links.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return links.Uint() == 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return links.Int() == 1
	default:
		return false
	}
}

func parseAdminEndpointArtifactName(name string) (string, string, bool) {
	const prefix = ".relay-rotate-"
	if !strings.HasPrefix(name, prefix) {
		return "", "", false
	}
	for _, suffix := range []string{".crt.new", ".key.new", ".crt.old", ".key.old"} {
		if !strings.HasSuffix(name, suffix) || len(name) != len(prefix)+32+len(suffix) {
			continue
		}
		requestID := name[len(prefix) : len(name)-len(suffix)]
		if relayadmin.ValidateRequestID(requestID) != nil {
			return "", "", false
		}
		return requestID, suffix, true
	}
	return "", "", false
}

func ensureNoAdminEndpointArtifacts(stateDir string) error {
	evidence, err := scanAdminEndpointEvidence(stateDir)
	if err != nil {
		return err
	}
	if evidence.requestID != "" {
		return ErrAdminStateIncompatible
	}
	return nil
}

func validateRelayEndpointIdentityFromFiles(
	certificatePath, keyPath string,
	caCertificate *x509.Certificate,
	hostname, serial string,
) (*x509.Certificate, error) {
	certificatePEM, err := readRegularAdminStateFile(certificatePath)
	if err != nil {
		return nil, err
	}
	privateKeyPEM, err := readRegularAdminStateFile(keyPath)
	if err != nil {
		return nil, err
	}
	return validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, caCertificate, hostname, serial)
}

func validateRelayEndpointCertificate(certificatePEM []byte, caCertificate *x509.Certificate, hostname, expectedSerial string) (*x509.Certificate, error) {
	block, rest := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrAdminStateIncompatible
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	publicKey, keyOK := certificatePublicKey(certificate)
	if err != nil || !keyOK || publicKey.Curve != elliptic.P256() || certificate.IsCA || bytes.Equal(certificate.Raw, caCertificate.Raw) ||
		certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 ||
		!certificateHasExtendedKeyUsage(certificate, x509.ExtKeyUsageServerAuth) ||
		expectedSerial != "" && strings.ToUpper(certificate.SerialNumber.Text(16)) != expectedSerial {
		return nil, ErrAdminStateIncompatible
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCertificate)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots: roots, DNSName: hostname, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return nil, ErrAdminStateIncompatible
	}
	return certificate, nil
}

func certificateHasExtendedKeyUsage(certificate *x509.Certificate, usage x509.ExtKeyUsage) bool {
	if certificate == nil {
		return false
	}
	for _, candidate := range certificate.ExtKeyUsage {
		if candidate == usage {
			return true
		}
	}
	return false
}

func validateRelayEndpointPrivateKey(privateKeyPEM []byte) error {
	block, rest := pem.Decode(privateKeyPEM)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return ErrAdminStateIncompatible
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	signer, ok := key.(crypto.Signer)
	privateKey, privateKeyOK := signer.(*ecdsa.PrivateKey)
	if !ok || !privateKeyOK || privateKey.Curve != elliptic.P256() {
		return ErrAdminStateIncompatible
	}
	return nil
}

func certificatePublicKey(certificate *x509.Certificate) (*ecdsa.PublicKey, bool) {
	if certificate == nil {
		return nil, false
	}
	key, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	return key, ok
}

func captureAdminRecoveryInvariant(ctx context.Context, queryer adminQueryer, stateDir, excludedRequestID string) (adminRecoveryInvariant, error) {
	caCertificatePEM, err := readRegularAdminStateFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		return adminRecoveryInvariant{}, err
	}
	caPrivateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		return adminRecoveryInvariant{}, err
	}
	if _, _, err := parseCertificateAuthorityState(caCertificatePEM, caPrivateKeyPEM); err != nil {
		return adminRecoveryInvariant{}, ErrAdminStateIncompatible
	}
	hash := sha256.New()
	queries := []struct {
		statement string
		args      []any
	}{
		{`SELECT serial, role, created_at, last_seen_at, revoked_at FROM identities ORDER BY serial`, nil},
		{`SELECT capability_hash, role, created_at, expires_at, consumed_at FROM pairing_capabilities ORDER BY capability_hash`, nil},
		{`SELECT singleton_id, total_streams, byte_count FROM metrics ORDER BY singleton_id`, nil},
		{`SELECT code, count FROM error_metrics ORDER BY code`, nil},
		{`SELECT key, value FROM settings ORDER BY key`, nil},
		{`SELECT capability_hash, relay_url, created_at, expires_at, consumed_at FROM endpoint_migrations ORDER BY capability_hash`, nil},
		{`SELECT request_id, digest, operation, state, response, created_at FROM admin_mutation_replay WHERE request_id <> ? ORDER BY request_id`, []any{excludedRequestID}},
	}
	for _, query := range queries {
		if err := hashAdminQueryRows(ctx, hash, queryer, query.statement, query.args...); err != nil {
			return adminRecoveryInvariant{}, err
		}
	}
	var databaseHash [sha256.Size]byte
	copy(databaseHash[:], hash.Sum(nil))
	return adminRecoveryInvariant{
		caCertificatePEM: append([]byte(nil), caCertificatePEM...),
		caPrivateKeyPEM:  append([]byte(nil), caPrivateKeyPEM...),
		databaseHash:     databaseHash,
	}, nil
}

func hashAdminQueryRows(ctx context.Context, hash interface{ Write([]byte) (int, error) }, queryer adminQueryer, statement string, args ...any) error {
	rows, err := queryer.QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	_, _ = hash.Write([]byte(statement))
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		for _, value := range values {
			var kind byte
			var encoded []byte
			switch typed := value.(type) {
			case nil:
				kind = 0
			case int64:
				kind, encoded = 1, []byte(strconv.FormatInt(typed, 10))
			case float64:
				kind, encoded = 2, []byte(strconv.FormatFloat(typed, 'g', -1, 64))
			case bool:
				kind, encoded = 3, []byte(strconv.FormatBool(typed))
			case []byte:
				kind, encoded = 4, typed
			case string:
				kind, encoded = 5, []byte(typed)
			default:
				return fmt.Errorf("unsupported SQLite value type %T", value)
			}
			_, _ = hash.Write([]byte{kind})
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(encoded)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write(encoded)
		}
	}
	return rows.Err()
}

func equalAdminRecoveryInvariant(left, right adminRecoveryInvariant) bool {
	return bytes.Equal(left.caCertificatePEM, right.caCertificatePEM) &&
		bytes.Equal(left.caPrivateKeyPEM, right.caPrivateKeyPEM) && left.databaseHash == right.databaseHash
}

func classifyAdminRecoveryError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, ErrAdminStateIncompatible) || errors.Is(err, relayadmin.ErrReplayState) || errors.Is(err, sql.ErrNoRows) {
		return ErrAdminStateIncompatible
	}
	return relayadmin.ErrMutationIndeterminate
}
