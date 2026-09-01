//go:build !darwin

package tailscale

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type portableMacStageOperations struct{}

type portableMacStageDirectory struct {
	path     string
	file     *os.File
	identity os.FileInfo
	closed   bool
}

func (directory *portableMacStageDirectory) Path() string {
	if directory == nil {
		return ""
	}
	return directory.path
}

func (directory *portableMacStageDirectory) Close() error {
	if directory == nil || directory.file == nil || directory.closed {
		return os.ErrClosed
	}
	directory.closed = true
	return directory.file.Close()
}

type portableMacStageFile struct {
	path     string
	file     *os.File
	created  os.FileInfo
	admitted os.FileInfo
	closed   bool
}

func (file *portableMacStageFile) Write(value []byte) (int, error) { return file.file.Write(value) }
func (file *portableMacStageFile) ReadAt(value []byte, offset int64) (int, error) {
	return file.file.ReadAt(value, offset)
}
func (file *portableMacStageFile) Sync() error { return file.file.Sync() }
func (file *portableMacStageFile) Close() error {
	if file == nil || file.file == nil || file.closed {
		return os.ErrClosed
	}
	file.closed = true
	return file.file.Close()
}

func newPlatformMacStageOperations() (macStageOperations, error) {
	return portableMacStageOperations{}, nil
}

func (portableMacStageOperations) ResolveTemporaryBase(ctx context.Context) (string, error) {
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
	return filepath.Clean(resolved), ctx.Err()
}

func (portableMacStageOperations) OpenParent(ctx context.Context, path string) (macStageDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	directory := &portableMacStageDirectory{path: path, file: file}
	info, err := file.Stat()
	if err != nil {
		return directory, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !info.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) {
		return directory, errMacStage
	}
	directory.identity = info
	return directory, nil
}

func (portableMacStageOperations) CreateDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	name string,
	mode fs.FileMode,
) (macStageDirectory, error) {
	parent, ok := parentHandle.(*portableMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || parent == nil || parent.closed || mode != 0o700 || !validStageChildName(name) {
		return nil, errMacStage
	}
	path := filepath.Join(parent.path, name)
	if err := os.Mkdir(path, mode); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	directory := &portableMacStageDirectory{path: path, file: file}
	if err != nil {
		return directory, err
	}
	if err := file.Chmod(mode); err != nil {
		return directory, err
	}
	info, err := file.Stat()
	if err != nil {
		return directory, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !info.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) {
		return directory, errMacStage
	}
	directory.identity = info
	return directory, nil
}

func (portableMacStageOperations) CreateFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	name string,
	mode fs.FileMode,
) (macStageFile, error) {
	directory, ok := directoryHandle.(*portableMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || directory == nil || directory.closed || mode != 0o600 || !validStageChildName(name) {
		return nil, errMacStage
	}
	path := filepath.Join(directory.path, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		return nil, err
	}
	handle := &portableMacStageFile{path: path, file: file}
	if err := file.Chmod(mode); err != nil {
		return handle, err
	}
	info, err := file.Stat()
	if err != nil {
		return handle, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) {
		return handle, errMacStage
	}
	handle.created = info
	return handle, nil
}

func (portableMacStageOperations) AdmitFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (int64, error) {
	directory, file, err := portableStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil {
		return 0, err
	}
	info, err := file.file.Stat()
	if err != nil {
		return 0, err
	}
	pathInfo, err := os.Lstat(filepath.Join(directory.path, name))
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 ||
		pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) ||
		file.created == nil || !os.SameFile(file.created, info) {
		return 0, errMacStage
	}
	file.admitted = info
	return info.Size(), nil
}

func (portableMacStageOperations) AdmitDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := portableStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil {
		return err
	}
	info, err := directory.file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(filepath.Join(parent.path, name))
	if err != nil || !info.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(info, pathInfo) || directory.identity == nil || !os.SameFile(directory.identity, info) {
		return errMacStage
	}
	directory.identity = info
	return nil
}

func (portableMacStageOperations) ValidateParent(ctx context.Context, handle macStageDirectory) error {
	directory, ok := handle.(*portableMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || directory == nil || directory.closed || directory.identity == nil {
		return errMacStage
	}
	info, err := directory.file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(directory.path)
	if err != nil || !info.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(directory.identity, info) || !os.SameFile(info, pathInfo) {
		return errMacStage
	}
	return nil
}

func (portableMacStageOperations) ValidateDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := portableStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil || directory.identity == nil {
		return errMacStage
	}
	info, err := directory.file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(filepath.Join(parent.path, name))
	if err != nil || !samePortableStageMetadata(directory.identity, info) ||
		!samePortableStageMetadata(info, pathInfo) {
		return errMacStage
	}
	return nil
}

func (portableMacStageOperations) ValidateDirectoryIdentity(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := portableStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil || directory.identity == nil {
		return errMacStage
	}
	info, err := directory.file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(filepath.Join(parent.path, name))
	if err != nil || !info.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(directory.identity, info) || !os.SameFile(info, pathInfo) {
		return errMacStage
	}
	return nil
}

func (portableMacStageOperations) ValidateFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (int64, error) {
	directory, file, err := portableStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil || file.admitted == nil {
		return 0, errMacStage
	}
	info, err := file.file.Stat()
	if err != nil {
		return 0, err
	}
	pathInfo, err := os.Lstat(filepath.Join(directory.path, name))
	if err != nil || !samePortableStageMetadata(file.admitted, info) || !samePortableStageMetadata(info, pathInfo) ||
		!info.Mode().IsRegular() {
		return 0, errMacStage
	}
	return info.Size(), nil
}

func (operations portableMacStageOperations) ReadDirectory(ctx context.Context, handle macStageDirectory) ([]string, error) {
	directory, ok := handle.(*portableMacStageDirectory)
	if err := ctx.Err(); err != nil || !ok || directory == nil || directory.closed {
		return nil, errMacStage
	}
	entries, err := os.ReadDir(directory.path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	sort.Strings(names)
	return names, nil
}

func (portableMacStageOperations) RemoveFile(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) error {
	directory, file, err := portableStageHandles(ctx, directoryHandle, fileHandle, name)
	if err != nil || file.created == nil {
		return errMacStage
	}
	path := filepath.Join(directory.path, name)
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(file.created, pathInfo) {
		return errMacStage
	}
	if err := file.Close(); err != nil {
		return err
	}
	pathInfo, err = os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(file.created, pathInfo) {
		return errMacStage
	}
	return os.Remove(path)
}

func (portableMacStageOperations) RemoveDirectory(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) error {
	parent, directory, err := portableStageDirectories(ctx, parentHandle, directoryHandle, name)
	if err != nil || directory.identity == nil {
		return errMacStage
	}
	path := filepath.Join(parent.path, name)
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(directory.identity, pathInfo) {
		return errMacStage
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return errMacStage
	}
	if err := directory.Close(); err != nil {
		return err
	}
	pathInfo, err = os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(directory.identity, pathInfo) {
		return errMacStage
	}
	return os.Remove(path)
}

func portableStageDirectories(
	ctx context.Context,
	parentHandle macStageDirectory,
	directoryHandle macStageDirectory,
	name string,
) (*portableMacStageDirectory, *portableMacStageDirectory, error) {
	parent, parentOK := parentHandle.(*portableMacStageDirectory)
	directory, directoryOK := directoryHandle.(*portableMacStageDirectory)
	if err := ctx.Err(); err != nil || !parentOK || !directoryOK || parent == nil || directory == nil ||
		parent.file == nil || directory.file == nil || parent.closed || directory.closed ||
		!validStageChildName(name) || directory.path != filepath.Join(parent.path, name) {
		return nil, nil, errMacStage
	}
	return parent, directory, nil
}

func portableStageHandles(
	ctx context.Context,
	directoryHandle macStageDirectory,
	fileHandle macStageFile,
	name string,
) (*portableMacStageDirectory, *portableMacStageFile, error) {
	directory, directoryOK := directoryHandle.(*portableMacStageDirectory)
	file, fileOK := fileHandle.(*portableMacStageFile)
	if err := ctx.Err(); err != nil || !directoryOK || !fileOK || directory == nil || file == nil ||
		directory.file == nil || file.file == nil || directory.closed || file.closed ||
		!validStageChildName(name) || file.path != filepath.Join(directory.path, name) {
		return nil, nil, errMacStage
	}
	return directory, file, nil
}

func samePortableStageMetadata(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

var _ macStageOperations = portableMacStageOperations{}
var _ macStageDirectory = (*portableMacStageDirectory)(nil)
var _ macStageFile = (*portableMacStageFile)(nil)
