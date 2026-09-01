//go:build darwin

package tailscale

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	identityDarwinBundleOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	identityDarwinFileOpenFlags   = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
)

type identityDarwinCommandFactory func(context.Context, string, ...string) *exec.Cmd

type identityDarwinTrustRunner struct {
	newCommand identityDarwinCommandFactory
}

func verifyDarwinBundle(
	ctx context.Context,
	bundlePath string,
	executablePath string,
	requirement string,
) (verifiedDarwinApp, error) {
	return verifyDarwinAppWithDependencies(
		ctx,
		bundlePath,
		executablePath,
		requirement,
		openIdentityDarwinAppPathState,
		identityDarwinTrustRunner{newCommand: exec.CommandContext},
	)
}

func (runner identityDarwinTrustRunner) Run(
	ctx context.Context,
	path string,
	args []string,
	environment []string,
	limit int,
) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if (path != "/usr/bin/codesign" && path != "/usr/sbin/spctl") ||
		limit != maximumIdentityTrustOutput || !sameIdentityStringSlice(environment, newIdentityTrustEnvironment()) {
		return nil, errTailscaleAppVerification
	}
	newCommand := runner.newCommand
	if newCommand == nil {
		newCommand = exec.CommandContext
	}
	output, err := runDarwinBoundedCommand(
		ctx,
		func(commandContext context.Context, commandPath string, commandArgs ...string) *exec.Cmd {
			return newCommand(commandContext, commandPath, commandArgs...)
		},
		path,
		args,
		environment,
		limit,
	)
	if errors.Is(err, errDarwinBoundedCommandOutputLimit) {
		return nil, errIdentityTrustOutput
	}
	if err != nil {
		return nil, err
	}
	return output, nil
}

func sameIdentityStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type identityDarwinAppPathState struct {
	mu               sync.Mutex
	bundlePath       string
	executablePath   string
	bundleFD         int
	executableFD     int
	bundleClosed     bool
	executableClosed bool
}

func openIdentityDarwinAppPathState(
	ctx context.Context,
	bundlePath string,
	executablePath string,
) (identityAppPathState, identityAppObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state := &identityDarwinAppPathState{
		bundlePath: bundlePath, executablePath: executablePath,
		bundleFD: -1, executableFD: -1,
	}
	if ctx.Err() != nil || validateIdentityDarwinExactPath(bundlePath) != nil ||
		validateIdentityDarwinExactPath(executablePath) != nil {
		return state, identityAppObservation{}, errTailscaleAppVerification
	}
	bundleFD, err := unix.Open(bundlePath, identityDarwinBundleOpenFlags, 0)
	if err != nil {
		return state, identityAppObservation{}, errTailscaleAppVerification
	}
	state.bundleFD = bundleFD
	executableFD, err := unix.Open(executablePath, identityDarwinFileOpenFlags, 0)
	if err != nil {
		return state, identityAppObservation{}, errTailscaleAppVerification
	}
	state.executableFD = executableFD
	observation, err := state.Observe(ctx)
	if err != nil || validateIdentityAppObservation(observation, observation) != nil {
		return state, identityAppObservation{}, errTailscaleAppVerification
	}
	return state, observation, nil
}

func (state *identityDarwinAppPathState) Observe(ctx context.Context) (identityAppObservation, error) {
	if state == nil {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if ctx.Err() != nil || state.bundleClosed || state.executableClosed ||
		state.bundleFD < 0 || state.executableFD < 0 ||
		validateIdentityDarwinExactPath(state.bundlePath) != nil ||
		validateIdentityDarwinExactPath(state.executablePath) != nil {
		return identityAppObservation{}, errTailscaleAppVerification
	}

	bundleStat, err := identityDarwinDescriptorPathStat(state.bundleFD, state.bundlePath)
	if err != nil || bundleStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	executableBefore, err := identityDarwinDescriptorPathStat(state.executableFD, state.executablePath)
	if err != nil || executableBefore.Mode&unix.S_IFMT != unix.S_IFREG ||
		executableBefore.Mode&0o111 == 0 || executableBefore.Size <= 0 {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	digest, err := hashIdentityDarwinDescriptor(ctx, state.executableFD, executableBefore.Size)
	if err != nil {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	executableAfter, err := identityDarwinDescriptorPathStat(state.executableFD, state.executablePath)
	if err != nil || !sameIdentityDarwinCompleteStat(executableBefore, executableAfter) ||
		validateIdentityDarwinExactPath(state.bundlePath) != nil ||
		validateIdentityDarwinExactPath(state.executablePath) != nil {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	bundleAfter, err := identityDarwinDescriptorPathStat(state.bundleFD, state.bundlePath)
	if err != nil || !sameIdentityDarwinCompleteStat(bundleStat, bundleAfter) {
		return identityAppObservation{}, errTailscaleAppVerification
	}
	return identityAppObservation{
		Bundle:     identityObservationFromDarwinStat(state.bundlePath, bundleAfter, identityPathDirectory, false, [32]byte{}),
		Executable: identityObservationFromDarwinStat(state.executablePath, executableAfter, identityPathRegular, true, digest),
	}, nil
}

func (state *identityDarwinAppPathState) CloseExecutable() error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.executableClosed || state.executableFD < 0 {
		state.executableClosed = true
		return nil
	}
	fd := state.executableFD
	state.executableFD = -1
	state.executableClosed = true
	return unix.Close(fd)
}

func (state *identityDarwinAppPathState) CloseBundle() error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.bundleClosed || state.bundleFD < 0 {
		state.bundleClosed = true
		return nil
	}
	fd := state.bundleFD
	state.bundleFD = -1
	state.bundleClosed = true
	return unix.Close(fd)
}

func identityDarwinDescriptorPathStat(fd int, path string) (unix.Stat_t, error) {
	var descriptor unix.Stat_t
	var pathname unix.Stat_t
	if unix.Fstat(fd, &descriptor) != nil || unix.Lstat(path, &pathname) != nil ||
		!sameIdentityDarwinCompleteStat(descriptor, pathname) {
		return unix.Stat_t{}, errTailscaleAppVerification
	}
	return descriptor, nil
}

func sameIdentityDarwinCompleteStat(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Gen == right.Gen &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Mode == right.Mode &&
		left.Nlink == right.Nlink && left.Rdev == right.Rdev && left.Size == right.Size &&
		left.Flags == right.Flags && left.Btim == right.Btim && left.Ctim == right.Ctim && left.Mtim == right.Mtim
}

func identityObservationFromDarwinStat(
	path string,
	stat unix.Stat_t,
	kind identityPathKind,
	executable bool,
	digest [32]byte,
) identityPathObservation {
	return identityPathObservation{
		Path: path, Present: true, ExactCase: true, SymlinkFree: true,
		Kind: kind, Executable: executable,
		Device: uint64(uint32(stat.Dev)), Inode: stat.Ino, Generation: uint64(stat.Gen),
		UID: stat.Uid, GID: stat.Gid, Mode: uint32(stat.Mode), LinkCount: uint64(stat.Nlink),
		DeviceType: uint64(uint32(stat.Rdev)), Size: stat.Size, Flags: stat.Flags,
		BirthTime:  identityTimestamp{Seconds: stat.Btim.Sec, Nanoseconds: stat.Btim.Nsec},
		ChangeTime: identityTimestamp{Seconds: stat.Ctim.Sec, Nanoseconds: stat.Ctim.Nsec},
		ModifyTime: identityTimestamp{Seconds: stat.Mtim.Sec, Nanoseconds: stat.Mtim.Nsec},
		Digest:     digest,
	}
}

func hashIdentityDarwinDescriptor(ctx context.Context, fd int, size int64) ([32]byte, error) {
	if size <= 0 {
		return [32]byte{}, errTailscaleAppVerification
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for offset := int64(0); offset < size; {
		if ctx.Err() != nil {
			return [32]byte{}, errTailscaleAppVerification
		}
		remaining := size - offset
		readBuffer := buffer
		if int64(len(readBuffer)) > remaining {
			readBuffer = readBuffer[:remaining]
		}
		count, err := unix.Pread(fd, readBuffer, offset)
		if count > 0 {
			_, _ = hash.Write(readBuffer[:count])
			offset += int64(count)
		}
		if err != nil {
			return [32]byte{}, errTailscaleAppVerification
		}
		if count == 0 {
			return [32]byte{}, errTailscaleAppVerification
		}
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func validateIdentityDarwinExactPath(path string) error {
	cleaned := filepath.Clean(path)
	if path == "" || cleaned != path || !filepath.IsAbs(cleaned) {
		return errTailscaleAppVerification
	}
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(cleaned, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return errTailscaleAppVerification
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return errTailscaleAppVerification
		}
		exact := false
		for _, entry := range entries {
			if entry.Name() == component {
				exact = true
				break
			}
		}
		if !exact {
			return errTailscaleAppVerification
		}
		current = filepath.Join(current, component)
		var stat unix.Stat_t
		if unix.Lstat(current, &stat) != nil || stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return errTailscaleAppVerification
		}
	}
	return nil
}

var _ identityTrustCommandRunner = identityDarwinTrustRunner{}
var _ identityAppPathState = (*identityDarwinAppPathState)(nil)
