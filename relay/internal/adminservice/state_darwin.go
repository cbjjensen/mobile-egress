//go:build darwin

package adminservice

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type darwinStateFilesystem struct{}

func newPlatformStateFilesystem() (stateFilesystem, error) {
	return darwinStateFilesystem{}, nil
}

func newPlatformACLInspector() pathACLInspector {
	return newDarwinACLInspector()
}

func (darwinStateFilesystem) Lstat(ctx context.Context, path string) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return pathMetadata{}, err
	}
	return pathMetadataFromDarwinStat(stat), nil
}

func (darwinStateFilesystem) Open(ctx context.Context, path string, expected pathMetadata) (openedPath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags, err := darwinOpenFlags(expected)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errStatePathUnsafe
	}
	opened := &darwinOpenedPath{path: path, file: file}
	metadata, err := opened.Metadata(ctx)
	if err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	if metadata != expected {
		return nil, errors.Join(errStatePathChanged, opened.Close())
	}
	return opened, nil
}

func darwinOpenFlags(expected pathMetadata) (int, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	switch expected.Type {
	case pathTypeDirectory:
		return flags | unix.O_DIRECTORY, nil
	case pathTypeRegular:
		return flags, nil
	default:
		return 0, errStatePathUnsafe
	}
}

func (darwinStateFilesystem) Mkdir(ctx context.Context, path string, mode uint16) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unix.Mkdir(path, uint32(mode))
}

type darwinOpenedPath struct {
	path   string
	file   *os.File
	closed bool
}

func (opened *darwinOpenedPath) Path() string {
	if opened == nil {
		return ""
	}
	return opened.path
}

func (opened *darwinOpenedPath) Metadata(ctx context.Context) (pathMetadata, error) {
	if err := ctx.Err(); err != nil {
		return pathMetadata{}, err
	}
	if opened == nil || opened.file == nil || opened.closed {
		return pathMetadata{}, os.ErrClosed
	}
	return darwinMetadataFromFD(int(opened.file.Fd()))
}

func (opened *darwinOpenedPath) nativeFileDescriptor() (int, error) {
	if opened == nil || opened.file == nil || opened.closed {
		return 0, os.ErrClosed
	}
	return int(opened.file.Fd()), nil
}

func (opened *darwinOpenedPath) ReadDir(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opened == nil || opened.file == nil || opened.closed {
		return nil, os.ErrClosed
	}
	var names []string
	for {
		batch, err := opened.file.Readdirnames(64)
		names = append(names, batch...)
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

func (opened *darwinOpenedPath) Chmod(
	ctx context.Context,
	expected pathMetadata,
	mode uint16,
	validateACL func(context.Context) error,
) error {
	if validateACL == nil {
		return errStatePathUnsafe
	}
	if err := opened.verify(ctx, expected); err != nil {
		return err
	}
	if err := validateACL(ctx); err != nil {
		return err
	}
	if err := opened.verify(ctx, expected); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Fchmod(int(opened.file.Fd()), uint32(mode)); err != nil {
		return err
	}
	wantAfter := expected
	wantAfter.Permissions = mode
	if err := opened.verify(ctx, wantAfter); err != nil {
		return err
	}
	if err := validateACL(ctx); err != nil {
		return err
	}
	return opened.verify(ctx, wantAfter)
}

func (opened *darwinOpenedPath) verify(ctx context.Context, expected pathMetadata) error {
	metadata, err := opened.Metadata(ctx)
	if err != nil || metadata != expected {
		return errStatePathChanged
	}
	var pathStat unix.Stat_t
	if err := unix.Lstat(opened.path, &pathStat); err != nil || pathMetadataFromDarwinStat(pathStat) != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}

func (opened *darwinOpenedPath) Close() error {
	if opened == nil || opened.file == nil || opened.closed {
		return os.ErrClosed
	}
	opened.closed = true
	return opened.file.Close()
}

func darwinMetadataFromFD(fd int) (pathMetadata, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return pathMetadata{}, err
	}
	return pathMetadataFromDarwinStat(stat), nil
}

func pathMetadataFromDarwinStat(stat unix.Stat_t) pathMetadata {
	rawType := uint16(stat.Mode) & uint16(unix.S_IFMT)
	objectType := pathTypeOther
	switch rawType {
	case uint16(unix.S_IFDIR):
		objectType = pathTypeDirectory
	case uint16(unix.S_IFREG):
		objectType = pathTypeRegular
	case uint16(unix.S_IFSOCK):
		objectType = pathTypeSocket
	case uint16(unix.S_IFLNK):
		objectType = pathTypeSymlink
	}
	return pathMetadata{
		Device:      uint64(uint32(stat.Dev)),
		Inode:       uint64(stat.Ino),
		UID:         stat.Uid,
		GID:         stat.Gid,
		Links:       stat.Nlink,
		RawType:     rawType,
		Type:        objectType,
		Permissions: uint16(stat.Mode) & 0o7777,
	}
}

var _ stateFilesystem = darwinStateFilesystem{}
var _ openedPath = (*darwinOpenedPath)(nil)
