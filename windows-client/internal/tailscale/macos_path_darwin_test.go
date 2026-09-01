//go:build darwin

package tailscale

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinStageDescriptorFlagsAreNoFollowAndDescriptorRelative(t *testing.T) {
	wantDirectory := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	wantFile := unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	if darwinStageDirectoryFlags != wantDirectory || darwinStageFileFlags != wantFile || darwinStageStatFlags != unix.AT_SYMLINK_NOFOLLOW {
		t.Fatalf("Darwin stage flags = %#x/%#x/%#x", darwinStageDirectoryFlags, darwinStageFileFlags, darwinStageStatFlags)
	}
}

func TestDarwinStageMetadataRequiresExactOwnerModes(t *testing.T) {
	directory := unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: uint32(unix.Geteuid())}
	file := unix.Stat_t{Mode: unix.S_IFREG | 0o600, Uid: uint32(unix.Geteuid()), Nlink: 1}
	if !validDarwinStageDirectoryStat(directory) || !validDarwinStageFileStat(file) {
		t.Fatal("exact owner-only stage modes were rejected")
	}
	for _, mutation := range []struct {
		name      string
		directory unix.Stat_t
		file      unix.Stat_t
	}{
		{name: "setuid", directory: unix.Stat_t{Mode: unix.S_IFDIR | 0o4700, Uid: directory.Uid}, file: file},
		{name: "sticky", directory: directory, file: unix.Stat_t{Mode: unix.S_IFREG | 0o1600, Uid: file.Uid, Nlink: 1}},
		{name: "wrong owner", directory: unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: directory.Uid + 1}, file: file},
		{name: "hard link", directory: directory, file: unix.Stat_t{Mode: unix.S_IFREG | 0o600, Uid: file.Uid, Nlink: 2}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if validDarwinStageDirectoryStat(mutation.directory) && validDarwinStageFileStat(mutation.file) {
				t.Fatal("non-exact Darwin stage metadata was accepted")
			}
		})
	}
}

func TestDarwinStageDescriptorRelativeLifecycleAndMetadata(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := darwinMacStageOperations{}
	parentHandle, err := operations.OpenParent(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentHandle.(*darwinMacStageDirectory)
	name := macStageDirectoryPrefix + "0123456789abcdef0123456789abcdef"
	directoryHandle, err := operations.CreateDirectory(ctx, parent, name, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	directory := directoryHandle.(*darwinMacStageDirectory)
	const basename = "Tailscale-1.100.1-macos.pkg"
	fileHandle, err := operations.CreateFile(ctx, directory, basename, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file := fileHandle.(*darwinMacStageFile)
	if _, err := file.Write([]byte("fixture package")); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitFile(ctx, directory, file, basename); err != nil {
		t.Fatal(err)
	}
	if err := operations.AdmitDirectory(ctx, parent, directory, name); err != nil {
		t.Fatal(err)
	}
	if err := operations.ValidateParent(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := operations.ValidateDirectory(ctx, parent, directory, name); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ValidateFile(ctx, directory, file, basename); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ValidateFile(ctx, directory, file, basename); err != nil {
		t.Fatalf("legitimate repeated validation changed atime-sensitive identity: %v", err)
	}

	var directoryStat unix.Stat_t
	var fileStat unix.Stat_t
	if err := unix.Fstatat(parent.fd, name, &directoryStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstatat(directory.fd, basename, &fileStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
	if !validDarwinStageDirectoryStat(directoryStat) || !validDarwinStageFileStat(fileStat) ||
		directoryStat.Uid != uint32(unix.Geteuid()) || fileStat.Uid != uint32(unix.Geteuid()) || fileStat.Nlink != 1 {
		t.Fatalf("Darwin directory/file metadata = %#v/%#v", directoryStat, fileStat)
	}
	entries, err := operations.ReadDirectory(ctx, directory)
	if err != nil || len(entries) != 1 || entries[0] != basename {
		t.Fatalf("descriptor enumeration = %#v/%v", entries, err)
	}
	if err := operations.RemoveFile(ctx, directory, file, basename); err != nil {
		t.Fatal(err)
	}
	if err := operations.RemoveDirectory(ctx, parent, directory, name); err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinStageRejectsNoFollowReplacementAndWrongCase(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := darwinMacStageOperations{}
	parentHandle, err := operations.OpenParent(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentHandle.(*darwinMacStageDirectory)
	name := macStageDirectoryPrefix + "fedcba9876543210fedcba9876543210"
	directoryHandle, err := operations.CreateDirectory(ctx, parent, name, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	directory := directoryHandle.(*darwinMacStageDirectory)
	const basename = "Tailscale-1.100.1-macos.pkg"
	fileHandle, err := operations.CreateFile(ctx, directory, basename, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file := fileHandle.(*darwinMacStageFile)
	if _, err := file.Write([]byte("fixture package")); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitFile(ctx, directory, file, basename); err != nil {
		t.Fatal(err)
	}
	if err := operations.AdmitDirectory(ctx, parent, directory, name); err != nil {
		t.Fatal(err)
	}

	const admittedBackup = "admitted.pkg"
	if err := unix.Renameat(directory.fd, basename, directory.fd, admittedBackup); err != nil {
		t.Fatal(err)
	}
	if err := unix.Symlinkat(admittedBackup, directory.fd, basename); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ValidateFile(ctx, directory, file, basename); err == nil {
		t.Fatal("symlink replacement passed descriptor/path validation")
	}
	var replacement unix.Stat_t
	if err := unix.Fstatat(directory.fd, basename, &replacement, unix.AT_SYMLINK_NOFOLLOW); err != nil || replacement.Mode&unix.S_IFMT != unix.S_IFLNK {
		t.Fatalf("replacement sentinel was not preserved: %#v/%v", replacement, err)
	}

	wrongCase := filepath.Join(filepath.Dir(base), strings.ToUpper(filepath.Base(base)))
	if wrongCase != base {
		if err := validateDarwinExactPath(wrongCase); err == nil {
			t.Fatalf("wrong-case path %q passed exact traversal", wrongCase)
		}
	}

	_ = file.Close()
	_ = unix.Unlinkat(directory.fd, basename, 0)
	_ = unix.Unlinkat(directory.fd, admittedBackup, 0)
	_ = directory.Close()
	_ = unix.Unlinkat(parent.fd, name, unix.AT_REMOVEDIR)
	_ = parent.Close()
}

func TestDarwinStageRejectsCaseOnlyChildRenames(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := darwinMacStageOperations{}
	parentHandle, err := operations.OpenParent(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentHandle.(*darwinMacStageDirectory)
	name := macStageDirectoryPrefix + "aabbccddeeff00112233445566778899"
	directoryHandle, err := operations.CreateDirectory(ctx, parent, name, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	directory := directoryHandle.(*darwinMacStageDirectory)
	const basename = "Tailscale-1.100.1-macos.pkg"
	fileHandle, err := operations.CreateFile(ctx, directory, basename, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file := fileHandle.(*darwinMacStageFile)
	if _, err := file.Write([]byte("fixture package")); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitFile(ctx, directory, file, basename); err != nil {
		t.Fatal(err)
	}
	if err := operations.AdmitDirectory(ctx, parent, directory, name); err != nil {
		t.Fatal(err)
	}

	wrongBasename := strings.ToLower(basename)
	if err := unix.Renameat(directory.fd, basename, directory.fd, wrongBasename); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ValidateFile(ctx, directory, file, basename); err == nil {
		t.Fatal("case-only file rename passed exact descriptor enumeration")
	}
	if err := unix.Renameat(directory.fd, wrongBasename, directory.fd, basename); err != nil {
		t.Fatal(err)
	}

	wrongDirectoryName := strings.ToUpper(name)
	if err := unix.Renameat(parent.fd, name, parent.fd, wrongDirectoryName); err != nil {
		t.Fatal(err)
	}
	if err := operations.ValidateDirectory(ctx, parent, directory, name); err == nil {
		t.Fatal("case-only directory rename passed exact descriptor enumeration")
	}
	if err := unix.Renameat(parent.fd, wrongDirectoryName, parent.fd, name); err != nil {
		t.Fatal(err)
	}

	_ = file.Close()
	_ = unix.Unlinkat(directory.fd, basename, 0)
	_ = directory.Close()
	_ = unix.Unlinkat(parent.fd, name, unix.AT_REMOVEDIR)
	_ = parent.Close()
}

func TestDarwinStageFileOpenDoesNotFollowExistingSymlink(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := darwinMacStageOperations{}
	parentHandle, err := operations.OpenParent(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentHandle.(*darwinMacStageDirectory)
	name := macStageDirectoryPrefix + "00112233445566778899aabbccddeeff"
	directoryHandle, err := operations.CreateDirectory(ctx, parent, name, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	directory := directoryHandle.(*darwinMacStageDirectory)
	const basename = "Tailscale-1.100.1-macos.pkg"
	if err := unix.Symlinkat("sentinel", directory.fd, basename); err != nil {
		t.Fatal(err)
	}
	if file, err := operations.CreateFile(ctx, directory, basename, 0o600); err == nil || file != nil {
		t.Fatalf("existing symlink opened as a stage file: %#v/%v", file, err)
	}
	_ = unix.Unlinkat(directory.fd, basename, 0)
	_ = directory.Close()
	_ = unix.Unlinkat(parent.fd, name, unix.AT_REMOVEDIR)
	_ = parent.Close()
}
