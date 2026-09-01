package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/pairing"
)

type adminEndpointPaths struct {
	certificateNew string
	keyNew         string
	certificateOld string
	keyOld         string
}

type adminEndpointFaultPoint string

const (
	adminEndpointBeforeNewCertificateWrite  adminEndpointFaultPoint = "before-new-certificate-write"
	adminEndpointAfterNewCertificateSync    adminEndpointFaultPoint = "after-new-certificate-sync"
	adminEndpointAfterNewKeySync            adminEndpointFaultPoint = "after-new-key-sync"
	adminEndpointAfterOldCertificateRename  adminEndpointFaultPoint = "after-old-certificate-rename"
	adminEndpointAfterOldKeyRename          adminEndpointFaultPoint = "after-old-key-rename"
	adminEndpointAfterNewCertificateRename  adminEndpointFaultPoint = "after-new-certificate-rename"
	adminEndpointAfterNewKeyRename          adminEndpointFaultPoint = "after-new-key-rename"
	adminEndpointBeforeCommit               adminEndpointFaultPoint = "before-commit"
	adminEndpointAfterCommit                adminEndpointFaultPoint = "after-commit"
	adminEndpointAfterOldKeyCleanup         adminEndpointFaultPoint = "after-old-key-cleanup"
	adminEndpointAfterOldCertificateCleanup adminEndpointFaultPoint = "after-old-certificate-cleanup"
)

type adminEndpointStage struct {
	stateDir          string
	requestID         string
	paths             adminEndpointPaths
	caCertificate     *x509.Certificate
	caCertificatePEM  []byte
	caPrivateKeyPEM   []byte
	oldHostname       string
	newHostname       string
	newURL            string
	serial            string
	oldCertificatePEM []byte
	oldPrivateKeyPEM  []byte
	newCertificatePEM []byte
	newPrivateKeyPEM  []byte
	fault             func(adminEndpointFaultPoint) error
}

func (state *AdminState) rotateEndpoint(
	ctx context.Context,
	transaction *adminMutationTransaction,
	options RotateEndpointOptions,
) (RotateEndpointResult, error) {
	origin, err := validateRelayOrigin(options.PublicURL, options.PublicName)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	if transaction.endpoint != nil {
		return RotateEndpointResult{}, relayadmin.ErrReplayState
	}
	evidence, err := scanAdminEndpointEvidence(state.stateDir)
	if err != nil || evidence.requestID != "" {
		state.markAdminDegraded()
		return RotateEndpointResult{}, ErrAdminStateIncompatible
	}
	var oldRelayURL string
	if err := transaction.tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'relay_url'`).Scan(&oldRelayURL); err != nil {
		return RotateEndpointResult{}, fmt.Errorf("read current relay URL: %w", err)
	}
	oldOrigin, err := pairing.RelayOrigin(oldRelayURL)
	if err != nil || oldRelayURL != oldOrigin.String() || oldOrigin.Hostname() == "" {
		return RotateEndpointResult{}, ErrAdminStateIncompatible
	}
	caCertificatePEM, err := readRegularAdminStateFile(filepath.Join(state.stateDir, caCertFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	caPrivateKeyPEM, err := readRegularAdminStateFile(filepath.Join(state.stateDir, caKeyFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	caCertificate, caPrivateKey, err := parseCertificateAuthorityState(caCertificatePEM, caPrivateKeyPEM)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	oldCertificatePEM, err := readRegularAdminStateFile(filepath.Join(state.stateDir, relayCertFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	oldPrivateKeyPEM, err := readRegularAdminStateFile(filepath.Join(state.stateDir, relayKeyFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	if _, err := validateRelayEndpointIdentity(oldCertificatePEM, oldPrivateKeyPEM, caCertificate, oldOrigin.Hostname(), ""); err != nil {
		return RotateEndpointResult{}, ErrAdminStateIncompatible
	}
	newCertificatePEM, newPrivateKeyPEM, serial, err := generateRelayCertificateState(options.PublicName, caCertificate, caPrivateKey, state.now())
	if err != nil {
		return RotateEndpointResult{}, err
	}
	if _, err := validateRelayEndpointIdentity(newCertificatePEM, newPrivateKeyPEM, caCertificate, options.PublicName, serial); err != nil {
		return RotateEndpointResult{}, err
	}
	stage := &adminEndpointStage{
		stateDir:          state.stateDir,
		requestID:         transaction.key.RequestID,
		paths:             adminEndpointArtifactPaths(state.stateDir, transaction.key.RequestID),
		caCertificate:     caCertificate,
		caCertificatePEM:  append([]byte(nil), caCertificatePEM...),
		caPrivateKeyPEM:   append([]byte(nil), caPrivateKeyPEM...),
		oldHostname:       oldOrigin.Hostname(),
		newHostname:       options.PublicName,
		newURL:            origin,
		serial:            serial,
		oldCertificatePEM: append([]byte(nil), oldCertificatePEM...),
		oldPrivateKeyPEM:  append([]byte(nil), oldPrivateKeyPEM...),
		newCertificatePEM: append([]byte(nil), newCertificatePEM...),
		newPrivateKeyPEM:  append([]byte(nil), newPrivateKeyPEM...),
		fault:             state.endpointFault,
	}
	transaction.endpoint = stage
	if err := stage.apply(ctx); err != nil {
		return RotateEndpointResult{}, err
	}
	if _, err := transaction.tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES ('relay_url', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, origin); err != nil {
		return RotateEndpointResult{}, fmt.Errorf("persist rotated endpoint URL: %w", err)
	}
	return RotateEndpointResult{PublicURL: origin, Serial: serial}, nil
}

func adminEndpointArtifactPaths(stateDir, requestID string) adminEndpointPaths {
	base := filepath.Join(stateDir, ".relay-rotate-"+requestID)
	return adminEndpointPaths{
		certificateNew: base + ".crt.new",
		keyNew:         base + ".key.new",
		certificateOld: base + ".crt.old",
		keyOld:         base + ".key.old",
	}
}

func (stage *adminEndpointStage) apply(ctx context.Context) error {
	if stage == nil || relayadmin.ValidateRequestID(stage.requestID) != nil ||
		stage.paths != adminEndpointArtifactPaths(stage.stateDir, stage.requestID) {
		return relayadmin.ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stage.fail(adminEndpointBeforeNewCertificateWrite); err != nil {
		return err
	}
	for _, path := range []string{stage.paths.certificateNew, stage.paths.keyNew, stage.paths.certificateOld, stage.paths.keyOld} {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return ErrAdminStateIncompatible
		}
	}
	if err := writeDurableFile(stage.paths.certificateNew, stage.newCertificatePEM, 0o644); err != nil {
		return fmt.Errorf("write staged relay certificate: %w", err)
	}
	if err := syncDirectory(stage.stateDir); err != nil {
		return fmt.Errorf("sync staged relay certificate: %w", err)
	}
	if err := stage.fail(adminEndpointAfterNewCertificateSync); err != nil {
		return err
	}
	if err := writeDurableFile(stage.paths.keyNew, stage.newPrivateKeyPEM, 0o600); err != nil {
		return fmt.Errorf("write staged relay private key: %w", err)
	}
	if err := syncDirectory(stage.stateDir); err != nil {
		return fmt.Errorf("sync staged relay private key: %w", err)
	}
	if err := stage.fail(adminEndpointAfterNewKeySync); err != nil {
		return err
	}
	certificatePEM, err := readRegularAdminStateFile(stage.paths.certificateNew)
	if err != nil {
		return err
	}
	privateKeyPEM, err := readRegularAdminStateFile(stage.paths.keyNew)
	if err != nil {
		return err
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, stage.caCertificate, stage.newHostname, stage.serial); err != nil {
		return err
	}
	if err := stage.validateAuthority(); err != nil {
		return err
	}
	if err := stage.validateOriginalPrimary(); err != nil {
		return err
	}
	for _, transition := range []struct {
		from, to string
		point    adminEndpointFaultPoint
	}{
		{filepath.Join(stage.stateDir, relayCertFilename), stage.paths.certificateOld, adminEndpointAfterOldCertificateRename},
		{filepath.Join(stage.stateDir, relayKeyFilename), stage.paths.keyOld, adminEndpointAfterOldKeyRename},
		{stage.paths.certificateNew, filepath.Join(stage.stateDir, relayCertFilename), adminEndpointAfterNewCertificateRename},
		{stage.paths.keyNew, filepath.Join(stage.stateDir, relayKeyFilename), adminEndpointAfterNewKeyRename},
	} {
		if err := os.Rename(transition.from, transition.to); err != nil {
			return fmt.Errorf("transition relay endpoint identity: %w", err)
		}
		if err := syncDirectory(stage.stateDir); err != nil {
			return fmt.Errorf("sync relay endpoint transition: %w", err)
		}
		if err := stage.fail(transition.point); err != nil {
			return err
		}
	}
	return nil
}

func (stage *adminEndpointStage) fail(point adminEndpointFaultPoint) error {
	if stage != nil && stage.fault != nil {
		return stage.fault(point)
	}
	return nil
}

func (stage *adminEndpointStage) rollback() error {
	if stage == nil {
		return nil
	}
	certificatePrimary := filepath.Join(stage.stateDir, relayCertFilename)
	keyPrimary := filepath.Join(stage.stateDir, relayKeyFilename)
	if err := validateKnownEndpointComponent(certificatePrimary, stage.oldCertificatePEM, stage.newCertificatePEM); err != nil {
		return err
	}
	if err := validateKnownEndpointComponent(keyPrimary, stage.oldPrivateKeyPEM, stage.newPrivateKeyPEM); err != nil {
		return err
	}
	if err := validateKnownEndpointComponent(stage.paths.certificateOld, stage.oldCertificatePEM); err != nil {
		return err
	}
	if err := validateKnownEndpointComponent(stage.paths.keyOld, stage.oldPrivateKeyPEM); err != nil {
		return err
	}
	if err := validateKnownEndpointComponent(stage.paths.certificateNew, stage.newCertificatePEM); err != nil {
		return err
	}
	if err := validateKnownEndpointComponent(stage.paths.keyNew, stage.newPrivateKeyPEM); err != nil {
		return err
	}
	if err := restoreAdminEndpointComponent(stage.stateDir, certificatePrimary, stage.paths.certificateOld, stage.oldCertificatePEM); err != nil {
		return err
	}
	if err := restoreAdminEndpointComponent(stage.stateDir, keyPrimary, stage.paths.keyOld, stage.oldPrivateKeyPEM); err != nil {
		return err
	}
	for _, artifact := range []struct {
		path     string
		expected []byte
	}{
		{stage.paths.keyNew, stage.newPrivateKeyPEM},
		{stage.paths.certificateNew, stage.newCertificatePEM},
	} {
		if err := removeAdminEndpointArtifactExact(stage.stateDir, artifact.path, artifact.expected); err != nil {
			return err
		}
	}
	certificatePEM, err := readRegularAdminStateFile(certificatePrimary)
	if err != nil || !bytes.Equal(certificatePEM, stage.oldCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, err := readRegularAdminStateFile(keyPrimary)
	if err != nil || !bytes.Equal(privateKeyPEM, stage.oldPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, stage.caCertificate, stage.oldHostname, ""); err != nil {
		return ErrAdminStateIncompatible
	}
	return stage.validateAuthority()
}

func (stage *adminEndpointStage) finalize() error {
	if stage == nil {
		return nil
	}
	if err := stage.validatePromoted(); err != nil {
		return err
	}
	for _, artifact := range []struct {
		path     string
		expected []byte
		point    adminEndpointFaultPoint
	}{
		{stage.paths.keyNew, stage.newPrivateKeyPEM, ""},
		{stage.paths.certificateNew, stage.newCertificatePEM, ""},
		{stage.paths.keyOld, stage.oldPrivateKeyPEM, adminEndpointAfterOldKeyCleanup},
		{stage.paths.certificateOld, stage.oldCertificatePEM, adminEndpointAfterOldCertificateCleanup},
	} {
		if err := removeAdminEndpointArtifactExact(stage.stateDir, artifact.path, artifact.expected); err != nil {
			return err
		}
		if artifact.point != "" {
			if err := stage.fail(artifact.point); err != nil {
				return err
			}
		}
	}
	return nil
}

func (stage *adminEndpointStage) validateOriginalPrimary() error {
	certificatePEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, relayCertFilename))
	if err != nil || !bytes.Equal(certificatePEM, stage.oldCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, relayKeyFilename))
	if err != nil || !bytes.Equal(privateKeyPEM, stage.oldPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, stage.caCertificate, stage.oldHostname, ""); err != nil {
		return ErrAdminStateIncompatible
	}
	return nil
}

func (stage *adminEndpointStage) validatePromoted() error {
	if err := stage.validateAuthority(); err != nil {
		return err
	}
	certificatePEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, relayCertFilename))
	if err != nil || !bytes.Equal(certificatePEM, stage.newCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, relayKeyFilename))
	if err != nil || !bytes.Equal(privateKeyPEM, stage.newPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	if _, err := validateRelayEndpointIdentity(certificatePEM, privateKeyPEM, stage.caCertificate, stage.newHostname, stage.serial); err != nil {
		return ErrAdminStateIncompatible
	}
	for _, artifact := range []struct {
		path     string
		expected []byte
	}{
		{stage.paths.certificateOld, stage.oldCertificatePEM},
		{stage.paths.keyOld, stage.oldPrivateKeyPEM},
		{stage.paths.certificateNew, stage.newCertificatePEM},
		{stage.paths.keyNew, stage.newPrivateKeyPEM},
	} {
		if err := validateKnownEndpointComponent(artifact.path, artifact.expected); err != nil {
			return err
		}
	}
	return nil
}

func (stage *adminEndpointStage) validateAuthority() error {
	if stage == nil {
		return relayadmin.ErrReplayState
	}
	certificatePEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, caCertFilename))
	if err != nil || !bytes.Equal(certificatePEM, stage.caCertificatePEM) {
		return ErrAdminStateIncompatible
	}
	privateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stage.stateDir, caKeyFilename))
	if err != nil || !bytes.Equal(privateKeyPEM, stage.caPrivateKeyPEM) {
		return ErrAdminStateIncompatible
	}
	certificate, _, err := parseCertificateAuthorityState(certificatePEM, privateKeyPEM)
	if err != nil || !bytes.Equal(certificate.Raw, stage.caCertificate.Raw) {
		return ErrAdminStateIncompatible
	}
	return nil
}

func restoreAdminEndpointComponent(stateDir, primary, backup string, expected []byte) error {
	backupContents, backupPresent, err := readOptionalRegularAdminStateFile(backup)
	if err != nil {
		return err
	}
	primaryContents, primaryPresent, err := readOptionalRegularAdminStateFile(primary)
	if err != nil {
		return err
	}
	if backupPresent {
		if !bytes.Equal(backupContents, expected) {
			return ErrAdminStateIncompatible
		}
		if primaryPresent {
			if err := os.Remove(primary); err != nil {
				return err
			}
			if err := syncDirectory(stateDir); err != nil {
				return err
			}
		}
		if err := os.Rename(backup, primary); err != nil {
			return err
		}
		return syncDirectory(stateDir)
	}
	if !primaryPresent || !bytes.Equal(primaryContents, expected) {
		return ErrAdminStateIncompatible
	}
	return nil
}

func validateKnownEndpointComponent(path string, expected ...[]byte) error {
	contents, present, err := readOptionalRegularAdminStateFile(path)
	if err != nil || !present {
		return err
	}
	for _, candidate := range expected {
		if bytes.Equal(contents, candidate) {
			return nil
		}
	}
	return ErrAdminStateIncompatible
}

func readOptionalRegularAdminStateFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !adminFileHasSingleLink(info) {
		return nil, false, ErrAdminStateIncompatible
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return contents, true, nil
}

func readRegularAdminStateFile(path string) ([]byte, error) {
	contents, present, err := readOptionalRegularAdminStateFile(path)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, ErrAdminStateIncompatible
	}
	return contents, nil
}

func validateRelayEndpointIdentity(
	certificatePEM, privateKeyPEM []byte,
	caCertificate *x509.Certificate,
	hostname, expectedSerial string,
) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil || len(pair.Certificate) != 1 {
		return nil, ErrAdminStateIncompatible
	}
	return validateRelayEndpointCertificate(certificatePEM, caCertificate, hostname, expectedSerial)
}
