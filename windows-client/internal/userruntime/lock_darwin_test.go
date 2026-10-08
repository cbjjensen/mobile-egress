//go:build darwin

package userruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserLockRejectsDuplicateAndCanBeReopenedAfterRelease(t *testing.T) {
	home := t.TempDir()
	lock, err := Acquire(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if duplicate, err := Acquire(home); err == nil {
		duplicate.Close()
		t.Fatal("second app acquired active user lock")
	}
	path := filepath.Join(home, "Library", "Application Support", "MobileEgressClient", "app.lock")
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if original.Mode().Perm() != 0600 {
		t.Fatal("user lock is not private")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(home)
	if err != nil {
		t.Fatalf("closed app retained lock: %v", err)
	}
	defer next.Close()
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(original, current) {
		t.Fatal("reopening replaced the lock inode")
	}
}

func TestUserLockRefusesSymlinkAndWritablePathsWithoutChangingThem(t *testing.T) {
	for _, entry := range []string{"Library", "Library/Application Support", "Library/Application Support/MobileEgressClient", "Library/Application Support/MobileEgressClient/app.lock"} {
		t.Run(strings.ReplaceAll(entry, "/", "-"), func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(home, filepath.FromSlash(entry))
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			if err := os.Symlink(destination, target); err != nil {
				t.Fatal(err)
			}
			if lock, err := Acquire(home); err == nil {
				lock.Close()
				t.Fatal("symlink reached user runtime lock")
			}
			if link, err := os.Readlink(target); err != nil || link != destination {
				t.Fatal("refusal altered another path")
			}
		})
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "Library"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, "Library"), 0777); err != nil {
		t.Fatal(err)
	}
	if lock, err := Acquire(home); err == nil {
		lock.Close()
		t.Fatal("writable ancestor reached user lock")
	}
	info, _ := os.Stat(filepath.Join(home, "Library"))
	if info.Mode().Perm() != 0777 {
		t.Fatal("refusal modified existing permissions")
	}
}

func TestInstalledLaunchdRegistrationIsRefusedAndUnknownChecksFailClosed(t *testing.T) {
	for _, tc := range []struct {
		output string
		code   int
		want   string
	}{
		{"system/com.zfnf.mobile-egress.client = { state = running }", 0, "installed"},
		{"system/com.zfnf.mobile-egress.client = { state = waiting }", 0, "installed"},
		{"Could not find service \"com.zfnf.mobile-egress.client\" in domain for system", 113, ""},
		{"permission denied", 113, "verify"},
		{"", 1, "verify"},
	} {
		err := launchdResult([]byte(tc.output), tc.code)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("absent daemon rejected: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("unsafe launchd result %q: %v", tc.output, err)
		}
	}
}
