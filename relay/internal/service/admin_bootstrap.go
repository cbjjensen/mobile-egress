package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/enrollment"
)

type adminSetupStage struct {
	dir      string
	database *store
	tx       *sql.Tx
	ownerUID uint32
}

func (state *AdminState) BootstrapOwner(
	ctx context.Context,
	mutation relayadmin.MutationTransaction,
	options AdminBootstrapOwnerOptions,
) (result EnrollmentResult, returnErr error) {
	transaction, err := state.adminTransaction(ctx, mutation, relayadmin.OperationSetup)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if options.AdministrativeOwnerUID == 0 {
		return EnrollmentResult{}, ErrAdminStateIncompatible
	}
	origin, err := validateRelayOrigin(options.PublicURL, options.PublicName)
	if err != nil {
		return EnrollmentResult{}, err
	}
	publicKey, err := parseDevicePublicKey(options.CSRPEM, "")
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("parse Owner certificate request: %w", err)
	}
	stateDir, err := validateInitOptions(InitOptions{
		StateDir: state.stateDir, PublicName: options.PublicName, PublicURL: origin,
	})
	if err != nil {
		return EnrollmentResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return EnrollmentResult{}, err
	}

	state.mu.Lock()
	presence := state.presence
	closed := state.closed
	state.mu.Unlock()
	if closed || presence == adminStatePresenceDegraded {
		return EnrollmentResult{}, ErrAdminStateIncompatible
	}
	if presence == adminStatePresenceReady {
		return EnrollmentResult{}, ErrAdminAlreadyInitialized
	}
	if _, err := os.Lstat(stateDir); err == nil {
		state.markAdminDegraded()
		return EnrollmentResult{}, ErrAdminStateIncompatible
	} else if !errors.Is(err, os.ErrNotExist) {
		state.markAdminDegraded()
		return EnrollmentResult{}, ErrAdminStateIncompatible
	}
	parent := filepath.Dir(stateDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return EnrollmentResult{}, fmt.Errorf("create relay admin state parent: %w", err)
	}
	stageDir := filepath.Join(parent, ".relay-setup-"+transaction.key.RequestID)
	if _, err := os.Lstat(stageDir); err == nil {
		state.markAdminDegraded()
		return EnrollmentResult{}, ErrAdminStateIncompatible
	} else if !errors.Is(err, os.ErrNotExist) {
		state.markAdminDegraded()
		return EnrollmentResult{}, ErrAdminStateIncompatible
	}
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return EnrollmentResult{}, fmt.Errorf("create relay admin setup stage: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			if err := state.removeSetupStageDirectory(stageDir); err != nil {
				state.retainAdminUncertain(transaction.key)
				result = EnrollmentResult{}
				returnErr = relayadmin.ErrMutationIndeterminate
			}
		}
	}()

	caCertPEM, caKeyPEM, relayCertPEM, relayKeyPEM, err := generateCertificateState(options.PublicName, time.Now().UTC())
	if err != nil {
		return EnrollmentResult{}, err
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{name: caCertFilename, data: caCertPEM, mode: 0o644},
		{name: caKeyFilename, data: caKeyPEM, mode: 0o600},
		{name: relayCertFilename, data: relayCertPEM, mode: 0o644},
		{name: relayKeyFilename, data: relayKeyPEM, mode: 0o600},
	}
	for _, file := range files {
		if err := writeDurableFile(filepath.Join(stageDir, file.name), file.data, file.mode); err != nil {
			return EnrollmentResult{}, fmt.Errorf("write relay admin setup state: %w", err)
		}
	}
	caCert, caKey, err := parseCertificateAuthorityState(caCertPEM, caKeyPEM)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if hook := state.beforeSetupDatabase; hook != nil {
		if err := hook(); err != nil {
			return EnrollmentResult{}, err
		}
	}
	database, err := createStore(filepath.Join(stageDir, databaseFilename))
	if err != nil {
		return EnrollmentResult{}, err
	}
	setupTx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		database.Close()
		return EnrollmentResult{}, fmt.Errorf("begin relay admin setup transaction: %w", err)
	}

	uid := options.AdministrativeOwnerUID
	result, err = issueOwnerInTransaction(ctx, setupTx, caCert, caKey, caCertPEM, publicKey, origin, &uid)
	if err != nil {
		setupTx.Rollback()
		database.Close()
		return EnrollmentResult{}, err
	}
	transaction.tx = setupTx
	transaction.setup = &adminSetupStage{dir: stageDir, database: database, tx: setupTx, ownerUID: uid}
	cleanup = false
	return result, nil
}

func (state *AdminState) adminTransaction(ctx context.Context, value relayadmin.MutationTransaction, operation relayadmin.Operation) (*adminMutationTransaction, error) {
	transaction, ok := value.(*adminMutationTransaction)
	if !ok || transaction == nil || transaction.state != state || transaction.reservation == nil ||
		transaction.key.Operation != operation || transaction.reservation.key != transaction.key {
		return nil, errAdminForeignTransaction
	}
	state.mu.Lock()
	active := state.active[transaction.key.RequestID] == transaction.reservation && transaction.reservation.started
	state.mu.Unlock()
	if !active {
		return nil, errAdminForeignTransaction
	}
	if transaction.database != nil {
		if err := transaction.ensureTransaction(ctx); err != nil {
			return nil, errAdminForeignTransaction
		}
	}
	return transaction, nil
}

func issueOwnerInTransaction(
	ctx context.Context,
	transaction *sql.Tx,
	caCert *x509.Certificate,
	caKey crypto.Signer,
	caCertPEM []byte,
	publicKey crypto.PublicKey,
	origin string,
	uid *uint32,
) (EnrollmentResult, error) {
	var ownerCount int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities WHERE role = 'owner' AND revoked_at IS NULL`).Scan(&ownerCount); err != nil {
		return EnrollmentResult{}, fmt.Errorf("count Owner identities: %w", err)
	}
	if ownerCount != 0 {
		return EnrollmentResult{}, ErrAdminAlreadyInitialized
	}
	now := time.Now().UTC()
	serialNumber, err := randomSerial()
	if err != nil {
		return EnrollmentResult{}, err
	}
	serial := strings.ToUpper(serialNumber.Text(16))
	certificatePEM, err := signDeviceCertificate(caCert, caKey, publicKey, enrollment.RoleOwner, serialNumber, serial, now)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("issue Owner certificate: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO identities(serial, role, created_at) VALUES (?, 'owner', ?)`, serial, now.Unix()); err != nil {
		return EnrollmentResult{}, fmt.Errorf("persist Owner identity: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES ('relay_url', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, origin); err != nil {
		return EnrollmentResult{}, fmt.Errorf("persist relay URL: %w", err)
	}
	if uid != nil {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES ('administrative_owner_uid', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatUint(uint64(*uid), 10)); err != nil {
			return EnrollmentResult{}, fmt.Errorf("persist administrative Owner UID: %w", err)
		}
	}
	return EnrollmentResult{
		CertificatePEM:   string(certificatePEM) + string(caCertPEM),
		CACertificatePEM: string(caCertPEM),
		Serial:           serial,
		Role:             enrollment.RoleOwner,
	}, nil
}

func validateCompletedAdminSetupStage(stageDir string, key relayadmin.ReplayKey, response []byte) error {
	if err := validateAdminSetupStageContents(stageDir, true); err != nil {
		return ErrAdminStateIncompatible
	}
	database, err := openStore(filepath.Join(stageDir, databaseFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	closeDatabase := true
	defer func() {
		if closeDatabase {
			_ = database.Close()
		}
	}()
	if err := database.validSchema(context.Background()); err != nil {
		return ErrAdminStateIncompatible
	}
	if !adminSetupDatabaseTablesExact(database.db) {
		return ErrAdminStateIncompatible
	}
	var count int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM admin_mutation_replay`).Scan(&count); err != nil || count != 1 {
		return ErrAdminStateIncompatible
	}
	var requestID, operation, replayState string
	var digest, persistedResponse []byte
	if err := database.db.QueryRow(`SELECT request_id, digest, operation, state, response FROM admin_mutation_replay`).
		Scan(&requestID, &digest, &operation, &replayState, &persistedResponse); err != nil {
		return ErrAdminStateIncompatible
	}
	if requestID != key.RequestID || operation != string(key.Operation) || replayState != "completed" ||
		!equalDigest(digest, key.Digest[:]) || !bytes.Equal(persistedResponse, response) || !validCachedAdminResponse(key, persistedResponse) {
		return ErrAdminStateIncompatible
	}
	snapshot, err := adminSnapshotFromQuery(context.Background(), database.db, stageDir)
	if err != nil || snapshot.Class != AdminStateReady {
		return ErrAdminStateIncompatible
	}
	if err := database.Close(); err != nil {
		return ErrAdminStateIncompatible
	}
	closeDatabase = false
	return nil
}

func recoverAdminSetupStage(stateDir string, syncParent func(string) error) (bool, error) {
	parent := filepath.Dir(stateDir)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect relay admin setup recovery: %w", err)
	}
	var stageDir, requestID string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".relay-setup-") {
			continue
		}
		candidateID := strings.TrimPrefix(entry.Name(), ".relay-setup-")
		if requestID != "" || relayadmin.ValidateRequestID(candidateID) != nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return false, ErrAdminStateIncompatible
		}
		requestID = candidateID
		stageDir = filepath.Join(parent, entry.Name())
	}
	if requestID == "" {
		return false, nil
	}
	if err := validateAdminSetupStageContents(stageDir, false); err != nil {
		return false, ErrAdminStateIncompatible
	}
	databasePath := filepath.Join(stageDir, databaseFilename)
	info, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		if err := removeAdminSetupStage(stageDir); err != nil {
			return false, ErrAdminStateIncompatible
		}
		return false, nil
	} else if err != nil {
		return false, ErrAdminStateIncompatible
	}
	if !info.Mode().IsRegular() {
		return false, ErrAdminStateIncompatible
	}
	// SQLite creates state.db before the schema transaction is authoritative.
	// An empty or shorter-than-header file cannot contain committed state, so an
	// exact request-bound stage is safe to discard and deterministically retry.
	if info.Size() < 100 {
		if err := removeAdminSetupStage(stageDir); err != nil {
			return false, ErrAdminStateIncompatible
		}
		return false, nil
	}
	database, err := openDatabase(databasePath)
	if err != nil {
		return false, ErrAdminStateIncompatible
	}
	closeDatabase := true
	defer func() {
		if closeDatabase {
			_ = database.Close()
		}
	}()
	var version int
	if err := database.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if version == 0 {
		var objectCount int
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&objectCount); err != nil || objectCount != 0 {
			return false, ErrAdminStateIncompatible
		}
		if err := database.Close(); err != nil {
			return false, ErrAdminStateIncompatible
		}
		closeDatabase = false
		if err := removeAdminSetupStage(stageDir); err != nil {
			return false, ErrAdminStateIncompatible
		}
		return false, nil
	}
	if version != schemaVersion {
		return false, ErrAdminStateIncompatible
	}
	if err := database.validSchema(context.Background()); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if !adminSetupDatabaseTablesExact(database.db) {
		return false, ErrAdminStateIncompatible
	}
	rows, err := database.db.Query(`SELECT request_id, digest, operation, state, response FROM admin_mutation_replay`)
	if err != nil {
		return false, ErrAdminStateIncompatible
	}
	type replayRow struct {
		requestID string
		digest    []byte
		operation string
		state     string
		response  []byte
	}
	var replayRows []replayRow
	for rows.Next() {
		var row replayRow
		if err := rows.Scan(&row.requestID, &row.digest, &row.operation, &row.state, &row.response); err != nil {
			rows.Close()
			return false, ErrAdminStateIncompatible
		}
		replayRows = append(replayRows, row)
	}
	if err := rows.Close(); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if len(replayRows) == 0 {
		if !pristineAdminSetupDatabase(database.db) {
			return false, ErrAdminStateIncompatible
		}
		if err := database.Close(); err != nil {
			return false, ErrAdminStateIncompatible
		}
		closeDatabase = false
		if err := removeAdminSetupStage(stageDir); err != nil {
			return false, ErrAdminStateIncompatible
		}
		return false, nil
	}
	if len(replayRows) != 1 {
		return false, ErrAdminStateIncompatible
	}
	row := replayRows[0]
	if row.requestID != requestID || row.operation != string(relayadmin.OperationSetup) || row.state != "completed" || len(row.digest) != 32 {
		return false, ErrAdminStateIncompatible
	}
	var digest [32]byte
	copy(digest[:], row.digest)
	key := relayadmin.ReplayKey{RequestID: requestID, Digest: digest, Operation: relayadmin.OperationSetup}
	response, err := relayadmin.ParseResponse(row.response)
	if err != nil || !response.OK {
		return false, ErrAdminStateIncompatible
	}
	if _, ok := response.Result.(relayadmin.OwnerBootstrapResult); !ok || !validCachedAdminResponse(key, row.response) {
		return false, ErrAdminStateIncompatible
	}
	snapshot, err := adminSnapshotFromQuery(context.Background(), database.db, stageDir)
	if err != nil || snapshot.Class != AdminStateReady {
		return false, ErrAdminStateIncompatible
	}
	if err := validateAdminSetupStageContents(stageDir, true); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if err := database.Close(); err != nil {
		return false, ErrAdminStateIncompatible
	}
	closeDatabase = false
	if err := syncAdminSetupStage(stageDir); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if err := os.Rename(stageDir, stateDir); err != nil {
		return false, ErrAdminStateIncompatible
	}
	if err := syncParent(parent); err != nil {
		return true, ErrAdminStateIncompatible
	}
	return true, nil
}

func pristineAdminSetupDatabase(database *sql.DB) bool {
	var identities, capabilities, settings, migrations, errorsCount int
	if err := database.QueryRow(`SELECT
        (SELECT COUNT(*) FROM identities),
        (SELECT COUNT(*) FROM pairing_capabilities),
        (SELECT COUNT(*) FROM settings),
        (SELECT COUNT(*) FROM endpoint_migrations),
        (SELECT COUNT(*) FROM error_metrics)`).
		Scan(&identities, &capabilities, &settings, &migrations, &errorsCount); err != nil {
		return false
	}
	if identities != 0 || capabilities != 0 || settings != 0 || migrations != 0 || errorsCount != 0 {
		return false
	}
	var metricsRows, nonzeroMetrics int
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN singleton_id <> 1 OR total_streams <> 0 OR byte_count <> 0 THEN 1 ELSE 0 END), 0) FROM metrics`).
		Scan(&metricsRows, &nonzeroMetrics); err != nil || metricsRows != 1 || nonzeroMetrics != 0 {
		return false
	}
	return true
}

func adminSetupDatabaseTablesExact(database *sql.DB) bool {
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return false
	}
	return count == 7
}

func adminSetupStagePresent(stateDir string) (bool, error) {
	entries, err := os.ReadDir(filepath.Dir(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".relay-setup-") {
			return true, nil
		}
	}
	return false, nil
}
