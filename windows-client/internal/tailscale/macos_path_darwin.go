//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	darwinStageDirectoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	darwinStageFileFlags      = unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	darwinStageStatFlags      = unix.AT_SYMLINK_NOFOLLOW
)

type darwinMacStageOperations struct{}

type darwinMacStageDirectory struct {
	path        string
	fd          int
	created     unix.Stat_t
	admitted    unix.Stat_t
	hasAdmitted bool
	closed      bool
}

func (directory *darwinMacStageDirectory) Path() string {
	if directory == nil {
		return ""
	}
	return directory.path
}

func (directory *darwinMacStageDirectory) Close() error {
	if directory == nil || directory.closed || directory.fd < 0 {
		return os.ErrClosed
	}
	directory.closed = true
	fd := directory.fd
	directory.fd = -1
	return unix.Close(fd)
}

type darwinMacStageFile struct {
	file        *os.File
	created     unix.Stat_t
	admitted    unix.Stat_t
	hasAdmitted bool
	closed      bool
}

func (file *darwinMacStageFile) Write(value []byte) (int, error) { return file.file.Write(value) }
func (file *darwinMacStageFile) ReadAt(value []byte, offset int64) (int, error) {
	return file.file.ReadAt(value, offset)
}
func (file *darwinMacStageFile) Sync() error { return file.file.Sync() }
func (file *darwinMacStageFile) Close() error {
	if file == nil || file.file == nil || file.closed {
		return os.ErrClosed
	}
	file.closed = true
	return file.file.Close()
}

func newPlatformMacStageOperations() (macStageOperations, error) {
	return darwinMacStageOperations{}, nil
}

func (darwinMacStageOperations) ResolveTemporaryBase(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if err := validateDarwinExactPath(resolved); err != nil {
		return "", err
	}
	return resolved, ctx.Err()
}

func (darwinMacStageOperations) OpenParent(ctx context.Context, path string) (macStageDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDarwinExactPath(path); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, darwinStageDirectoryFlags, 0)
	if err != nil {
		return nil, err
	}
	directory := &darwinMacStageDirectory{path: path, fd: fd}
	var descriptor unix.Stat_t
	var pathname unix.Stat_t
	if validateDarwinExactPath(path) != nil || unix.Fstat(fd, &descriptor) != nil || unix.Lstat(path, &pathname) != nil ||
		!sameDarwinStaticIdentity(descriptor, pathname) || descriptor.Mode&unix.S_IFMT != unix.S_IFDIR {
		return directory, errMacStage
	}
	directory.created = descriptor
	directory.admitted = descriptor
	directory.hasAdmitted = true
	return directory, nil
}

func (darwinMacStageOperations) CreateDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	name string,
	mode fs.FileMode,
) (macStageDirectory, error) {
	parent, ok := parentHandle.(*darwinMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || !usableDarwinDirectory(parent) || mode != 0o700 || !validStageChildName(name) {
		return nil, errMacStage
	}
	if err := unix.Mkdirat(parent.fd, name, uint32(mode)); err != nil {
		return nil, err
	}
	path := filepath.Join(parent.path, name)
	directory := &darwinMacStageDirectory{path: path, fd: -1}
	fd, err := unix.Openat(parent.fd, name, darwinStageDirectoryFlags, 0)
	if err != nil {
		return directory, err
	}
	directory.fd = fd
	if err := unix.Fchmod(fd, uint32(mode)); err != nil {
		return directory, err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(fd, &descriptor) != nil || unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(descriptor, relative) || !validDarwinStageDirectoryStat(descriptor) {
		return directory, errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return directory, errMacStage
	}
	directory.created = descriptor
	return directory, nil
}

func (darwinMacStageOperations) CreateFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	name string,
	mode fs.FileMode,
) (macStageFile, error) {
	directory, ok := directoryHandle.(*darwinMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || !usableDarwinDirectory(directory) || mode != 0o600 || !validStageChildName(name) {
		return nil, errMacStage
	}
	fd, err := unix.Openat(directory.fd, name, darwinStageFileFlags, uint32(mode))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
	handle := &darwinMacStageFile{file: file}
	if file == nil {
		_ = unix.Close(fd)
		return handle, errMacStage
	}
	if err := unix.Fchmod(fd, uint32(mode)); err != nil {
		return handle, err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(fd, &descriptor) != nil || unix.Fstatat(directory.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(descriptor, relative) || !validDarwinStageFileStat(descriptor) {
		return handle, errMacStage
	}
	if err := validateDarwinExactChild(ctx, directory.fd, name); err != nil {
		return handle, errMacStage
	}
	handle.created = descriptor
	return handle, nil
}

func (darwinMacStageOperations) AdmitFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (int64, error) {
	directory, file, err := darwinStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil {
		return 0, err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(int(file.file.Fd()), &descriptor) != nil || unix.Fstatat(directory.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(descriptor, relative) || !sameDarwinStaticIdentity(file.created, descriptor) ||
		!validDarwinStageFileStat(descriptor) || descriptor.Size <= 0 {
		return 0, errMacStage
	}
	if err := validateDarwinExactChild(ctx, directory.fd, name); err != nil {
		return 0, errMacStage
	}
	file.admitted = descriptor
	file.hasAdmitted = true
	return descriptor.Size, nil
}

func (darwinMacStageOperations) AdmitDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := darwinStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil {
		return err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(directory.fd, &descriptor) != nil || unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(descriptor, relative) || !sameDarwinStaticIdentity(directory.created, descriptor) ||
		!validDarwinStageDirectoryStat(descriptor) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return errMacStage
	}
	directory.admitted = descriptor
	directory.hasAdmitted = true
	return nil
}

func (darwinMacStageOperations) ValidateParent(ctx context.Context, handle macStageDirectory) error {
	directory, ok := handle.(*darwinMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || !usableDarwinDirectory(directory) || !directory.hasAdmitted {
		return errMacStage
	}
	if err := validateDarwinExactPath(directory.path); err != nil {
		return errMacStage
	}
	var descriptor unix.Stat_t
	var pathname unix.Stat_t
	if unix.Fstat(directory.fd, &descriptor) != nil || unix.Lstat(directory.path, &pathname) != nil ||
		!sameDarwinStaticIdentity(directory.admitted, descriptor) || !sameDarwinStaticIdentity(descriptor, pathname) ||
		descriptor.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errMacStage
	}
	return nil
}

func (darwinMacStageOperations) ValidateDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := darwinStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil || !directory.hasAdmitted {
		return errMacStage
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(directory.fd, &descriptor) != nil || unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(directory.admitted, descriptor) || !sameDarwinCompleteStat(descriptor, relative) ||
		!validDarwinStageDirectoryStat(descriptor) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return errMacStage
	}
	return nil
}

func (darwinMacStageOperations) ValidateDirectoryIdentity(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := darwinStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil {
		return err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(directory.fd, &descriptor) != nil || unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinStaticIdentity(directory.created, descriptor) || !sameDarwinStaticIdentity(descriptor, relative) ||
		!validDarwinStageDirectoryStat(descriptor) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return errMacStage
	}
	return nil
}

func (darwinMacStageOperations) ValidateFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (int64, error) {
	directory, file, err := darwinStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil || !file.hasAdmitted {
		return 0, errMacStage
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(int(file.file.Fd()), &descriptor) != nil || unix.Fstatat(directory.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinCompleteStat(file.admitted, descriptor) || !sameDarwinCompleteStat(descriptor, relative) ||
		!validDarwinStageFileStat(descriptor) {
		return 0, errMacStage
	}
	if err := validateDarwinExactChild(ctx, directory.fd, name); err != nil {
		return 0, errMacStage
	}
	return descriptor.Size, nil
}

func (darwinMacStageOperations) ReadDirectory(ctx context.Context, handle macStageDirectory) (names []string, returnErr error) {
	directory, ok := handle.(*darwinMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || !usableDarwinDirectory(directory) {
		return nil, errMacStage
	}
	return readDarwinDirectoryNames(ctx, directory.fd)
}

func readDarwinDirectoryNames(ctx context.Context, directoryFD int) (names []string, returnErr error) {
	fd, err := unix.Openat(directoryFD, ".", darwinStageDirectoryFlags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "darwin-stage-directory")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errMacStage
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			names = nil
			returnErr = errMacStage
		}
	}()
	var reopened unix.Stat_t
	var original unix.Stat_t
	if unix.Fstat(fd, &reopened) != nil || unix.Fstat(directoryFD, &original) != nil || !sameDarwinCompleteStat(reopened, original) {
		return nil, errMacStage
	}
	for {
		batch, readErr := file.Readdirnames(64)
		names = append(names, batch...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

func validateDarwinExactChild(ctx context.Context, directoryFD int, name string) error {
	if !validStageChildName(name) {
		return errMacStage
	}
	names, err := readDarwinDirectoryNames(ctx, directoryFD)
	if err != nil || !hasExactStageEntry(names, name) {
		return errMacStage
	}
	return nil
}

func (darwinMacStageOperations) RemoveFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) error {
	directory, file, err := darwinStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil {
		return err
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(int(file.file.Fd()), &descriptor) != nil || unix.Fstatat(directory.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinStaticIdentity(file.created, descriptor) || !sameDarwinStaticIdentity(descriptor, relative) ||
		!validDarwinStageFileStat(descriptor) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, directory.fd, name); err != nil {
		return errMacStage
	}
	if err := file.Close(); err != nil {
		return err
	}
	if unix.Fstatat(directory.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinStaticIdentity(file.created, relative) || !validDarwinStageFileStat(relative) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, directory.fd, name); err != nil {
		return errMacStage
	}
	return unix.Unlinkat(directory.fd, name, 0)
}

func (operations darwinMacStageOperations) RemoveDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := darwinStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil {
		return err
	}
	entries, err := operations.ReadDirectory(ctx, directory)
	if err != nil || len(entries) != 0 {
		return errMacStage
	}
	var descriptor unix.Stat_t
	var relative unix.Stat_t
	if unix.Fstat(directory.fd, &descriptor) != nil || unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinStaticIdentity(directory.created, descriptor) || !sameDarwinStaticIdentity(descriptor, relative) ||
		!validDarwinStageDirectoryStat(descriptor) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return errMacStage
	}
	if err := directory.Close(); err != nil {
		return err
	}
	if unix.Fstatat(parent.fd, name, &relative, darwinStageStatFlags) != nil ||
		!sameDarwinStaticIdentity(directory.created, relative) || !validDarwinStageDirectoryStat(relative) {
		return errMacStage
	}
	if err := validateDarwinExactChild(ctx, parent.fd, name); err != nil {
		return errMacStage
	}
	return unix.Unlinkat(parent.fd, name, unix.AT_REMOVEDIR)
}

func darwinStageDirectories(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) (*darwinMacStageDirectory, *darwinMacStageDirectory, error) {
	parent, parentOK := parentHandle.(*darwinMacStageDirectory)
	directory, directoryOK := directoryHandle.(*darwinMacStageDirectory)
	if err := ctx.Err(); err != nil || !parentOK || !directoryOK || !usableDarwinDirectory(parent) ||
		!usableDarwinDirectory(directory) || !validStageChildName(name) || directory.path != filepath.Join(parent.path, name) {
		return nil, nil, errMacStage
	}
	return parent, directory, nil
}

func darwinStageHandles(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (*darwinMacStageDirectory, *darwinMacStageFile, error) {
	directory, directoryOK := directoryHandle.(*darwinMacStageDirectory)
	file, fileOK := fileHandle.(*darwinMacStageFile)
	if err := ctx.Err(); err != nil || !directoryOK || !fileOK || !usableDarwinDirectory(directory) ||
		file == nil || file.file == nil || file.closed || !validStageChildName(name) {
		return nil, nil, errMacStage
	}
	return directory, file, nil
}

func usableDarwinDirectory(directory *darwinMacStageDirectory) bool {
	return directory != nil && !directory.closed && directory.fd >= 0
}

func validDarwinStageDirectoryStat(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Mode&0o7777 == 0o700 && stat.Uid == uint32(unix.Geteuid())
}

func validDarwinStageFileStat(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&0o7777 == 0o600 &&
		stat.Uid == uint32(unix.Geteuid()) && stat.Nlink == 1
}

func sameDarwinStaticIdentity(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Gen == right.Gen &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Mode == right.Mode &&
		left.Rdev == right.Rdev && left.Flags == right.Flags
}

// Atime is intentionally excluded: descriptor hashing can legitimately update
// it. The captured fields are the complete stable Darwin identity required by
// the staging policy.
func sameDarwinCompleteStat(left, right unix.Stat_t) bool {
	return sameDarwinStaticIdentity(left, right) && left.Nlink == right.Nlink &&
		left.Size == right.Size && left.Btim == right.Btim && left.Ctim == right.Ctim && left.Mtim == right.Mtim
}

func validateDarwinExactPath(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) || cleaned != path {
		return errors.New("path is not canonical")
	}
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(cleaned, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return err
		}
		exact := false
		for _, entry := range entries {
			if entry.Name() == component {
				exact = true
				break
			}
		}
		if !exact {
			return errors.New("path component case mismatch")
		}
		current = filepath.Join(current, component)
		var stat unix.Stat_t
		if err := unix.Lstat(current, &stat); err != nil || stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return errors.New("symlinked path component")
		}
	}
	return nil
}

var _ macStageOperations = darwinMacStageOperations{}
var _ macStageDirectory = (*darwinMacStageDirectory)(nil)
var _ macStageFile = (*darwinMacStageFile)(nil)
