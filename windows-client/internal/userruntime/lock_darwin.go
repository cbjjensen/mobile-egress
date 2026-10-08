//go:build darwin

package userruntime

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var errProtectedLock = errors.New("The app's user lock is not protected. Review permissions in ~/Library/Application Support/MobileEgressClient, then reopen the app.")

// Acquire pins each directory before opening its child, so symlink substitution
// cannot redirect the lock. Closing the descriptor releases flock; leave the
// inode in place so another launch cannot lock a replacement inode.
func Acquire(home string) (*os.File, error) {
	if !filepath.IsAbs(home) || os.Geteuid() == 0 {
		return nil, errProtectedLock
	}
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errProtectedLock
	}
	defer func() { unix.Close(fd) }()
	if err := checkUserPath(fd, true, false); err != nil {
		return nil, err
	}
	for _, name := range []string{"Library", "Application Support", "MobileEgressClient"} {
		if err := unix.Mkdirat(fd, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, errProtectedLock
		}
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errProtectedLock
		}
		unix.Close(fd)
		fd = next
		if err := checkUserPath(fd, true, name == "MobileEgressClient"); err != nil {
			return nil, err
		}
	}
	lockFD, err := unix.Openat(fd, "app.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errProtectedLock
	}
	lock := os.NewFile(uintptr(lockFD), "app.lock")
	if err := checkUserPath(lockFD, false, true); err != nil {
		lock.Close()
		return nil, err
	}
	if err := unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("Inevitable Mobile Relay is already open for this user. Use the existing app window, or quit it before opening another copy.")
		}
		return nil, errProtectedLock
	}
	return lock, nil
}

func checkUserPath(fd int, directory, private bool) error {
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0022 != 0 {
		return errProtectedLock
	}
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	if uint32(stat.Mode)&unix.S_IFMT != kind || private && stat.Mode&0077 != 0 || !directory && stat.Nlink != 1 {
		return errProtectedLock
	}
	return nil
}
