//go:build darwin && cgo && macintegration

package adminservice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const darwinFIFOOpenHelperEnv = "MOBILE_EGRESS_DARWIN_FIFO_OPEN_HELPER"

func TestDarwinStateOpenRejectsFIFOReplacementWithoutBlocking(t *testing.T) {
	if os.Getenv(darwinFIFOOpenHelperEnv) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinStateOpenRejectsFIFOReplacementWithoutBlocking$")
		command.Env = append(os.Environ(), darwinFIFOOpenHelperEnv+"=1")
		output, err := command.CombinedOutput()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("FIFO replacement held descriptor acquisition past its operation bound: %s", strings.TrimSpace(string(output)))
		}
		if err != nil {
			t.Fatalf("FIFO replacement helper failed: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		return
	}

	testRoot := newAdmittedDarwinTestRoot(t)
	path := filepath.Join(testRoot.spelled, "replace-with-fifo")
	testRoot.writeFile(t, path, []byte("fixture"), 0o600)
	filesystem, err := newPlatformStateFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filesystem.Lstat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	testRoot.remove(t, path)
	testRoot.mkfifo(t, path, 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	opened, err := filesystem.Open(ctx, path, expected)
	if opened != nil {
		_ = opened.Close()
		t.Fatal("Open() returned a descriptor for a FIFO replacement")
	}
	if !errors.Is(err, errStatePathChanged) {
		t.Fatalf("Open() error = %v, want errStatePathChanged", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("FIFO rejection exceeded operation context: %v", err)
	}
}

func TestDarwinStateACLInspectorAcceptsTempObjectWithoutExtendedACL(t *testing.T) {
	t.Parallel()

	testRoot := newAdmittedDarwinTestRoot(t)
	path := filepath.Join(testRoot.spelled, "state-file")
	testRoot.writeFile(t, path, []byte("fixture"), 0o600)
	filesystem, err := newPlatformStateFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filesystem.Lstat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := filesystem.Open(context.Background(), path, expected)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	inspector := newDarwinACLInspector()
	for _, policy := range []pathACLPolicy{pathACLRejectExtended, pathACLRejectNonRootMutation} {
		if err := inspector.Validate(context.Background(), opened, policy); err != nil {
			t.Fatalf("Validate(no ACL, policy %d) error = %v", policy, err)
		}
	}
}

func TestDarwinACLInspectorAcceptsSystemRunDirectoryWithoutExtendedACL(t *testing.T) {
	inspector := newDarwinACLInspector()
	if err := inspector.ValidatePath(context.Background(), "/private/var/run", pathACLRejectNonRootMutation); err != nil {
		t.Fatalf("ValidatePath(/private/var/run without an extended ACL) error = %v", err)
	}
}

func TestDarwinRootStateGuardValidatesAndRepairsRootOwnedTempTree(t *testing.T) {
	requireDarwinRootStateTest(t)

	fixture := newDarwinRootStateFixture(t)
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	fixture.testRoot.chmod(t, fixture.product, 0o755)
	keyPath := filepath.Join(fixture.state, "ca.key")
	fixture.testRoot.chmod(t, keyPath, 0o640)
	if err := fixture.guard.Repair(context.Background()); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	for path, want := range map[string]os.FileMode{fixture.product: 0o700, keyPath: 0o600} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %#o, want %#o", path, got, want)
		}
	}
}

func TestDarwinRootStateGuardRejectsSymlinkAndHardLinkEvidence(t *testing.T) {
	requireDarwinRootStateTest(t)

	t.Run("symlink", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		path := filepath.Join(fixture.state, "state.db")
		fixture.testRoot.remove(t, path)
		fixture.testRoot.symlink(t, filepath.Join(fixture.state, "ca.key"), path)
		if err := fixture.guard.Validate(context.Background()); err == nil {
			t.Fatal("Validate() accepted a state-file symlink")
		}
	})

	t.Run("hard link", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		wal := filepath.Join(fixture.state, "state.db-wal")
		fixture.testRoot.link(t, filepath.Join(fixture.state, "state.db"), wal)
		if err := fixture.guard.Validate(context.Background()); err == nil {
			t.Fatal("Validate() accepted hard-linked state evidence")
		}
	})
}

func TestDarwinRootStateGuardAppliesDistinctAncestorAndStateACLPolicies(t *testing.T) {
	requireDarwinRootStateTest(t)

	t.Run("benign ancestor deny", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		addDarwinTestACL(t, fixture.testRoot, fixture.ancestors[1], "user:nobody deny write,delete_child,writesecurity,chown")
		if err := fixture.guard.Validate(context.Background()); err != nil {
			t.Fatalf("Validate() rejected benign ancestor deny ACL: %v", err)
		}
	})

	t.Run("benign ancestor read allow", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		addDarwinTestACL(t, fixture.testRoot, fixture.ancestors[1], "user:nobody allow read")
		if err := fixture.guard.Validate(context.Background()); err != nil {
			t.Fatalf("Validate() rejected non-mutating ancestor allow ACL: %v", err)
		}
	})

	t.Run("mutating ancestor allow", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		addDarwinTestACL(t, fixture.testRoot, fixture.ancestors[1], "user:nobody allow write,delete_child")
		if err := fixture.guard.Validate(context.Background()); err == nil {
			t.Fatal("Validate() accepted a non-root mutating ancestor allow ACL")
		}
	})

	t.Run("any product ACL", func(t *testing.T) {
		fixture := newDarwinRootStateFixture(t)
		addDarwinTestACL(t, fixture.testRoot, fixture.product, "user:nobody allow read")
		if err := fixture.guard.Validate(context.Background()); err == nil {
			t.Fatal("Validate() accepted a nontrivial product ACL")
		}
	})
}

type darwinRootStateFixture struct {
	testRoot  darwinTestRoot
	guard     *statePathGuard
	ancestors []string
	product   string
	state     string
}

func newDarwinRootStateFixture(t *testing.T) darwinRootStateFixture {
	t.Helper()

	testRoot := newAdmittedDarwinTestRoot(t)
	root := filepath.Join(testRoot.spelled, "guard-root")
	library := filepath.Join(root, "Library")
	support := filepath.Join(library, "Application Support")
	product := filepath.Join(support, "ZFNF Mobile Egress")
	state := filepath.Join(product, "Relay")
	for _, path := range []string{root, library, support} {
		testRoot.mkdir(t, path, 0o755)
	}
	for _, path := range []string{product, state} {
		testRoot.mkdir(t, path, 0o700)
	}
	for _, name := range testBaseStateNames[:5] {
		path := filepath.Join(state, name)
		testRoot.writeFile(t, path, []byte("fixture"), os.FileMode(safeStateFileMode(name)))
	}
	guard, err := newNativeStatePathGuard(nativeStatePathGuardConfig{
		ProductDir:       product,
		StateDir:         state,
		TrustedAncestors: []string{root, library, support},
	})
	if err != nil {
		t.Fatal(err)
	}
	return darwinRootStateFixture{
		testRoot: testRoot, guard: guard, ancestors: []string{root, library, support}, product: product, state: state,
	}
}

func requireDarwinRootStateTest(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned state integration requires explicit root authorization")
	}
}

func newAdmittedDarwinTestRoot(t *testing.T) darwinTestRoot {
	t.Helper()
	base, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	factory := darwinTestRootFactory{
		canonicalize: canonicalizeDarwinExistingTestDirectory,
		create: func() (string, error) {
			return t.TempDir(), nil
		},
	}
	root, err := factory.Create(filepath.ToSlash(base))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(root.spelled)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("created Darwin integration fixture root is not a real directory: %v", err)
	}
	root.identity = info
	root.acl = newDarwinACLInspector()
	root.revalidate(t)
	return root
}

func canonicalizeDarwinExistingTestDirectory(candidate string) (string, error) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("canonical Darwin integration path is not a real directory")
	}
	return filepath.ToSlash(filepath.Clean(canonical)), nil
}

func (root darwinTestRoot) revalidate(t *testing.T) {
	t.Helper()
	if root.identity == nil {
		t.Fatal("Darwin integration fixture root lacks admitted identity")
	}
	if root.acl == nil {
		t.Fatal("Darwin integration fixture root lacks an ACL inspector")
	}
	before, err := os.Lstat(root.spelled)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || !os.SameFile(root.identity, before) {
		t.Fatalf("Darwin integration fixture root identity changed: %v", err)
	}
	canonical, err := canonicalizeDarwinExistingTestDirectory(root.spelled)
	if err != nil || !strings.EqualFold(canonical, root.canonical) || darwinIntegrationPathRefused(root.spelled, canonical) {
		t.Fatalf("Darwin integration fixture root canonical path changed: %q (%v)", canonical, err)
	}
	if err := root.acl.ValidatePath(context.Background(), root.spelled, pathACLRejectExtended); err != nil {
		t.Fatalf("Darwin integration fixture root ACL is unsafe: %v", err)
	}
	after, err := os.Lstat(root.spelled)
	if err != nil || !after.IsDir() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || !os.SameFile(root.identity, after) {
		t.Fatalf("Darwin integration fixture root changed across ACL validation: %v", err)
	}
}

func (root darwinTestRoot) mutationCoordinates(t *testing.T, candidate string, followFinal bool) (string, string, string) {
	t.Helper()
	root.revalidate(t)
	abs, err := filepath.Abs(candidate)
	if err != nil {
		t.Fatal(err)
	}
	spelled := filepath.ToSlash(filepath.Clean(abs))
	canonical := root.canonical
	if !strings.EqualFold(spelled, root.spelled) {
		canonicalParent, err := canonicalizeDarwinExistingTestDirectory(filepath.Dir(abs))
		if err != nil {
			t.Fatalf("canonicalize Darwin integration mutation parent: %v", err)
		}
		canonical = filepath.ToSlash(filepath.Join(canonicalParent, filepath.Base(abs)))
	}
	if followFinal {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			t.Fatalf("resolve Darwin integration mutation target: %v", err)
		}
		canonical = filepath.ToSlash(filepath.Clean(resolved))
	}
	return abs, spelled, canonical
}

func (root darwinTestRoot) runMutation(t *testing.T, candidate string, followFinal bool, mutate func(string) error) {
	t.Helper()
	abs, spelled, canonical := root.mutationCoordinates(t, candidate, followFinal)
	err := runDarwinAdmittedMutation(root, spelled, canonical, func() error {
		root.revalidate(t)
		return mutate(abs)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (root darwinTestRoot) mkdir(t *testing.T, candidate string, mode os.FileMode) {
	t.Helper()
	root.runMutation(t, candidate, false, func(path string) error { return os.Mkdir(path, mode) })
}

func (root darwinTestRoot) writeFile(t *testing.T, candidate string, data []byte, mode os.FileMode) {
	t.Helper()
	root.runMutation(t, candidate, false, func(path string) error {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		return errors.Join(writeErr, file.Close())
	})
}

func (root darwinTestRoot) chmod(t *testing.T, candidate string, mode os.FileMode) {
	t.Helper()
	root.runMutation(t, candidate, true, func(path string) error { return os.Chmod(path, mode) })
}

func (root darwinTestRoot) lchown(t *testing.T, candidate string, uid, gid int) {
	t.Helper()
	root.runMutation(t, candidate, true, func(path string) error { return unix.Lchown(path, uid, gid) })
}

func (root darwinTestRoot) remove(t *testing.T, candidate string) {
	t.Helper()
	root.runMutation(t, candidate, false, os.Remove)
}

func (root darwinTestRoot) removeIfExists(t *testing.T, candidate string) {
	t.Helper()
	root.runMutation(t, candidate, false, func(path string) error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (root darwinTestRoot) removeAll(t *testing.T) {
	t.Helper()
	if !strings.EqualFold(filepath.ToSlash(filepath.Clean(root.spelled)), root.spelled) {
		t.Fatal("Darwin integration recursive cleanup requires the exact admitted root")
	}
	root.runMutation(t, root.spelled, true, os.RemoveAll)
}

func (root darwinTestRoot) symlink(t *testing.T, target, link string) {
	t.Helper()
	targetPath, targetSpelled, targetCanonical := root.mutationCoordinates(t, target, true)
	if !root.Contains(targetSpelled, targetCanonical) {
		t.Fatalf("Darwin integration symlink target escaped admitted root: %q -> %q", targetSpelled, targetCanonical)
	}
	root.runMutation(t, link, false, func(linkPath string) error { return os.Symlink(targetPath, linkPath) })
}

func (root darwinTestRoot) link(t *testing.T, existing, link string) {
	t.Helper()
	existingPath, existingSpelled, existingCanonical := root.mutationCoordinates(t, existing, true)
	if !root.Contains(existingSpelled, existingCanonical) {
		t.Fatalf("Darwin integration hard-link source escaped admitted root: %q -> %q", existingSpelled, existingCanonical)
	}
	root.runMutation(t, link, false, func(linkPath string) error { return os.Link(existingPath, linkPath) })
}

func (root darwinTestRoot) rename(t *testing.T, existing, replacement string) {
	t.Helper()
	existingPath, existingSpelled, existingCanonical := root.mutationCoordinates(t, existing, true)
	if !root.Contains(existingSpelled, existingCanonical) {
		t.Fatalf("Darwin integration rename source escaped admitted root: %q -> %q", existingSpelled, existingCanonical)
	}
	root.runMutation(t, replacement, false, func(replacementPath string) error { return os.Rename(existingPath, replacementPath) })
}

func (root darwinTestRoot) mkfifo(t *testing.T, candidate string, mode os.FileMode) {
	t.Helper()
	root.runMutation(t, candidate, false, func(path string) error { return unix.Mkfifo(path, uint32(mode.Perm())) })
}

func (root darwinTestRoot) listenUnix(t *testing.T, candidate string) *net.UnixListener {
	t.Helper()
	var listener *net.UnixListener
	root.runMutation(t, candidate, false, func(path string) error {
		created, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		listener = created
		return err
	})
	return listener
}

func (root darwinTestRoot) leaveUnixSocket(t *testing.T, candidate string) {
	t.Helper()
	listener := root.listenUnix(t, candidate)
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func addDarwinTestACL(t *testing.T, root darwinTestRoot, path, entry string) {
	t.Helper()
	root.runMutation(t, path, true, func(admittedPath string) error {
		command := exec.Command("/bin/chmod", "+a", entry, admittedPath)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("add temp-tree ACL: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}
