//go:build darwin

package adminservice

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"testing"

	"mobile-egress/internal/relayadmin"

	"golang.org/x/sys/unix"
)

func TestDarwinSocketMetadataConversionPreservesUnsignedDeviceAndShape(t *testing.T) {
	stat := unix.Stat_t{
		Dev:   -1,
		Ino:   91,
		Mode:  uint16(unix.S_IFSOCK) | 0o660,
		Nlink: 1,
		Uid:   0,
		Gid:   80,
	}
	metadata := pathMetadataFromDarwinStat(stat)
	if metadata.Device != uint64(4294967295) || metadata.Inode != 91 || metadata.RawType != uint16(unix.S_IFSOCK) ||
		metadata.Type != pathTypeSocket || metadata.UID != 0 || metadata.GID != 80 || metadata.Links != 1 || metadata.Permissions != 0o660 {
		t.Fatalf("socket metadata = %+v", metadata)
	}
}

func TestDarwinAdminLockUsesTwoPassNoFollowFlagsAndNonblockingFlock(t *testing.T) {
	system := &fakeDarwinAdminSocketSystem{openFD: 41}
	platform := darwinAdminSocketPlatform{system: system}

	created, err := platform.OpenLock(context.Background(), "/test/admin.lock", lockOpenCreateExclusive, 0o600)
	if err != nil {
		t.Fatalf("create OpenLock: %v", err)
	}
	existing, err := platform.OpenLock(context.Background(), "/test/admin.lock", lockOpenExisting, 0o600)
	if err != nil {
		t.Fatalf("existing OpenLock: %v", err)
	}
	wantCreate := unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NOFOLLOW_ANY
	wantExisting := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW_ANY
	if len(system.opens) != 2 || system.opens[0] != (darwinOpenCall{path: "/test/admin.lock", flags: wantCreate, mode: 0o600}) ||
		system.opens[1] != (darwinOpenCall{path: "/test/admin.lock", flags: wantExisting, mode: 0}) {
		t.Fatalf("open calls = %+v", system.opens)
	}
	if err := created.TryExclusive(); err != nil {
		t.Fatalf("TryExclusive: %v", err)
	}
	if err := created.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := created.Close(); err != nil {
		t.Fatalf("Close created: %v", err)
	}
	if err := existing.Close(); err != nil {
		t.Fatalf("Close existing: %v", err)
	}
	if len(system.flocks) != 2 || system.flocks[0] != unix.LOCK_EX|unix.LOCK_NB || system.flocks[1] != unix.LOCK_UN {
		t.Fatalf("flock operations = %v", system.flocks)
	}
}

func TestDarwinAdminLockNoFollowUsesDerivedCanonicalVarRunPath(t *testing.T) {
	system := &fakeDarwinAdminSocketSystem{
		openFD: 42,
		openErrors: map[string]error{
			"/var/run/com.cbjjensen.mobile-egress.relay.lock": unix.ELOOP,
		},
	}
	platform := darwinAdminSocketPlatform{system: system}
	config := darwinAdminSocketConfig(80, platform, unavailablePathACLInspector{})
	openPath, err := canonicalAdminLockOpenPath(config)
	if err != nil {
		t.Fatalf("canonicalAdminLockOpenPath: %v", err)
	}
	if openPath != "/private/var/run/com.cbjjensen.mobile-egress.relay.lock" {
		t.Fatalf("canonical lock open path = %q", openPath)
	}
	if lock, err := platform.OpenLock(context.Background(), config.LockPath, lockOpenCreateExclusive, 0o600); !errors.Is(err, unix.ELOOP) || lock != nil {
		t.Fatalf("logical no-follow lock open = (%v, %v), want ELOOP", lock, err)
	}
	lock, err := platform.OpenLock(context.Background(), openPath, lockOpenCreateExclusive, 0o600)
	if err != nil || lock == nil {
		t.Fatalf("canonical no-follow lock open = (%v, %v), want success", lock, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	existing, err := platform.OpenLock(context.Background(), openPath, lockOpenExisting, 0o600)
	if err != nil || existing == nil {
		t.Fatalf("canonical existing no-follow lock open = (%v, %v), want success", existing, err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}
	if len(system.opens) != 3 || system.opens[1].path != openPath || system.opens[2].path != openPath {
		t.Fatalf("canonical create/existing open calls = %+v", system.opens)
	}
}

func TestDarwinAdminSocketMutationsAreNoFollowAndIdentityBound(t *testing.T) {
	stat := unix.Stat_t{Dev: 4, Ino: 44, Mode: uint16(unix.S_IFSOCK) | 0o700, Nlink: 1, Uid: 0, Gid: 0}
	system := &fakeDarwinAdminSocketSystem{lstatResults: []darwinStatResult{{stat: stat}, {stat: stat}, {stat: stat}, {stat: stat}, {stat: stat}, {err: fs.ErrNotExist}}}
	platform := darwinAdminSocketPlatform{system: system}
	identity := pathIdentity{Device: 4, Inode: 44}
	if err := platform.ChownNoFollow(context.Background(), "/test/admin.sock", identity, 0, 80); err != nil {
		t.Fatalf("ChownNoFollow: %v", err)
	}
	if err := platform.ChmodNoFollow(context.Background(), "/test/admin.sock", identity, 0o660); err != nil {
		t.Fatalf("ChmodNoFollow: %v", err)
	}
	if err := platform.Unlink(context.Background(), "/test/admin.sock", identity); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if len(system.chowns) != 1 || system.chowns[0].flags != unix.AT_SYMLINK_NOFOLLOW || system.chowns[0].uid != 0 || system.chowns[0].gid != 80 {
		t.Fatalf("chown calls = %+v", system.chowns)
	}
	if len(system.chmods) != 1 || system.chmods[0].flags != unix.AT_SYMLINK_NOFOLLOW || system.chmods[0].mode != 0o660 {
		t.Fatalf("chmod calls = %+v", system.chmods)
	}
	if len(system.unlinks) != 1 || system.unlinks[0] != "/test/admin.sock" {
		t.Fatalf("unlink calls = %v", system.unlinks)
	}
}

func TestDarwinAdminSocketUnlinkPreservesReplacementBeforeNativeUnlink(t *testing.T) {
	replacement := unix.Stat_t{Dev: 4, Ino: 45, Mode: uint16(unix.S_IFSOCK) | 0o700, Nlink: 1, Uid: 0, Gid: 0}
	system := &fakeDarwinAdminSocketSystem{lstatResults: []darwinStatResult{{stat: replacement}}}
	platform := darwinAdminSocketPlatform{system: system}
	err := platform.Unlink(context.Background(), "/test/admin.sock", pathIdentity{Device: 4, Inode: 44})
	if !errors.Is(err, errStatePathChanged) {
		t.Fatalf("Unlink replacement error = %v, want errStatePathChanged", err)
	}
	if len(system.unlinks) != 0 {
		t.Fatalf("replacement reached native unlink: %v", system.unlinks)
	}
}

func TestDarwinAdminSocketConfigUsesFixedProductionPaths(t *testing.T) {
	platform := &fakeDarwinAdminSocketPlatform{}
	acl := unavailablePathACLInspector{}
	config := darwinAdminSocketConfig(80, platform, acl)
	if config.SocketPath != relayadmin.DarwinAdminSocketPath || config.LockPath != "/var/run/com.cbjjensen.mobile-egress.relay.lock" ||
		config.LexicalParent != "/var/run" || config.CanonicalParent != "/private/var/run" || config.AdminGID != 80 {
		t.Fatalf("Darwin config = %+v", config)
	}
	wantAncestors := []string{"/", "/private", "/private/var", "/private/var/run"}
	if len(config.CanonicalAncestors) != len(wantAncestors) {
		t.Fatalf("ancestors = %v", config.CanonicalAncestors)
	}
	for index := range wantAncestors {
		if config.CanonicalAncestors[index] != wantAncestors[index] {
			t.Fatalf("ancestors = %v, want %v", config.CanonicalAncestors, wantAncestors)
		}
	}
}

type darwinOpenCall struct {
	path  string
	flags int
	mode  uint32
}

type darwinStatResult struct {
	stat unix.Stat_t
	err  error
}

type darwinChownCall struct {
	dirfd int
	path  string
	uid   int
	gid   int
	flags int
}

type darwinChmodCall struct {
	dirfd int
	path  string
	mode  uint32
	flags int
}

type fakeDarwinAdminSocketSystem struct {
	openFD       int
	openErrors   map[string]error
	opens        []darwinOpenCall
	flocks       []int
	lstatResults []darwinStatResult
	lstatCalls   int
	chowns       []darwinChownCall
	chmods       []darwinChmodCall
	unlinks      []string
}

func (*fakeDarwinAdminSocketSystem) EvalSymlinks(path string) (string, error) { return path, nil }

func (system *fakeDarwinAdminSocketSystem) Lstat(_ string, target *unix.Stat_t) error {
	if system.lstatCalls >= len(system.lstatResults) {
		return errors.New("missing fake lstat result")
	}
	result := system.lstatResults[system.lstatCalls]
	system.lstatCalls++
	*target = result.stat
	return result.err
}

func (system *fakeDarwinAdminSocketSystem) Open(path string, flags int, mode uint32) (int, error) {
	system.opens = append(system.opens, darwinOpenCall{path: path, flags: flags, mode: mode})
	if err := system.openErrors[path]; err != nil {
		return -1, err
	}
	return system.openFD, nil
}

func (*fakeDarwinAdminSocketSystem) Fstat(int, *unix.Stat_t) error { return nil }

func (system *fakeDarwinAdminSocketSystem) Flock(_ int, operation int) error {
	system.flocks = append(system.flocks, operation)
	return nil
}

func (*fakeDarwinAdminSocketSystem) Close(int) error { return nil }

func (*fakeDarwinAdminSocketSystem) ListenUnix(string, *net.UnixAddr) (*net.UnixListener, error) {
	return nil, errors.New("unused")
}

func (system *fakeDarwinAdminSocketSystem) Fchownat(dirfd int, path string, uid, gid, flags int) error {
	system.chowns = append(system.chowns, darwinChownCall{dirfd: dirfd, path: path, uid: uid, gid: gid, flags: flags})
	return nil
}

func (system *fakeDarwinAdminSocketSystem) Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	system.chmods = append(system.chmods, darwinChmodCall{dirfd: dirfd, path: path, mode: mode, flags: flags})
	return nil
}

func (system *fakeDarwinAdminSocketSystem) Unlink(path string) error {
	system.unlinks = append(system.unlinks, path)
	return nil
}

type fakeDarwinAdminSocketPlatform struct{}

func (*fakeDarwinAdminSocketPlatform) CanonicalParent(context.Context, string) (string, error) {
	return "", errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) Lstat(context.Context, string) (pathMetadata, error) {
	return pathMetadata{}, errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) OpenLock(context.Context, string, lockOpenDisposition, uint16) (adminLock, error) {
	return nil, errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) ListenUnix(context.Context, string) (adminUnixListener, error) {
	return nil, errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) ChownNoFollow(context.Context, string, pathIdentity, uint32, uint32) error {
	return errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) ChmodNoFollow(context.Context, string, pathIdentity, uint16) error {
	return errors.New("unused")
}
func (*fakeDarwinAdminSocketPlatform) Unlink(context.Context, string, pathIdentity) error {
	return errors.New("unused")
}

var _ darwinAdminSocketSystem = (*fakeDarwinAdminSocketSystem)(nil)
var _ adminSocketPlatform = (*fakeDarwinAdminSocketPlatform)(nil)
