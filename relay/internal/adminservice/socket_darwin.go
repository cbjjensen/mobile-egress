//go:build darwin

package adminservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"

	"mobile-egress/internal/relayadmin"

	"golang.org/x/sys/unix"
)

const darwinAdminLockPath = "/var/run/com.cbjjensen.mobile-egress.relay.lock"

type darwinAdminSocketSystem interface {
	EvalSymlinks(string) (string, error)
	Lstat(string, *unix.Stat_t) error
	Open(string, int, uint32) (int, error)
	Fstat(int, *unix.Stat_t) error
	Flock(int, int) error
	Close(int) error
	ListenUnix(string, *net.UnixAddr) (*net.UnixListener, error)
	Fchownat(int, string, int, int, int) error
	Fchmodat(int, string, uint32, int) error
	Unlink(string) error
}

type nativeDarwinAdminSocketSystem struct{}

func (nativeDarwinAdminSocketSystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (nativeDarwinAdminSocketSystem) Lstat(path string, stat *unix.Stat_t) error {
	return unix.Lstat(path, stat)
}

func (nativeDarwinAdminSocketSystem) Open(path string, flags int, mode uint32) (int, error) {
	return unix.Open(path, flags, mode)
}

func (nativeDarwinAdminSocketSystem) Fstat(fd int, stat *unix.Stat_t) error {
	return unix.Fstat(fd, stat)
}

func (nativeDarwinAdminSocketSystem) Flock(fd, operation int) error {
	return unix.Flock(fd, operation)
}

func (nativeDarwinAdminSocketSystem) Close(fd int) error {
	return unix.Close(fd)
}

func (nativeDarwinAdminSocketSystem) ListenUnix(network string, address *net.UnixAddr) (*net.UnixListener, error) {
	return net.ListenUnix(network, address)
}

func (nativeDarwinAdminSocketSystem) Fchownat(dirfd int, path string, uid, gid, flags int) error {
	return unix.Fchownat(dirfd, path, uid, gid, flags)
}

func (nativeDarwinAdminSocketSystem) Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	return unix.Fchmodat(dirfd, path, mode, flags)
}

func (nativeDarwinAdminSocketSystem) Unlink(path string) error {
	return unix.Unlink(path)
}

type darwinAdminSocketPlatform struct {
	system darwinAdminSocketSystem
}

func OpenDarwinAdminSocket(ctx context.Context, adminGID uint32) (*AdminSocket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if adminGID == 0 {
		return nil, errAdminSocketUnsafe
	}
	platform := darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}
	acl := newPlatformACLInspector()
	if acl == nil {
		return nil, errStateACLUnavailable
	}
	return openAdminSocket(ctx, darwinAdminSocketConfig(adminGID, platform, acl))
}

func darwinAdminSocketConfig(adminGID uint32, platform adminSocketPlatform, acl pathACLInspector) adminSocketConfig {
	return adminSocketConfig{
		SocketPath:         relayadmin.DarwinAdminSocketPath,
		LockPath:           darwinAdminLockPath,
		LexicalParent:      "/var/run",
		CanonicalParent:    "/private/var/run",
		CanonicalAncestors: []string{"/", "/private", "/private/var", "/private/var/run"},
		AdminGID:           adminGID,
		Platform:           platform,
		ACL:                acl,
	}
}

func (platform darwinAdminSocketPlatform) nativeSystem() (darwinAdminSocketSystem, error) {
	if platform.system == nil {
		return nil, errAdminSocketUnsafe
	}
	return platform.system, nil
}

func (platform darwinAdminSocketPlatform) CanonicalParent(ctx context.Context, lexical string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return "", err
	}
	canonical, err := system.EvalSymlinks(lexical)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return canonical, nil
}

func (platform darwinAdminSocketPlatform) Lstat(ctx context.Context, path string) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return pathMetadata{}, err
	}
	var stat unix.Stat_t
	if err := system.Lstat(path, &stat); err != nil {
		return pathMetadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	return pathMetadataFromDarwinStat(stat), nil
}

func (platform darwinAdminSocketPlatform) OpenLock(ctx context.Context, path string, disposition lockOpenDisposition, mode uint16) (adminLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mode != 0o600 {
		return nil, errAdminSocketUnsafe
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return nil, err
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW_ANY
	openMode := uint32(0)
	switch disposition {
	case lockOpenCreateExclusive:
		flags |= unix.O_CREAT | unix.O_EXCL
		openMode = uint32(mode)
	case lockOpenExisting:
	default:
		return nil, errAdminSocketUnsafe
	}
	fd, err := system.Open(path, flags, openMode)
	if err != nil {
		return nil, err
	}
	if fd < 0 {
		return nil, errAdminSocketUnsafe
	}
	return &darwinAdminLock{fd: fd, system: system}, nil
}

func (platform darwinAdminSocketPlatform) ListenUnix(ctx context.Context, path string) (adminUnixListener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return nil, err
	}
	listener, err := system.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if listener == nil {
		return nil, errAdminSocketUnsafe
	}
	return listener, nil
}

func (platform darwinAdminSocketPlatform) ChownNoFollow(ctx context.Context, path string, expected pathIdentity, uid, gid uint32) error {
	if err := platform.requireSocketIdentity(ctx, path, expected); err != nil {
		return err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return err
	}
	if err := system.Fchownat(unix.AT_FDCWD, path, int(uid), int(gid), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	return platform.requireSocketIdentity(ctx, path, expected)
}

func (platform darwinAdminSocketPlatform) ChmodNoFollow(ctx context.Context, path string, expected pathIdentity, mode uint16) error {
	if err := platform.requireSocketIdentity(ctx, path, expected); err != nil {
		return err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return err
	}
	if err := system.Fchmodat(unix.AT_FDCWD, path, uint32(mode), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	return platform.requireSocketIdentity(ctx, path, expected)
}

func (platform darwinAdminSocketPlatform) Unlink(ctx context.Context, path string, expected pathIdentity) error {
	if err := platform.requireSocketIdentity(ctx, path, expected); err != nil {
		return err
	}
	system, err := platform.nativeSystem()
	if err != nil {
		return err
	}
	if err := system.Unlink(path); err != nil {
		return err
	}
	metadata, err := platform.Lstat(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify no-follow unlink: %w", err)
	}
	return fmt.Errorf("verify no-follow unlink: replacement at device %d inode %d: %w", metadata.Device, metadata.Inode, errStatePathChanged)
}

func (platform darwinAdminSocketPlatform) requireSocketIdentity(ctx context.Context, path string, expected pathIdentity) error {
	metadata, err := platform.Lstat(ctx, path)
	if err != nil {
		return err
	}
	if metadata.Type != pathTypeSocket || metadata.Identity() != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}

type darwinAdminLock struct {
	fd     int
	system darwinAdminSocketSystem
}

func (lock *darwinAdminLock) Fstat(ctx context.Context) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	if lock == nil || lock.system == nil || lock.fd < 0 {
		return pathMetadata{}, errAdminSocketUnsafe
	}
	var stat unix.Stat_t
	if err := lock.system.Fstat(lock.fd, &stat); err != nil {
		return pathMetadata{}, err
	}
	return pathMetadataFromDarwinStat(stat), ctx.Err()
}

func (lock *darwinAdminLock) TryExclusive() error {
	if lock == nil || lock.system == nil || lock.fd < 0 {
		return errAdminSocketUnsafe
	}
	return lock.system.Flock(lock.fd, unix.LOCK_EX|unix.LOCK_NB)
}

func (lock *darwinAdminLock) Unlock() error {
	if lock == nil || lock.system == nil || lock.fd < 0 {
		return errAdminSocketUnsafe
	}
	return lock.system.Flock(lock.fd, unix.LOCK_UN)
}

func (lock *darwinAdminLock) Close() error {
	if lock == nil || lock.system == nil || lock.fd < 0 {
		return errAdminSocketUnsafe
	}
	fd := lock.fd
	lock.fd = -1
	return lock.system.Close(fd)
}

var _ darwinAdminSocketSystem = nativeDarwinAdminSocketSystem{}
var _ adminSocketPlatform = darwinAdminSocketPlatform{}
var _ adminLock = (*darwinAdminLock)(nil)
