package adminservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"mobile-egress/internal/relayadmin"
)

var (
	errStatePathUnsafe  = errors.New("relay state path is unsafe")
	errStatePathChanged = errors.New("relay state path changed during verification")
)

type pathObjectType uint8

const (
	pathTypeUnknown pathObjectType = iota
	pathTypeDirectory
	pathTypeRegular
	pathTypeSymlink
	pathTypeOther
)

type pathIdentity struct {
	Device uint64
	Inode  uint64
}

type pathMetadata struct {
	Device      uint64
	Inode       uint64
	UID         uint32
	GID         uint32
	Links       uint16
	RawType     uint16
	Type        pathObjectType
	Permissions uint16
}

func (metadata pathMetadata) Identity() pathIdentity {
	return pathIdentity{Device: metadata.Device, Inode: metadata.Inode}
}

type pathACLPolicy uint8

const (
	pathACLRejectExtended pathACLPolicy = iota + 1
	pathACLRejectNonRootMutation
)

type pathACLInspector interface {
	Validate(context.Context, openedPath, pathACLPolicy) error
}

type stateFilesystem interface {
	Lstat(context.Context, string) (pathMetadata, error)
	Open(context.Context, string, pathMetadata) (openedPath, error)
	Mkdir(context.Context, string, uint16) error
}

type openedPath interface {
	Path() string
	Metadata(context.Context) (pathMetadata, error)
	ReadDir(context.Context) ([]string, error)
	Chmod(context.Context, pathMetadata, uint16, func(context.Context) error) error
	Close() error
}

type statePathGuardConfig struct {
	ProductDir       string
	StateDir         string
	TrustedAncestors []string
	Filesystem       stateFilesystem
	ACL              pathACLInspector
}

type statePathGuard struct {
	productDir       string
	stateDir         string
	trustedAncestors []string
	filesystem       stateFilesystem
	acl              pathACLInspector
}

type nativeStatePathGuardConfig struct {
	ProductDir       string
	StateDir         string
	TrustedAncestors []string
}

func newNativeStatePathGuard(config nativeStatePathGuardConfig) (*statePathGuard, error) {
	filesystem, err := newPlatformStateFilesystem()
	if err != nil {
		return nil, err
	}
	acl := newPlatformACLInspector()
	if acl == nil {
		return nil, errStateACLUnavailable
	}
	return newStatePathGuard(statePathGuardConfig{
		ProductDir:       config.ProductDir,
		StateDir:         config.StateDir,
		TrustedAncestors: config.TrustedAncestors,
		Filesystem:       filesystem,
		ACL:              acl,
	})
}

func newStatePathGuard(config statePathGuardConfig) (*statePathGuard, error) {
	productDir := filepath.Clean(strings.TrimSpace(config.ProductDir))
	stateDir := filepath.Clean(strings.TrimSpace(config.StateDir))
	if config.Filesystem == nil || config.ACL == nil || config.ProductDir == "" || config.StateDir == "" ||
		!filepath.IsAbs(productDir) || !filepath.IsAbs(stateDir) ||
		filepath.Base(stateDir) != "Relay" || filepath.Dir(stateDir) != productDir ||
		len(config.TrustedAncestors) == 0 {
		return nil, errors.New("invalid relay state path guard configuration")
	}
	ancestors := make([]string, len(config.TrustedAncestors))
	for index, ancestor := range config.TrustedAncestors {
		clean := filepath.Clean(strings.TrimSpace(ancestor))
		if ancestor == "" || !filepath.IsAbs(clean) || clean == productDir || clean == stateDir {
			return nil, errors.New("invalid relay state trusted ancestor configuration")
		}
		if index > 0 && filepath.Dir(clean) != ancestors[index-1] {
			return nil, errors.New("relay state trusted ancestors are not an exact chain")
		}
		ancestors[index] = clean
	}
	if filepath.Dir(productDir) != ancestors[len(ancestors)-1] {
		return nil, errors.New("relay state product is outside trusted ancestors")
	}
	return &statePathGuard{
		productDir:       productDir,
		stateDir:         stateDir,
		trustedAncestors: ancestors,
		filesystem:       config.Filesystem,
		acl:              config.ACL,
	}, nil
}

func (guard *statePathGuard) Validate(ctx context.Context) error {
	if guard == nil {
		return errStatePathUnsafe
	}
	if err := guard.validateAncestors(ctx); err != nil {
		return err
	}
	product, err := guard.validateDirectory(ctx, guard.productDir, true)
	if err != nil {
		return err
	}
	entries, err := guard.readDirectory(ctx, guard.productDir, product)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) != 1 {
		return errStatePathUnsafe
	}
	name := entries[0]
	switch {
	case name == "Relay":
		state, err := guard.validateDirectory(ctx, guard.stateDir, true)
		if err != nil {
			return err
		}
		return guard.validateStateDirectory(ctx, guard.stateDir, state, true)
	case validSetupStageName(name):
		stageDir := filepath.Join(guard.productDir, name)
		stage, err := guard.validateDirectory(ctx, stageDir, true)
		if err != nil {
			return err
		}
		return guard.validateStateDirectory(ctx, stageDir, stage, false)
	default:
		return errStatePathUnsafe
	}
}

func (guard *statePathGuard) Prepare(ctx context.Context) error {
	if guard == nil {
		return errStatePathUnsafe
	}
	if err := guard.validateAncestors(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	metadata, err := guard.filesystem.Lstat(ctx, guard.productDir)
	switch {
	case err == nil:
		if metadata.Type != pathTypeDirectory {
			return errStatePathUnsafe
		}
		return guard.Validate(ctx)
	case !statePathMissing(err):
		return fmt.Errorf("inspect relay state product for preparation: %w", err)
	}
	if err := guard.filesystem.Mkdir(ctx, guard.productDir, 0o700); err != nil {
		return fmt.Errorf("create relay state product: %w", err)
	}
	product, err := guard.validateDirectory(ctx, guard.productDir, true)
	if err != nil {
		return err
	}
	entries, err := guard.readDirectory(ctx, guard.productDir, product)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errStatePathUnsafe
	}
	return nil
}

type stateRepairTarget struct {
	path       string
	metadata   pathMetadata
	targetMode uint16
	directory  bool
}

func (guard *statePathGuard) Repair(ctx context.Context) error {
	if guard == nil {
		return errStatePathUnsafe
	}
	targets, err := guard.repairTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.metadata.Permissions == target.targetMode {
			continue
		}
		if err := guard.applyRepairTarget(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func (guard *statePathGuard) repairTargets(ctx context.Context) ([]stateRepairTarget, error) {
	if err := guard.validateAncestors(ctx); err != nil {
		return nil, err
	}
	product, err := guard.inspectRepairDirectory(ctx, guard.productDir)
	if err != nil {
		return nil, err
	}
	targets := []stateRepairTarget{{path: guard.productDir, metadata: product, targetMode: 0o700, directory: true}}
	entries, err := guard.readDirectory(ctx, guard.productDir, product)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return targets, nil
	}
	if len(entries) != 1 {
		return nil, errStatePathUnsafe
	}
	name := entries[0]
	directory := ""
	allowArtifacts := false
	switch {
	case name == "Relay":
		directory = guard.stateDir
		allowArtifacts = true
	case validSetupStageName(name):
		directory = filepath.Join(guard.productDir, name)
	default:
		return nil, errStatePathUnsafe
	}
	metadata, err := guard.inspectRepairDirectory(ctx, directory)
	if err != nil {
		return nil, err
	}
	targets = append(targets, stateRepairTarget{path: directory, metadata: metadata, targetMode: 0o700, directory: true})
	fileTargets, err := guard.inspectRepairFiles(ctx, directory, metadata, allowArtifacts)
	if err != nil {
		return nil, err
	}
	targets = append(targets, fileTargets...)
	return targets, nil
}

func (guard *statePathGuard) inspectRepairDirectory(ctx context.Context, path string) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	metadata, err := guard.filesystem.Lstat(ctx, path)
	if err != nil {
		return pathMetadata{}, fmt.Errorf("inspect repairable relay state directory: %w", err)
	}
	if !safeRepairDirectory(metadata) {
		return pathMetadata{}, errStatePathUnsafe
	}
	if err := guard.validateOpenedPath(ctx, path, metadata, pathACLRejectExtended); err != nil {
		return pathMetadata{}, fmt.Errorf("validate repairable relay state directory access: %w", err)
	}
	return metadata, nil
}

func (guard *statePathGuard) inspectRepairFiles(ctx context.Context, directory string, directoryMetadata pathMetadata, allowArtifacts bool) ([]stateRepairTarget, error) {
	entries, err := guard.readDirectory(ctx, directory, directoryMetadata)
	if err != nil {
		return nil, err
	}
	var artifactRequestID string
	identities := make(map[pathIdentity]struct{}, len(entries))
	targets := make([]stateRepairTarget, 0, len(entries))
	for _, name := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, known := knownStateFile(name)
		if !known {
			if !allowArtifacts {
				return nil, errStatePathUnsafe
			}
			requestID, ok := rotationArtifactRequestID(name)
			if !ok || artifactRequestID != "" && artifactRequestID != requestID {
				return nil, errStatePathUnsafe
			}
			artifactRequestID = requestID
		}
		path := filepath.Join(directory, name)
		metadata, err := guard.inspectRepairFile(ctx, path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := identities[metadata.Identity()]; duplicate {
			return nil, errStatePathUnsafe
		}
		identities[metadata.Identity()] = struct{}{}
		targets = append(targets, stateRepairTarget{path: path, metadata: metadata, targetMode: 0o600})
	}
	return targets, nil
}

func (guard *statePathGuard) inspectRepairFile(ctx context.Context, path string) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	metadata, err := guard.filesystem.Lstat(ctx, path)
	if err != nil {
		return pathMetadata{}, fmt.Errorf("inspect repairable relay state file: %w", err)
	}
	if !safeRepairFile(metadata) {
		return pathMetadata{}, errStatePathUnsafe
	}
	if err := guard.validateOpenedPath(ctx, path, metadata, pathACLRejectExtended); err != nil {
		return pathMetadata{}, fmt.Errorf("validate repairable relay state file access: %w", err)
	}
	return metadata, nil
}

func (guard *statePathGuard) applyRepairTarget(ctx context.Context, target stateRepairTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := guard.filesystem.Lstat(ctx, target.path)
	if err != nil {
		return fmt.Errorf("reinspect relay state repair target: %w", err)
	}
	if before != target.metadata || target.directory && !safeRepairDirectory(before) ||
		!target.directory && !safeRepairFile(before) {
		return errStatePathChanged
	}
	opened, err := guard.openVerifiedPath(ctx, target.path, target.metadata, pathACLRejectExtended)
	if err != nil {
		return fmt.Errorf("open relay state repair target: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = opened.Close()
		}
	}()
	validateACL := func(validationContext context.Context) error {
		return guard.acl.Validate(validationContext, opened, pathACLRejectExtended)
	}
	if err := opened.Chmod(ctx, target.metadata, target.targetMode, validateACL); err != nil {
		return fmt.Errorf("tighten relay state permissions: %w", err)
	}
	wantAfter := target.metadata
	wantAfter.Permissions = target.targetMode
	after, err := guard.filesystem.Lstat(ctx, target.path)
	if err != nil {
		return fmt.Errorf("verify tightened relay state permissions: %w", err)
	}
	if after != wantAfter ||
		target.directory && !safeRepairDirectory(after) || !target.directory && !safeRepairFile(after) {
		return errStatePathChanged
	}
	openedAfter, err := opened.Metadata(ctx)
	if err != nil || openedAfter != wantAfter {
		return errStatePathChanged
	}
	if err := guard.acl.Validate(ctx, opened, pathACLRejectExtended); err != nil {
		return fmt.Errorf("verify tightened relay state access: %w", err)
	}
	if err := opened.Close(); err != nil {
		return fmt.Errorf("close tightened relay state target: %w", err)
	}
	closed = true
	return nil
}

func safeRepairDirectory(metadata pathMetadata) bool {
	return metadata.Type == pathTypeDirectory && metadata.UID == 0 && metadata.Permissions&0o7022 == 0
}

func safeRepairFile(metadata pathMetadata) bool {
	return metadata.Type == pathTypeRegular && metadata.UID == 0 && metadata.Links == 1 && metadata.Permissions&0o7133 == 0
}

func (guard *statePathGuard) validateAncestors(ctx context.Context) error {
	for _, path := range guard.trustedAncestors {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := guard.filesystem.Lstat(ctx, path)
		if err != nil {
			return fmt.Errorf("inspect relay state ancestor: %w", err)
		}
		if metadata.Type != pathTypeDirectory || metadata.UID != 0 || metadata.Permissions&0o022 != 0 {
			return errStatePathUnsafe
		}
		if err := guard.validateOpenedPath(ctx, path, metadata, pathACLRejectNonRootMutation); err != nil {
			return fmt.Errorf("validate relay state ancestor access: %w", err)
		}
	}
	return ctx.Err()
}

func (guard *statePathGuard) validateDirectory(ctx context.Context, path string, exactMode bool) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	metadata, err := guard.filesystem.Lstat(ctx, path)
	if err != nil {
		return pathMetadata{}, fmt.Errorf("inspect relay state directory: %w", err)
	}
	if metadata.Type != pathTypeDirectory || metadata.UID != 0 || exactMode && metadata.Permissions != 0o700 {
		return pathMetadata{}, errStatePathUnsafe
	}
	if err := guard.validateOpenedPath(ctx, path, metadata, pathACLRejectExtended); err != nil {
		return pathMetadata{}, fmt.Errorf("validate relay state directory access: %w", err)
	}
	return metadata, nil
}

func (guard *statePathGuard) validateOpenedPath(ctx context.Context, path string, expected pathMetadata, policy pathACLPolicy) error {
	opened, err := guard.openVerifiedPath(ctx, path, expected, policy)
	if err != nil {
		return err
	}
	if err := opened.Close(); err != nil {
		return fmt.Errorf("close verified relay state path: %w", err)
	}
	return nil
}

func (guard *statePathGuard) openVerifiedPath(
	ctx context.Context,
	path string,
	expected pathMetadata,
	policy pathACLPolicy,
) (openedPath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opened, err := guard.filesystem.Open(ctx, path, expected)
	if err != nil {
		return nil, fmt.Errorf("open relay state path without following: %w", err)
	}
	if opened == nil {
		return nil, errStatePathUnsafe
	}
	if err := guard.verifyOpenedPath(ctx, opened, path, expected, policy); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return opened, nil
}

func (guard *statePathGuard) verifyOpenedPath(
	ctx context.Context,
	opened openedPath,
	path string,
	expected pathMetadata,
	policy pathACLPolicy,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opened == nil || opened.Path() != path {
		return errStatePathUnsafe
	}
	metadata, err := opened.Metadata(ctx)
	if err != nil {
		return fmt.Errorf("inspect opened relay state path: %w", err)
	}
	if metadata != expected {
		return errStatePathChanged
	}
	if err := guard.acl.Validate(ctx, opened, policy); err != nil {
		return fmt.Errorf("validate opened relay state path access: %w", err)
	}
	metadata, err = opened.Metadata(ctx)
	if err != nil || metadata != expected {
		return errStatePathChanged
	}
	pathMetadata, err := guard.filesystem.Lstat(ctx, path)
	if err != nil || pathMetadata != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}

func (guard *statePathGuard) readDirectory(ctx context.Context, path string, expected pathMetadata) (entries []string, returnErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opened, err := guard.openVerifiedPath(ctx, path, expected, pathACLRejectExtended)
	if err != nil {
		return nil, fmt.Errorf("open relay state directory for enumeration: %w", err)
	}
	defer func() {
		if err := opened.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close enumerated relay state directory: %w", err))
		}
	}()
	entries, err = opened.ReadDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("read relay state directory descriptor: %w", err)
	}
	if err := guard.verifyOpenedPath(ctx, opened, path, expected, pathACLRejectExtended); err != nil {
		return nil, fmt.Errorf("verify enumerated relay state directory: %w", err)
	}
	entries = append([]string(nil), entries...)
	sort.Strings(entries)
	for index, name := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
			index > 0 && entries[index-1] == name {
			return nil, errStatePathUnsafe
		}
	}
	return entries, nil
}

func (guard *statePathGuard) validateStateDirectory(ctx context.Context, directory string, directoryMetadata pathMetadata, allowArtifacts bool) error {
	entries, err := guard.readDirectory(ctx, directory, directoryMetadata)
	if err != nil {
		return err
	}
	var artifactRequestID string
	identities := make(map[pathIdentity]struct{}, len(entries))
	for _, name := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		certificate, known := knownStateFile(name)
		if !known {
			if !allowArtifacts {
				return errStatePathUnsafe
			}
			requestID, ok := rotationArtifactRequestID(name)
			if !ok || artifactRequestID != "" && artifactRequestID != requestID {
				return errStatePathUnsafe
			}
			artifactRequestID = requestID
			certificate = false
		}
		path := filepath.Join(directory, name)
		metadata, err := guard.validateStateFile(ctx, path, certificate && allowArtifacts)
		if err != nil {
			return err
		}
		if _, duplicate := identities[metadata.Identity()]; duplicate {
			return errStatePathUnsafe
		}
		identities[metadata.Identity()] = struct{}{}
	}
	return nil
}

func (guard *statePathGuard) validateStateFile(ctx context.Context, path string, certificate bool) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	metadata, err := guard.filesystem.Lstat(ctx, path)
	if err != nil {
		return pathMetadata{}, fmt.Errorf("inspect relay state file: %w", err)
	}
	validMode := metadata.Permissions == 0o600
	if certificate {
		validMode = metadata.Permissions == 0o600 || metadata.Permissions == 0o644
	}
	if metadata.Type != pathTypeRegular || metadata.UID != 0 || metadata.Links != 1 || !validMode {
		return pathMetadata{}, errStatePathUnsafe
	}
	if err := guard.validateOpenedPath(ctx, path, metadata, pathACLRejectExtended); err != nil {
		return pathMetadata{}, fmt.Errorf("validate relay state file access: %w", err)
	}
	return metadata, nil
}

func validSetupStageName(name string) bool {
	const prefix = ".relay-setup-"
	return strings.HasPrefix(name, prefix) && len(name) == len(prefix)+32 &&
		relayadmin.ValidateRequestID(name[len(prefix):]) == nil
}

func knownStateFile(name string) (certificate bool, known bool) {
	switch name {
	case "ca.crt", "relay.crt":
		return true, true
	case "ca.key", "relay.key", "state.db", "state.db-wal", "state.db-shm":
		return false, true
	default:
		return false, false
	}
}

func rotationArtifactRequestID(name string) (string, bool) {
	const prefix = ".relay-rotate-"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	for _, suffix := range []string{".crt.new", ".key.new", ".crt.old", ".key.old"} {
		if !strings.HasSuffix(name, suffix) || len(name) != len(prefix)+32+len(suffix) {
			continue
		}
		requestID := name[len(prefix) : len(name)-len(suffix)]
		return requestID, relayadmin.ValidateRequestID(requestID) == nil
	}
	return "", false
}

func statePathMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

type canonicalParentPolicy uint8

const (
	canonicalParentExact canonicalParentPolicy = iota + 1
	canonicalParentDarwinVarRun
)

func validateCanonicalPrivilegedParent(spelled, canonical string, policy canonicalParentPolicy) error {
	if spelled == "" || canonical == "" || pathpkg.Clean(spelled) != spelled || pathpkg.Clean(canonical) != canonical ||
		!pathpkg.IsAbs(spelled) || !pathpkg.IsAbs(canonical) {
		return errStatePathUnsafe
	}
	switch policy {
	case canonicalParentExact:
		if canonical == spelled {
			return nil
		}
	case canonicalParentDarwinVarRun:
		if spelled == "/var/run" && canonical == "/private/var/run" {
			return nil
		}
	}
	return errStatePathUnsafe
}

var errStateACLUnavailable = errors.New("native relay state ACL inspection is unavailable")

type unavailablePathACLInspector struct{}

func (unavailablePathACLInspector) Validate(context.Context, openedPath, pathACLPolicy) error {
	return errStateACLUnavailable
}
