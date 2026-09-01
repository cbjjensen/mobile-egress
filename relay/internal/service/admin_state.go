package service

import (
	"bytes"
	"container/list"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/pairing"
	"mobile-egress/relay/internal/enrollment"
)

var (
	ErrAdminNotInitialized     = errors.New("relay admin state not initialized")
	ErrAdminAlreadyInitialized = errors.New("relay admin state already initialized")
	ErrAdminStateIncompatible  = errors.New("relay admin state incompatible")
	errAdminForeignTransaction = errors.New("relay admin mutation transaction is invalid")
)

type AdminStateClass uint8

const (
	AdminStateAbsent AdminStateClass = iota
	AdminStateReady
	AdminStateIncompatible
)

type adminStatePresence uint8

const (
	adminStatePresenceAbsent adminStatePresence = iota
	adminStatePresenceReady
	adminStatePresenceDegraded
)

type AdminSnapshot struct {
	Class                  AdminStateClass
	AdministrativeOwnerUID uint32
	OwnerUIDBound          bool
}

type AdminPathGuard interface {
	Validate(context.Context) error
	Repair(context.Context) error
}

type AdminStateOptions struct {
	StateDir         string
	MutationCapacity int
	PathGuard        AdminPathGuard
	MutationFinished func(relayadmin.ReplayKey)
	syncSetupParent  func(string) error
}

type AdminBootstrapOwnerOptions struct {
	PublicName             string
	PublicURL              string
	CSRPEM                 string
	AdministrativeOwnerUID uint32
}

type AdminState struct {
	stateDir         string
	pathGuard        AdminPathGuard
	mutationFinished func(relayadmin.ReplayKey)
	mutationCapacity int
	syncSetupParent  func(string) error
	statusReplay     *relayadmin.MemoryReplayStore
	replay           *adminReplayStore

	mutationGate chan struct{}
	mu           sync.Mutex
	database     *store
	presence     adminStatePresence
	degraded     AdminSnapshot
	replayReady  bool
	closed       bool
	active       map[string]*adminMutationReservation
	pending      map[string]relayadmin.ReplayKey
	mutationKeys map[string]relayadmin.ReplayKey
	fallback     map[string]adminCompletedReplay
	uncertain    map[string]relayadmin.ReplayKey
	status       map[string]*adminStatusEntry
	statusLRU    list.List

	// Test-only fault seams are unexported so production construction cannot
	// weaken durability. Slice-one tests set them only from this package.
	beforeSetupCommit        func() error
	beforeSetupDatabase      func() error
	beforeSetupRename        func() error
	beforeSetupReopen        func() error
	afterSetupRename         func() error
	afterMutationGateAcquire func()
	beforeReplayDatabase     func(relayadmin.ReplayKey)
	removeSetupStage         func(string) error
	now                      func() time.Time
	endpointFault            func(adminEndpointFaultPoint) error
	syncEndpointDirectory    func(string) error
	commitAdminMutation      func(*sql.Tx) error
	makeSetupParent          func(string, os.FileMode) error
	writeSetupFile           func(string, []byte, os.FileMode) error
}

func OpenAdminState(options AdminStateOptions) (*AdminState, error) {
	stateDir := filepath.Clean(strings.TrimSpace(options.StateDir))
	if options.StateDir == "" || stateDir == "." {
		return nil, errors.New("relay admin state directory is required")
	}
	capacity := options.MutationCapacity
	if capacity <= 0 || capacity > relayadmin.MutationReplayCapacity {
		capacity = relayadmin.MutationReplayCapacity
	}
	syncSetupParent := options.syncSetupParent
	if syncSetupParent == nil {
		syncSetupParent = syncDirectory
	}
	state := &AdminState{
		stateDir:              stateDir,
		pathGuard:             options.PathGuard,
		mutationFinished:      options.MutationFinished,
		mutationCapacity:      capacity,
		syncSetupParent:       syncSetupParent,
		statusReplay:          relayadmin.NewMemoryReplayStore(relayadmin.MemoryReplayConfig{}),
		mutationGate:          make(chan struct{}, 1),
		active:                make(map[string]*adminMutationReservation),
		pending:               make(map[string]relayadmin.ReplayKey),
		mutationKeys:          make(map[string]relayadmin.ReplayKey),
		fallback:              make(map[string]adminCompletedReplay),
		uncertain:             make(map[string]relayadmin.ReplayKey),
		status:                make(map[string]*adminStatusEntry),
		now:                   func() time.Time { return time.Now().UTC() },
		syncEndpointDirectory: syncDirectory,
		commitAdminMutation: func(transaction *sql.Tx) error {
			return transaction.Commit()
		},
		makeSetupParent: os.MkdirAll,
		writeSetupFile:  writeDurableFile,
	}
	state.mutationGate <- struct{}{}
	state.replay = &adminReplayStore{state: state}
	if options.PathGuard != nil {
		if err := options.PathGuard.Validate(context.Background()); err != nil {
			state.presence = adminStatePresenceDegraded
			return state, nil
		}
	}

	info, err := os.Stat(stateDir)
	recoveryUncertain := false
	if errors.Is(err, os.ErrNotExist) {
		promoted, recoveryErr := recoverAdminSetupStage(stateDir, state.syncSetupParent)
		if recoveryErr != nil {
			if !promoted {
				state.presence = adminStatePresenceDegraded
				return state, nil
			}
			recoveryUncertain = true
		}
		if !promoted {
			return state, nil
		}
		info, err = os.Stat(stateDir)
	}
	if err != nil {
		state.presence = adminStatePresenceDegraded
		return state, nil
	}
	// Once the final namespace exists it is authoritative even when its schema,
	// files, or replay database cannot be reopened. Never infer freshness from
	// a nil database handle after this point.
	state.presence = adminStatePresenceDegraded
	if staged, stageErr := adminSetupStagePresent(stateDir); stageErr != nil || staged {
		return state, nil
	}
	if !info.IsDir() {
		return state, nil
	}
	database, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		return state, nil
	}
	state.database = database
	if err := database.validSchema(context.Background()); err != nil {
		state.degraded = canonicalAdminBinding(context.Background(), database.db)
		return state, nil
	}
	mutationKeys, err := loadAdminMutationKeys(context.Background(), database)
	if err != nil {
		state.degraded = canonicalAdminBinding(context.Background(), database.db)
		return state, nil
	}
	state.mutationKeys = mutationKeys
	state.replayReady = true
	snapshot, err := adminSnapshotFromQuery(context.Background(), database.db, stateDir)
	evidence, inventoryErr := scanAdminEndpointEvidence(stateDir)
	endpointInventoryClean := inventoryErr == nil && evidence.requestID == ""
	if err == nil && snapshot.Class == AdminStateReady && !recoveryUncertain && endpointInventoryClean {
		state.presence = adminStatePresenceReady
		snapshot.Class = AdminStateIncompatible
		state.degraded = snapshot
	} else if err == nil {
		snapshot.Class = AdminStateIncompatible
		state.degraded = snapshot
	} else {
		state.degraded = canonicalAdminBinding(context.Background(), database.db)
	}
	return state, nil
}

type adminCompletedReplay struct {
	key      relayadmin.ReplayKey
	response []byte
}

type adminStatusEntry struct {
	key       relayadmin.ReplayKey
	completed bool
	expiresAt time.Time
	lru       *list.Element
}

func (state *AdminState) ReplayStore() relayadmin.ReplayStore {
	if state == nil {
		return nil
	}
	return state.replay
}

func (state *AdminState) Snapshot(ctx context.Context) (AdminSnapshot, error) {
	if state == nil {
		return AdminSnapshot{}, ErrAdminStateIncompatible
	}
	if err := ctx.Err(); err != nil {
		return AdminSnapshot{}, err
	}
	state.mu.Lock()
	database := state.database
	presence := state.presence
	degraded := state.degraded
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return AdminSnapshot{}, ErrAdminStateIncompatible
	}
	switch presence {
	case adminStatePresenceAbsent:
		return AdminSnapshot{Class: AdminStateAbsent}, nil
	case adminStatePresenceDegraded:
		degraded.Class = AdminStateIncompatible
		return degraded, nil
	case adminStatePresenceReady:
		if database == nil {
			state.markAdminDegraded()
			return AdminSnapshot{Class: AdminStateIncompatible}, nil
		}
	default:
		return AdminSnapshot{Class: AdminStateIncompatible}, nil
	}
	select {
	case <-ctx.Done():
		return AdminSnapshot{}, ctx.Err()
	case <-state.mutationGate:
		defer func() { state.mutationGate <- struct{}{} }()
	default:
		// A mutation owns the gate and may be between endpoint namespace
		// transitions while its SQL transaction owns the only database
		// connection. Return the last coherent readiness snapshot instead of
		// inspecting a transient pair or blocking on that transaction.
		state.mu.Lock()
		presence = state.presence
		degraded = state.degraded
		database = state.database
		closed = state.closed
		state.mu.Unlock()
		if closed || presence != adminStatePresenceReady || database == nil {
			degraded.Class = AdminStateIncompatible
			return degraded, nil
		}
		degraded.Class = AdminStateReady
		return degraded, nil
	}
	state.mu.Lock()
	presence = state.presence
	degraded = state.degraded
	database = state.database
	closed = state.closed
	state.mu.Unlock()
	if closed || presence != adminStatePresenceReady || database == nil {
		degraded.Class = AdminStateIncompatible
		return degraded, nil
	}
	evidence, inventoryErr := scanAdminEndpointEvidence(state.stateDir)
	if inventoryErr != nil || evidence.requestID != "" {
		degraded.Class = AdminStateIncompatible
		state.setAdminDegraded(degraded)
		return degraded, nil
	}
	snapshot, err := adminSnapshotFromQuery(ctx, database.db, state.stateDir)
	if err == nil && snapshot.Class != AdminStateReady {
		state.setAdminDegraded(snapshot)
	}
	return snapshot, err
}

func (state *AdminState) markAdminDegraded() {
	state.setAdminDegraded(AdminSnapshot{Class: AdminStateIncompatible})
}

func (state *AdminState) setAdminDegraded(snapshot AdminSnapshot) {
	state.mu.Lock()
	if !state.closed {
		state.presence = adminStatePresenceDegraded
		snapshot.Class = AdminStateIncompatible
		state.degraded = snapshot
	}
	state.mu.Unlock()
}

func (state *AdminState) retainAdminUncertain(key relayadmin.ReplayKey) {
	state.mu.Lock()
	state.presence = adminStatePresenceDegraded
	state.degraded = AdminSnapshot{Class: AdminStateIncompatible}
	state.mutationKeys[key.RequestID] = key
	state.uncertain[key.RequestID] = key
	state.mu.Unlock()
}

func (state *AdminState) removeSetupStageDirectory(stageDir string) error {
	if hook := state.removeSetupStage; hook != nil {
		return hook(stageDir)
	}
	return removeAdminSetupStage(stageDir)
}

func canonicalAdminBinding(ctx context.Context, queryer adminQueryer) AdminSnapshot {
	uid, bound, err := administrativeOwnerUIDFromQuery(ctx, queryer)
	if err != nil || !bound || uid == 0 {
		return AdminSnapshot{Class: AdminStateIncompatible}
	}
	return AdminSnapshot{
		Class: AdminStateIncompatible, AdministrativeOwnerUID: uid, OwnerUIDBound: true,
	}
}

type adminQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func adminSnapshotFromQuery(ctx context.Context, queryer adminQueryer, stateDir string) (AdminSnapshot, error) {
	uid, bound, err := administrativeOwnerUIDFromQuery(ctx, queryer)
	if errors.Is(err, ErrAdminStateIncompatible) {
		return AdminSnapshot{Class: AdminStateIncompatible}, nil
	}
	if err != nil {
		return AdminSnapshot{}, err
	}
	var ownerCount int
	var ownerSerial sql.NullString
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*), MIN(serial) FROM identities WHERE role = 'owner' AND revoked_at IS NULL`).Scan(&ownerCount, &ownerSerial); err != nil {
		return AdminSnapshot{}, err
	}
	snapshot := AdminSnapshot{AdministrativeOwnerUID: uid, OwnerUIDBound: bound}
	if ownerCount != 1 || !ownerSerial.Valid || !validSerial(ownerSerial.String) || !bound || uid == 0 {
		snapshot.Class = AdminStateIncompatible
		return snapshot, nil
	}
	var relayURL string
	if err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'relay_url'`).Scan(&relayURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			snapshot.Class = AdminStateIncompatible
			return snapshot, nil
		}
		return AdminSnapshot{}, err
	}
	origin, err := pairing.RelayOrigin(relayURL)
	if err != nil || relayURL != origin.String() || origin.Hostname() == "" {
		snapshot.Class = AdminStateIncompatible
		return snapshot, nil
	}
	ownerResult, err := completedAdminOwnerResult(ctx, queryer)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return AdminSnapshot{}, ctxErr
		}
		snapshot.Class = AdminStateIncompatible
		return snapshot, nil
	}
	if err := validateAdminCertificateCoherence(stateDir, origin.Hostname(), ownerSerial.String, ownerResult); err != nil {
		snapshot.Class = AdminStateIncompatible
		return snapshot, nil
	}
	snapshot.Class = AdminStateReady
	return snapshot, nil
}

func completedAdminOwnerResult(ctx context.Context, queryer adminQueryer) (relayadmin.OwnerBootstrapResult, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT request_id, response FROM admin_mutation_replay WHERE operation = 'setup' AND state = 'completed'`)
	if err != nil {
		return relayadmin.OwnerBootstrapResult{}, err
	}
	defer rows.Close()
	var ownerResult relayadmin.OwnerBootstrapResult
	successes := 0
	for rows.Next() {
		var requestID string
		var raw []byte
		if err := rows.Scan(&requestID, &raw); err != nil {
			return relayadmin.OwnerBootstrapResult{}, err
		}
		response, err := relayadmin.ParseResponse(raw)
		if err != nil || response.RequestID != requestID || response.Operation != relayadmin.OperationSetup {
			return relayadmin.OwnerBootstrapResult{}, ErrAdminStateIncompatible
		}
		if !response.OK {
			continue
		}
		result, ok := response.Result.(relayadmin.OwnerBootstrapResult)
		if !ok {
			return relayadmin.OwnerBootstrapResult{}, ErrAdminStateIncompatible
		}
		successes++
		ownerResult = result
	}
	if err := rows.Err(); err != nil {
		return relayadmin.OwnerBootstrapResult{}, err
	}
	if successes != 1 {
		return relayadmin.OwnerBootstrapResult{}, ErrAdminStateIncompatible
	}
	return ownerResult, nil
}

func validateAdminCertificateCoherence(stateDir, relayHostname, ownerSerial string, result relayadmin.OwnerBootstrapResult) error {
	if result.Role != string(enrollment.RoleOwner) || result.Serial != ownerSerial {
		return ErrAdminStateIncompatible
	}
	caPEM, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil || !bytes.Equal(caPEM, []byte(result.CACertificatePEM)) {
		return ErrAdminStateIncompatible
	}
	caCertificate, err := pairing.CACertificate(result.CACertificatePEM)
	if err != nil {
		return ErrAdminStateIncompatible
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	if _, _, err := parseCertificateAuthorityState(caPEM, caKeyPEM); err != nil {
		return ErrAdminStateIncompatible
	}

	ownerBlock, ownerRest := pem.Decode([]byte(result.CertificatePEM))
	if ownerBlock == nil || ownerBlock.Type != "CERTIFICATE" {
		return ErrAdminStateIncompatible
	}
	ownerCertificate, err := x509.ParseCertificate(ownerBlock.Bytes)
	if err != nil || strings.ToUpper(ownerCertificate.SerialNumber.Text(16)) != ownerSerial {
		return ErrAdminStateIncompatible
	}
	chainCABlock, ownerRest := pem.Decode(ownerRest)
	if chainCABlock == nil || chainCABlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(ownerRest)) != 0 || !bytes.Equal(chainCABlock.Bytes, caCertificate.Raw) {
		return ErrAdminStateIncompatible
	}
	ownerRoots := x509.NewCertPool()
	ownerRoots.AddCert(caCertificate)
	if _, err := ownerCertificate.Verify(x509.VerifyOptions{
		Roots: ownerRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return ErrAdminStateIncompatible
	}

	relayCertificatePEM, err := os.ReadFile(filepath.Join(stateDir, relayCertFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	relayKeyPEM, err := os.ReadFile(filepath.Join(stateDir, relayKeyFilename))
	if err != nil {
		return ErrAdminStateIncompatible
	}
	relayPair, err := tls.X509KeyPair(relayCertificatePEM, relayKeyPEM)
	if err != nil || len(relayPair.Certificate) == 0 {
		return ErrAdminStateIncompatible
	}
	relayCertificate, err := x509.ParseCertificate(relayPair.Certificate[0])
	if err != nil {
		return ErrAdminStateIncompatible
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCertificate)
	if _, err := relayCertificate.Verify(x509.VerifyOptions{
		DNSName: relayHostname, Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return ErrAdminStateIncompatible
	}
	return nil
}

func (state *AdminState) Close() error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil
	}
	state.closed = true
	database := state.database
	state.database = nil
	state.mu.Unlock()
	if database != nil {
		return database.Close()
	}
	return nil
}

// RotateEndpoint performs request-bound endpoint rotation inside the narrow
// service-owned transaction boundary used by the privileged runtime.
func (state *AdminState) RotateEndpoint(ctx context.Context, mutation relayadmin.MutationTransaction, options RotateEndpointOptions) (RotateEndpointResult, error) {
	transaction, err := state.adminTransaction(ctx, mutation, relayadmin.OperationRotate)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	if err := state.requireAdminReady(ctx, transaction); err != nil {
		return RotateEndpointResult{}, err
	}
	return state.rotateEndpoint(ctx, transaction, options)
}

// Repair performs identity-preserving recovery inside the narrow
// service-owned transaction boundary used by the privileged runtime.
func (state *AdminState) Repair(ctx context.Context, mutation relayadmin.MutationTransaction) error {
	transaction, err := state.adminTransaction(ctx, mutation, relayadmin.OperationRepair)
	if err != nil {
		return err
	}
	state.mu.Lock()
	presence := state.presence
	state.mu.Unlock()
	switch presence {
	case adminStatePresenceAbsent:
		return ErrAdminNotInitialized
	case adminStatePresenceReady, adminStatePresenceDegraded:
		return state.repairAdminState(ctx, transaction)
	default:
		return ErrAdminStateIncompatible
	}
}

func (state *AdminState) requireAdminReady(ctx context.Context, transaction *adminMutationTransaction) error {
	state.mu.Lock()
	presence := state.presence
	state.mu.Unlock()
	switch presence {
	case adminStatePresenceAbsent:
		return ErrAdminNotInitialized
	case adminStatePresenceDegraded:
		return ErrAdminStateIncompatible
	case adminStatePresenceReady:
	default:
		return ErrAdminStateIncompatible
	}
	if transaction != nil && transaction.tx != nil {
		snapshot, err := adminSnapshotFromQuery(ctx, transaction.tx, state.stateDir)
		return classifyAdminReadySnapshot(snapshot, err)
	}
	snapshot, err := state.Snapshot(ctx)
	return classifyAdminReadySnapshot(snapshot, err)
}

func classifyAdminReadySnapshot(snapshot AdminSnapshot, err error) error {
	if err != nil {
		return err
	}
	switch snapshot.Class {
	case AdminStateAbsent:
		return ErrAdminNotInitialized
	case AdminStateIncompatible:
		return ErrAdminStateIncompatible
	case AdminStateReady:
		return nil
	default:
		return ErrAdminStateIncompatible
	}
}

func (state *store) setAdministrativeOwnerUID(ctx context.Context, uid uint32) error {
	_, err := state.db.ExecContext(ctx, `
        INSERT INTO settings(key, value) VALUES ('administrative_owner_uid', ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return fmt.Errorf("persist administrative Owner UID: %w", err)
	}
	return nil
}

func (state *store) administrativeOwnerUID(ctx context.Context) (uint32, bool, error) {
	return administrativeOwnerUIDFromQuery(ctx, state.db)
}

func administrativeOwnerUIDFromQuery(ctx context.Context, queryer adminQueryer) (uint32, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'administrative_owner_uid'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read administrative Owner UID: %w", err)
	}
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, false, ErrAdminStateIncompatible
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false, ErrAdminStateIncompatible
		}
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || strconv.FormatUint(value, 10) != raw {
		return 0, false, ErrAdminStateIncompatible
	}
	return uint32(value), true, nil
}
