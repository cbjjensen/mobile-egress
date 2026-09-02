package adminservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path"
	"sync"
)

var (
	errAdminSocketUnsafe           = errors.New("relay admin socket path is unsafe")
	errAdminSocketCleanupUncertain = errors.New("relay admin socket cleanup is uncertain")
)

type lockOpenDisposition uint8

const (
	lockOpenCreateExclusive lockOpenDisposition = iota + 1
	lockOpenExisting
)

type adminLock interface {
	Fstat(context.Context) (pathMetadata, error)
	TryExclusive() error
	Unlock() error
	Close() error
}

type adminUnixListener interface {
	net.Listener
	SetUnlinkOnClose(bool)
}

type adminSocketPlatform interface {
	CanonicalParent(context.Context, string) (string, error)
	Lstat(context.Context, string) (pathMetadata, error)
	OpenLock(context.Context, string, lockOpenDisposition, uint16) (adminLock, error)
	ListenUnix(context.Context, string) (adminUnixListener, error)
	ChownNoFollow(context.Context, string, pathIdentity, uint32, uint32) error
	ChmodNoFollow(context.Context, string, pathIdentity, uint16) error
	Unlink(context.Context, string, pathIdentity) error
}

type adminSocketConfig struct {
	SocketPath         string
	LockPath           string
	LexicalParent      string
	CanonicalParent    string
	CanonicalAncestors []string
	AdminGID           uint32
	Platform           adminSocketPlatform
	ACL                pathACLInspector
}

type AdminSocket struct {
	lock           adminLock
	listener       adminUnixListener
	platform       adminSocketPlatform
	socketPath     string
	socketIdentity pathIdentity
	launchManaged  bool
	closeOnce      sync.Once
	closeErr       error
}

func (socket *AdminSocket) Listener() net.Listener {
	if socket == nil {
		return nil
	}
	return socket.listener
}

func (socket *AdminSocket) Close() error {
	if socket == nil {
		return nil
	}
	socket.closeOnce.Do(func() {
		var cleanupErr error
		if socket.listener != nil {
			if socket.launchManaged {
				cleanupErr = socket.listener.Close()
			} else {
				identity := socket.socketIdentity
				cleanupErr = cleanupBoundAdminSocket(context.Background(), socket.listener, socket.platform, socket.socketPath, &identity)
			}
		}
		var unlockErr error
		var descriptorErr error
		if socket.lock != nil {
			unlockErr = socket.lock.Unlock()
			descriptorErr = socket.lock.Close()
		}
		socket.closeErr = errors.Join(cleanupErr, unlockErr, descriptorErr)
	})
	return socket.closeErr
}

func openAdminSocket(ctx context.Context, config adminSocketConfig) (*AdminSocket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateAdminSocketConfig(config); err != nil {
		return nil, err
	}
	if err := validateAdminSocketParent(ctx, config); err != nil {
		return nil, err
	}
	lock, err := openVerifiedAdminLock(ctx, config)
	if err != nil {
		return nil, err
	}
	owner := &AdminSocket{lock: lock}
	if err := recoverAdminSocketPredecessor(ctx, config); err != nil {
		return nil, errors.Join(err, owner.Close())
	}
	listener, err := config.Platform.ListenUnix(ctx, config.SocketPath)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("bind relay admin socket: %w", err), owner.Close())
	}
	if listener == nil {
		return nil, errors.Join(errAdminSocketUnsafe, owner.Close())
	}
	listener.SetUnlinkOnClose(false)
	provisional, err := config.Platform.Lstat(ctx, config.SocketPath)
	if err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("inspect provisional relay admin socket: %w", err), owner, listener, config, nil)
	}
	identity := provisional.Identity()
	if !validProvisionalAdminSocket(provisional) {
		return nil, failAdminSocketPublication(errAdminSocketUnsafe, owner, listener, config, &identity)
	}
	if err := validateAdminPathACL(ctx, config, config.SocketPath, provisional, pathACLRejectExtended); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("validate provisional relay admin socket access: %w", err), owner, listener, config, &identity)
	}
	if err := config.Platform.ChownNoFollow(ctx, config.SocketPath, identity, 0, config.AdminGID); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("set relay admin socket ownership: %w", err), owner, listener, config, &identity)
	}
	chowned := provisional
	chowned.UID = 0
	chowned.GID = config.AdminGID
	if err := requireAdminSocketMetadata(ctx, config, chowned); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("verify relay admin socket ownership: %w", err), owner, listener, config, &identity)
	}
	if err := config.Platform.ChmodNoFollow(ctx, config.SocketPath, identity, 0o660); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("set relay admin socket permissions: %w", err), owner, listener, config, &identity)
	}
	final := chowned
	final.Permissions = 0o660
	if err := requireAdminSocketMetadata(ctx, config, final); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("verify published relay admin socket: %w", err), owner, listener, config, &identity)
	}
	if !validPublishedAdminSocket(final, config.AdminGID) {
		return nil, failAdminSocketPublication(errAdminSocketUnsafe, owner, listener, config, &identity)
	}
	if err := validateAdminPathACL(ctx, config, config.SocketPath, final, pathACLRejectExtended); err != nil {
		return nil, failAdminSocketPublication(fmt.Errorf("validate published relay admin socket access: %w", err), owner, listener, config, &identity)
	}
	owner.listener = listener
	owner.platform = config.Platform
	owner.socketPath = config.SocketPath
	owner.socketIdentity = identity
	return owner, nil
}

func validateAdminSocketConfig(config adminSocketConfig) error {
	if config.Platform == nil || config.ACL == nil || config.AdminGID == 0 ||
		config.SocketPath == "" || config.LockPath == "" || config.LexicalParent == "" || config.CanonicalParent == "" ||
		path.Clean(config.SocketPath) != config.SocketPath || path.Clean(config.LockPath) != config.LockPath ||
		path.Clean(config.LexicalParent) != config.LexicalParent || path.Clean(config.CanonicalParent) != config.CanonicalParent ||
		!path.IsAbs(config.SocketPath) || !path.IsAbs(config.LockPath) || !path.IsAbs(config.LexicalParent) || !path.IsAbs(config.CanonicalParent) ||
		path.Dir(config.SocketPath) != config.LexicalParent || path.Dir(config.LockPath) != config.LexicalParent ||
		len(config.CanonicalAncestors) == 0 {
		return errAdminSocketUnsafe
	}
	for index, ancestor := range config.CanonicalAncestors {
		if ancestor == "" || path.Clean(ancestor) != ancestor || !path.IsAbs(ancestor) {
			return errAdminSocketUnsafe
		}
		if index > 0 && path.Dir(ancestor) != config.CanonicalAncestors[index-1] {
			return errAdminSocketUnsafe
		}
	}
	if config.CanonicalAncestors[len(config.CanonicalAncestors)-1] != config.CanonicalParent {
		return errAdminSocketUnsafe
	}
	return nil
}

func validateAdminSocketParent(ctx context.Context, config adminSocketConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	canonical, err := config.Platform.CanonicalParent(ctx, config.LexicalParent)
	if err != nil {
		return fmt.Errorf("resolve relay admin socket parent: %w", err)
	}
	if canonical != config.CanonicalParent {
		return errAdminSocketUnsafe
	}
	policy := canonicalParentExact
	if config.LexicalParent == "/var/run" && config.CanonicalParent == "/private/var/run" {
		policy = canonicalParentDarwinVarRun
	}
	if err := validateCanonicalPrivilegedParent(config.LexicalParent, canonical, policy); err != nil {
		return errAdminSocketUnsafe
	}
	for _, ancestor := range config.CanonicalAncestors {
		metadata, err := config.Platform.Lstat(ctx, ancestor)
		if err != nil {
			return fmt.Errorf("inspect relay admin socket ancestor: %w", err)
		}
		if !safeAdminSocketAncestor(config, ancestor, metadata) {
			return errAdminSocketUnsafe
		}
		if err := validateAdminPathACL(ctx, config, ancestor, metadata, pathACLRejectNonRootMutation); err != nil {
			return fmt.Errorf("validate relay admin socket ancestor access: %w", err)
		}
	}
	return ctx.Err()
}

func safeAdminSocketAncestor(config adminSocketConfig, ancestor string, metadata pathMetadata) bool {
	if metadata.Type == pathTypeDirectory && metadata.UID == 0 && metadata.Permissions&0o022 == 0 {
		return true
	}
	// macOS owns its runtime directory as root:daemon with mode 0775. Its
	// daemon group is not an operator-writable group, so accept only that exact
	// platform-owned directory; all other ancestors remain non-writable.
	return config.LexicalParent == "/var/run" && config.CanonicalParent == "/private/var/run" &&
		ancestor == "/private/var/run" && metadata.Type == pathTypeDirectory &&
		metadata.UID == 0 && metadata.GID == 1 && metadata.Permissions == 0o775
}

func openVerifiedAdminLock(ctx context.Context, config adminSocketConfig) (adminLock, error) {
	openPath, err := canonicalAdminLockOpenPath(config)
	if err != nil {
		return nil, err
	}
	lock, err := config.Platform.OpenLock(ctx, openPath, lockOpenCreateExclusive, 0o600)
	if errors.Is(err, fs.ErrExist) {
		lock, err = config.Platform.OpenLock(ctx, openPath, lockOpenExisting, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("open persistent relay admin lock: %w", err)
	}
	if lock == nil {
		return nil, errAdminSocketUnsafe
	}
	if err := verifyAdminLock(ctx, config, lock); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if err := lock.TryExclusive(); err != nil {
		return nil, errors.Join(fmt.Errorf("acquire persistent relay admin lock: %w", err), lock.Close())
	}
	if err := verifyAdminLock(ctx, config, lock); err != nil {
		return nil, errors.Join(err, lock.Unlock(), lock.Close())
	}
	return lock, nil
}

func canonicalAdminLockOpenPath(config adminSocketConfig) (string, error) {
	if err := validateCanonicalPrivilegedParent(config.LexicalParent, config.CanonicalParent, canonicalParentExact); err != nil {
		if config.LexicalParent != "/var/run" || config.CanonicalParent != "/private/var/run" ||
			validateCanonicalPrivilegedParent(config.LexicalParent, config.CanonicalParent, canonicalParentDarwinVarRun) != nil {
			return "", errAdminSocketUnsafe
		}
	}
	base := path.Base(config.LockPath)
	openPath := path.Join(config.CanonicalParent, base)
	if base == "." || base == "/" || path.Dir(openPath) != config.CanonicalParent || path.Base(openPath) != base {
		return "", errAdminSocketUnsafe
	}
	return openPath, nil
}

func verifyAdminLock(ctx context.Context, config adminSocketConfig, lock adminLock) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	descriptor, err := lock.Fstat(ctx)
	if err != nil {
		return fmt.Errorf("inspect persistent relay admin lock descriptor: %w", err)
	}
	if !validAdminLockMetadata(descriptor) {
		return errAdminSocketUnsafe
	}
	if err := validateAdminPathACL(ctx, config, config.LockPath, descriptor, pathACLRejectExtended); err != nil {
		return fmt.Errorf("validate persistent relay admin lock: %w", err)
	}
	return nil
}

func validAdminLockMetadata(metadata pathMetadata) bool {
	return metadata.Type == pathTypeRegular && metadata.UID == 0 && metadata.Links == 1 && metadata.Permissions == 0o600
}

func recoverAdminSocketPredecessor(ctx context.Context, config adminSocketConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	first, err := config.Platform.Lstat(ctx, config.SocketPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect predecessor relay admin socket: %w", err)
	}
	if !validRecoverableAdminSocket(first, config.AdminGID) {
		return errAdminSocketUnsafe
	}
	if err := validateAdminPathACL(ctx, config, config.SocketPath, first, pathACLRejectExtended); err != nil {
		return fmt.Errorf("validate predecessor relay admin socket access: %w", err)
	}
	second, err := config.Platform.Lstat(ctx, config.SocketPath)
	if err != nil || second != first || !validRecoverableAdminSocket(second, config.AdminGID) {
		return errStatePathChanged
	}
	if err := validateAdminPathACL(ctx, config, config.SocketPath, second, pathACLRejectExtended); err != nil {
		return fmt.Errorf("revalidate predecessor relay admin socket access: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.Platform.Unlink(ctx, config.SocketPath, second.Identity()); err != nil {
		return fmt.Errorf("remove verified predecessor relay admin socket: %w", err)
	}
	return nil
}

func validRecoverableAdminSocket(metadata pathMetadata, adminGID uint32) bool {
	if metadata.Type != pathTypeSocket || metadata.UID != 0 || metadata.Links != 1 {
		return false
	}
	return metadata.Permissions == 0o700 || metadata.Permissions == 0o660 && metadata.GID == adminGID
}

func validProvisionalAdminSocket(metadata pathMetadata) bool {
	return metadata.Type == pathTypeSocket && metadata.UID == 0 && metadata.GID == 0 && metadata.Links == 1 && metadata.Permissions == 0o700
}

func validPublishedAdminSocket(metadata pathMetadata, adminGID uint32) bool {
	return metadata.Type == pathTypeSocket && metadata.UID == 0 && metadata.GID == adminGID &&
		metadata.Links == 1 && metadata.Permissions == 0o660
}

func requireAdminSocketMetadata(ctx context.Context, config adminSocketConfig, expected pathMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	metadata, err := config.Platform.Lstat(ctx, config.SocketPath)
	if err != nil {
		return err
	}
	if metadata != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}

func failAdminSocketPublication(
	cause error,
	owner *AdminSocket,
	listener adminUnixListener,
	config adminSocketConfig,
	identity *pathIdentity,
) error {
	cleanupErr := cleanupBoundAdminSocket(context.Background(), listener, config.Platform, config.SocketPath, identity)
	return errors.Join(cause, cleanupErr, owner.Close())
}

func cleanupBoundAdminSocket(
	ctx context.Context,
	listener adminUnixListener,
	platform adminSocketPlatform,
	socketPath string,
	identity *pathIdentity,
) error {
	if listener == nil || platform == nil || socketPath == "" {
		return errAdminSocketCleanupUncertain
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return errors.Join(errAdminSocketCleanupUncertain, fmt.Errorf("close relay admin socket listener: %w", err))
	}
	if identity == nil {
		return errAdminSocketCleanupUncertain
	}
	metadata, err := platform.Lstat(ctx, socketPath)
	if err != nil {
		return errors.Join(errAdminSocketCleanupUncertain, fmt.Errorf("inspect relay admin socket for cleanup: %w", err))
	}
	if metadata.Type != pathTypeSocket || metadata.Identity() != *identity {
		return errAdminSocketCleanupUncertain
	}
	if err := platform.Unlink(ctx, socketPath, *identity); err != nil {
		return errors.Join(errAdminSocketCleanupUncertain, fmt.Errorf("remove relay admin socket: %w", err))
	}
	metadata, err = platform.Lstat(ctx, socketPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Join(errAdminSocketCleanupUncertain, fmt.Errorf("verify relay admin socket cleanup: %w", err))
	}
	return errors.Join(errAdminSocketCleanupUncertain, fmt.Errorf("relay admin socket path remains at device %d inode %d", metadata.Device, metadata.Inode))
}

func validateAdminPathACL(ctx context.Context, config adminSocketConfig, target string, expected pathMetadata, policy pathACLPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := config.Platform.Lstat(ctx, target)
	if err != nil {
		return err
	}
	if before != expected {
		return errStatePathChanged
	}
	if err := config.ACL.ValidatePath(ctx, target, policy); err != nil {
		return err
	}
	after, err := config.Platform.Lstat(ctx, target)
	if err != nil || after != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}
