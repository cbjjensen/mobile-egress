//go:build darwin

package clientapp

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSocketDirectoryRemainsTraversableUnderDaemonUmask(t *testing.T) {
	// launchd's Umask=63 is octal 077. Do not run this test in parallel:
	// umask is process-wide.
	previous := unix.Umask(0o077)
	defer unix.Umask(previous)
	path := filepath.Join(t.TempDir(), "runtime")
	if err := prepareSocketDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("runtime directory mode = %o; Client owner cannot traverse it", info.Mode().Perm())
	}
	// Also repair a directory left behind by the previous daemon startup.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(path)
	if info.Mode().Perm() != 0o755 {
		t.Fatal("existing private runtime directory was not repaired")
	}
}

func TestSocketDirectoryRejectsSymlinksBeforeChangingPermissions(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "runtime")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(link); err == nil {
		t.Fatal("accepted a symlink as the service runtime directory")
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o700 {
		t.Fatal("rejected symlink changed target permissions")
	}
}

func TestSocketDirectoryRejectsWritablePathsBeforeChangingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(path); err == nil {
		t.Fatal("accepted a runtime directory writable by other accounts")
	}
	info, _ := os.Lstat(path)
	if info.Mode().Perm() != 0o777 {
		t.Fatal("changed permissions before rejecting unsafe ownership boundary")
	}
}
