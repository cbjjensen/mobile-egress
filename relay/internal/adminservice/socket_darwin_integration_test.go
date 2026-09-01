//go:build darwin && cgo && macintegration

package adminservice

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	darwinSocketIntegrationBaseEnv = "MOBILE_EGRESS_MAC_INTEGRATION_ROOT"
	darwinSocketLockHelperEnv      = "MOBILE_EGRESS_SLICE4B_LOCK_HELPER"
	darwinSocketLockHelperRootEnv  = "MOBILE_EGRESS_SLICE4B_LOCK_ROOT"
	darwinSocketLockHelperGIDEnv   = "MOBILE_EGRESS_SLICE4B_LOCK_GID"
	darwinSocketUmaskHelperEnv     = "MOBILE_EGRESS_SLICE4B_UMASK_HELPER"
)

func TestDarwinRootAdminSocketPublishesExactShapeAndPersistentLock(t *testing.T) {
	requireDarwinRootStateTest(t)
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	root := newAuthorizedDarwinSocketTestRoot(t, true)
	adminGID := darwinIntegrationAdminGID(t)
	owner, err := openAdminSocket(context.Background(), darwinIntegrationSocketConfig(t, root, adminGID))
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root.spelled, "admin.sock")
	lockPath := filepath.Join(root.spelled, "admin.lock")
	assertDarwinIntegrationPathShape(t, socketPath, pathTypeSocket, 0, adminGID, 0o660)
	if err := newDarwinACLInspector().ValidatePath(context.Background(), socketPath, pathACLRejectExtended); err != nil {
		t.Fatalf("published socket ACL: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket remains after Close: %v", err)
	}
	assertDarwinIntegrationLockShape(t, lockPath)
	if err := newDarwinACLInspector().ValidatePath(context.Background(), lockPath, pathACLRejectExtended); err != nil {
		t.Fatalf("persistent lock ACL: %v", err)
	}
}

func TestDarwinAdminSocketLiveLockAndStaleRecovery(t *testing.T) {
	requireDarwinRootStateTest(t)
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	if os.Getenv(darwinSocketLockHelperEnv) == "1" {
		darwinSocketIntegrationLockHelper(t)
		return
	}
	root := newAuthorizedDarwinSocketTestRoot(t, true)
	adminGID := darwinIntegrationAdminGID(t)
	readyPath := filepath.Join(root.spelled, "helper.ready")
	command := exec.Command(os.Args[0], "-test.run=^TestDarwinAdminSocketLiveLockAndStaleRecovery$")
	command.Env = append(os.Environ(),
		darwinSocketLockHelperEnv+"=1",
		darwinSocketLockHelperRootEnv+"="+root.spelled,
		darwinSocketLockHelperGIDEnv+"="+strconv.FormatUint(uint64(adminGID), 10),
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	waitForDarwinIntegrationPath(t, readyPath, 10*time.Second)

	socketPath := filepath.Join(root.spelled, "admin.sock")
	before, err := darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}.Lstat(context.Background(), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := openAdminSocket(context.Background(), darwinIntegrationSocketConfig(t, root, adminGID)); err == nil || owner != nil {
		t.Fatalf("live lock admitted second owner: (%v, %v)", owner, err)
	}
	after, err := darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}.Lstat(context.Background(), socketPath)
	if err != nil || after != before {
		t.Fatalf("busy-lock attempt changed socket: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	successor, err := openAdminSocket(context.Background(), darwinIntegrationSocketConfig(t, root, adminGID))
	if err != nil {
		t.Fatalf("successor stale recovery: %v", err)
	}
	if err := successor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinAdminSocketRejectsAndPreservesUnsafePredecessors(t *testing.T) {
	requireDarwinRootStateTest(t)
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	adminGID := darwinIntegrationAdminGID(t)
	for _, shape := range []string{"symlink", "file", "directory", "fifo", "wrong-mode", "wrong-owner", "wrong-group", "hard-link", "extended-acl"} {
		t.Run(shape, func(t *testing.T) {
			root := newAuthorizedDarwinSocketTestRoot(t, true)
			socketPath := filepath.Join(root.spelled, "admin.sock")
			var linkedPath string
			switch shape {
			case "symlink":
				target := filepath.Join(root.spelled, "target")
				root.writeFile(t, target, []byte("fixture"), 0o600)
				root.symlink(t, target, socketPath)
			case "file":
				root.writeFile(t, socketPath, []byte("fixture"), 0o600)
			case "directory":
				root.mkdir(t, socketPath, 0o700)
			case "fifo":
				root.mkfifo(t, socketPath, 0o600)
			case "wrong-mode", "wrong-owner", "wrong-group", "hard-link", "extended-acl":
				root.leaveUnixSocket(t, socketPath)
				if shape == "wrong-mode" {
					root.chmod(t, socketPath, 0o666)
				} else if shape == "wrong-owner" {
					root.chmod(t, socketPath, 0o700)
					root.lchown(t, socketPath, 501, 0)
				} else if shape == "wrong-group" {
					root.chmod(t, socketPath, 0o660)
					root.lchown(t, socketPath, 0, darwinIntegrationWrongGID(adminGID))
				} else if shape == "hard-link" {
					linkedPath = filepath.Join(root.spelled, "admin-linked.sock")
					root.link(t, socketPath, linkedPath)
				} else {
					root.chmod(t, socketPath, 0o700)
					addDarwinTestACL(t, root, socketPath, "user:nobody allow read")
				}
			}
			before, err := os.Lstat(socketPath)
			if err != nil {
				t.Fatal(err)
			}
			if owner, err := openAdminSocket(context.Background(), darwinIntegrationSocketConfig(t, root, adminGID)); err == nil || owner != nil {
				t.Fatalf("unsafe %s predecessor admitted: (%v, %v)", shape, owner, err)
			}
			after, err := os.Lstat(socketPath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unsafe %s predecessor not preserved: %v", shape, err)
			}
			if linkedPath != "" {
				linked, err := os.Lstat(linkedPath)
				if err != nil || !os.SameFile(after, linked) {
					t.Fatalf("unsafe hard-link predecessor alias not preserved: %v", err)
				}
			}
		})
	}
}

func TestDarwinAdminLockCanonicalAliasUsesNoFollowOpenAndLogicalIdentityChecks(t *testing.T) {
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	root := newAuthorizedDarwinSocketTestRoot(t, false)
	canonicalParent := filepath.Join(root.spelled, "canonical-run")
	root.mkdir(t, canonicalParent, 0o700)
	lexicalParent := filepath.Join(root.spelled, "lexical-run")
	root.symlink(t, canonicalParent, lexicalParent)
	base := "canonical-alias.lock"
	logicalPath := filepath.Join(lexicalParent, base)
	canonicalPath := filepath.Join(canonicalParent, base)
	platform := darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}
	var logicalLock adminLock
	var logicalErr error
	root.runMutation(t, logicalPath, false, func(admittedPath string) error {
		logicalLock, logicalErr = platform.OpenLock(context.Background(), admittedPath, lockOpenCreateExclusive, 0o600)
		return nil
	})
	if !errors.Is(logicalErr, unix.ELOOP) || logicalLock != nil {
		t.Fatalf("logical alias OpenLock = (%v, %v), want ELOOP", logicalLock, logicalErr)
	}
	var lock adminLock
	var err error
	root.runMutation(t, canonicalPath, false, func(admittedPath string) error {
		lock, err = platform.OpenLock(context.Background(), admittedPath, lockOpenCreateExclusive, 0o600)
		return nil
	})
	if err != nil {
		t.Fatalf("canonical OpenLock: %v", err)
	}
	defer lock.Close()
	descriptor, err := lock.Fstat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	logical, err := platform.Lstat(context.Background(), logicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Identity() != logical.Identity() {
		t.Fatalf("canonical descriptor/logical path identity mismatch: descriptor=%+v logical=%+v", descriptor, logical)
	}
	if err := newDarwinACLInspector().ValidatePath(context.Background(), logicalPath, pathACLRejectExtended); err != nil {
		t.Fatalf("logical-path ACL verification: %v", err)
	}
}

func TestDarwinAdminSocketNativeUnlinkPreservesReplacement(t *testing.T) {
	requireDarwinRootStateTest(t)
	requireDarwinSocketIntegrationBase(t)
	if !enterDarwinSocketIntegrationUmask(t) {
		return
	}
	root := newAuthorizedDarwinSocketTestRoot(t, true)
	socketPath := filepath.Join(root.spelled, "admin.sock")
	root.leaveUnixSocket(t, socketPath)
	platform := darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}
	original, err := platform.Lstat(context.Background(), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(root.spelled, "replacement.sock")
	root.leaveUnixSocket(t, replacementPath)
	replacementCandidate, err := platform.Lstat(context.Background(), replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if replacementCandidate.Identity() == original.Identity() {
		t.Fatal("concurrent native replacement fixture did not have a distinct identity")
	}
	root.remove(t, socketPath)
	root.rename(t, replacementPath, socketPath)
	replacement, err := platform.Lstat(context.Background(), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Identity() != replacementCandidate.Identity() {
		t.Fatal("native replacement fixture changed identity during admitted rename")
	}
	replacementBefore, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	var unlinkErr error
	root.runMutation(t, socketPath, false, func(admittedPath string) error {
		unlinkErr = platform.Unlink(context.Background(), admittedPath, original.Identity())
		return nil
	})
	if !errors.Is(unlinkErr, errStatePathChanged) {
		t.Fatalf("identity-bound native unlink error = %v, want errStatePathChanged", unlinkErr)
	}
	replacementAfter, err := os.Lstat(socketPath)
	if err != nil || !os.SameFile(replacementBefore, replacementAfter) {
		t.Fatalf("native unlink removed/replaced the replacement: %v", err)
	}
}

func darwinSocketIntegrationLockHelper(t *testing.T) {
	rootPath := filepath.Clean(os.Getenv(darwinSocketLockHelperRootEnv))
	canonical, err := canonicalizeDarwinExistingTestDirectory(rootPath)
	if err != nil || darwinIntegrationPathRefused(rootPath, canonical) {
		t.Fatalf("unsafe helper root: %v", err)
	}
	info, err := os.Lstat(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	root := darwinTestRoot{spelled: filepath.ToSlash(rootPath), canonical: canonical, identity: info, acl: newDarwinACLInspector()}
	root.revalidate(t)
	rawGID := os.Getenv(darwinSocketLockHelperGIDEnv)
	adminGID, err := ParseCanonicalAdminGID(rawGID)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := openAdminSocket(context.Background(), darwinIntegrationSocketConfig(t, root, adminGID))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	root.writeFile(t, filepath.Join(root.spelled, "helper.ready"), []byte("ready"), 0o600)
	select {}
}

func newAuthorizedDarwinSocketTestRoot(t *testing.T, requireRoot bool) darwinTestRoot {
	t.Helper()
	base := filepath.Clean(os.Getenv(darwinSocketIntegrationBaseEnv))
	if base == "." || base == "" {
		t.Skip("set MOBILE_EGRESS_MAC_INTEGRATION_ROOT to a caller-provided safe temporary base")
	}
	canonicalBase, err := canonicalizeDarwinExistingTestDirectory(base)
	if err != nil || darwinIntegrationPathRefused(filepath.ToSlash(base), canonicalBase) {
		t.Fatalf("integration base refused: %v", err)
	}
	baseInfo, err := os.Lstat(base)
	if err != nil || !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("integration base must be an existing real directory: %v", err)
	}
	acl := newDarwinACLInspector()
	assertDarwinIntegrationACLStable(t, base, baseInfo, acl)
	if requireRoot {
		if os.Geteuid() != 0 {
			t.Skip("root socket integration requires explicit root authorization")
		}
		assertDarwinIntegrationRootDirectory(t, canonicalBase)
	}
	factory := darwinTestRootFactory{
		canonicalize: canonicalizeDarwinExistingTestDirectory,
		create: func() (string, error) {
			return os.MkdirTemp(base, "mobile-egress-slice4b-")
		},
	}
	root, err := factory.Create(filepath.ToSlash(base))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(root.spelled)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("created integration root is unsafe: %v", err)
	}
	root.identity = info
	root.acl = acl
	root.revalidate(t)
	if requireRoot {
		root.chmod(t, root.spelled, 0o700)
		assertDarwinIntegrationRootDirectory(t, root.spelled)
	}
	baseAfter, err := os.Lstat(base)
	if err != nil || !os.SameFile(baseInfo, baseAfter) {
		t.Fatalf("integration base changed during fixture creation: %v", err)
	}
	assertDarwinIntegrationACLStable(t, base, baseAfter, acl)
	t.Cleanup(func() {
		root.removeAll(t)
	})
	return root
}

func darwinIntegrationSocketConfig(t *testing.T, root darwinTestRoot, adminGID uint32) adminSocketConfig {
	t.Helper()
	root.revalidate(t)
	canonicalParent, err := canonicalizeDarwinExistingTestDirectory(root.spelled)
	if err != nil || darwinIntegrationPathRefused(root.spelled, canonicalParent) {
		t.Fatalf("integration socket parent refused: %v", err)
	}
	return adminSocketConfig{
		SocketPath:         filepath.ToSlash(filepath.Join(root.spelled, "admin.sock")),
		LockPath:           filepath.ToSlash(filepath.Join(root.spelled, "admin.lock")),
		LexicalParent:      filepath.ToSlash(root.spelled),
		CanonicalParent:    canonicalParent,
		CanonicalAncestors: darwinIntegrationAncestorChain(canonicalParent),
		AdminGID:           adminGID,
		Platform:           darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}},
		ACL:                newDarwinACLInspector(),
	}
}

func darwinIntegrationAncestorChain(parent string) []string {
	parent = pathpkg.Clean(filepath.ToSlash(parent))
	if parent == "/" {
		return []string{"/"}
	}
	parts := strings.Split(strings.TrimPrefix(parent, "/"), "/")
	chain := []string{"/"}
	current := ""
	for _, part := range parts {
		current += "/" + part
		chain = append(chain, current)
	}
	return chain
}

func darwinIntegrationAdminGID(t *testing.T) uint32 {
	t.Helper()
	group, err := user.LookupGroup("admin")
	if err != nil {
		t.Fatal(err)
	}
	gid, err := ParseCanonicalAdminGID(group.Gid)
	if err != nil {
		t.Fatal(err)
	}
	return gid
}

func darwinIntegrationWrongGID(adminGID uint32) int {
	if adminGID == 1<<32-1 {
		return int(adminGID - 1)
	}
	return int(adminGID + 1)
}

func assertDarwinIntegrationRootDirectory(t *testing.T, path string) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	metadata := pathMetadataFromDarwinStat(stat)
	if metadata.Type != pathTypeDirectory || metadata.UID != 0 || metadata.Permissions&0o022 != 0 {
		t.Fatalf("integration directory is not root-owned/nonwritable: %+v", metadata)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	assertDarwinIntegrationACLStable(t, path, info, newDarwinACLInspector())
}

func assertDarwinIntegrationACLStable(t *testing.T, path string, expected os.FileInfo, acl pathACLInspector) {
	t.Helper()
	before, err := os.Lstat(path)
	if err != nil || expected == nil || !os.SameFile(expected, before) {
		t.Fatalf("integration ACL target identity changed before validation: %v", err)
	}
	if err := acl.ValidatePath(context.Background(), path, pathACLRejectExtended); err != nil {
		t.Fatalf("integration ACL target is unsafe: %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || !os.SameFile(expected, after) {
		t.Fatalf("integration ACL target identity changed across validation: %v", err)
	}
}

func assertDarwinIntegrationPathShape(t *testing.T, path string, objectType pathObjectType, uid, gid uint32, mode uint16) {
	t.Helper()
	metadata, err := (darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}).Lstat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Type != objectType || metadata.UID != uid || metadata.GID != gid || metadata.Links != 1 || metadata.Permissions != mode {
		t.Fatalf("path shape = %+v, want type=%d uid=%d gid=%d links=1 mode=%#o", metadata, objectType, uid, gid, mode)
	}
}

func assertDarwinIntegrationLockShape(t *testing.T, path string) {
	t.Helper()
	metadata, err := (darwinAdminSocketPlatform{system: nativeDarwinAdminSocketSystem{}}).Lstat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Type != pathTypeRegular || metadata.UID != 0 || metadata.Links != 1 || metadata.Permissions != 0o600 {
		t.Fatalf("lock shape = %+v, want root-owned regular link-one mode 0600", metadata)
	}
}

func requireDarwinSocketIntegrationBase(t *testing.T) {
	t.Helper()
	base := filepath.Clean(os.Getenv(darwinSocketIntegrationBaseEnv))
	if base == "." || base == "" {
		t.Skip("set MOBILE_EGRESS_MAC_INTEGRATION_ROOT to a caller-provided safe temporary base")
	}
}

func enterDarwinSocketIntegrationUmask(t *testing.T) bool {
	t.Helper()
	helper := os.Getenv(darwinSocketUmaskHelperEnv)
	if helper == "1" {
		previous := unix.Umask(0o077)
		observed := unix.Umask(0o077)
		if observed != 0o077 {
			unix.Umask(previous)
			t.Fatalf("isolated socket integration umask = %#o, want 077", observed)
		}
		t.Cleanup(func() { unix.Umask(previous) })
		return true
	}
	if helper != "" {
		t.Fatalf("invalid %s value %q", darwinSocketUmaskHelperEnv, helper)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	command.Env = append(os.Environ(), darwinSocketUmaskHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("isolated umask helper timed out: %s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("isolated umask helper failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	return false
}

func waitForDarwinIntegrationPath(t *testing.T, path string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(fmt.Errorf("timed out waiting for %s", path))
}
