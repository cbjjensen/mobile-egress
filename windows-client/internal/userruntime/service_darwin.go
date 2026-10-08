//go:build darwin

package userruntime

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const installedService = "com.zfnf.mobile-egress.client"
const installedSocket = "/var/run/mobile-egress-client/control.sock"

var errInstalledService = errors.New("The installed PKG Client service is registered or running. Use its installed Client app, or ask the installation owner to uninstall the PKG service before using this app. The DMG keeps separate activation and phone pairing.")
var errServiceCheck = errors.New("Could not verify that the installed Client service is inactive. Reopen the app, or ask the installation owner to review the PKG service. No service settings were changed.")

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 65536 {
		return 0, errServiceCheck
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// Refuse a registered launchd job even between restarts: its KeepAlive policy
// may start it again while the per-user app is serving.
func CheckInstalledService(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/launchctl", "print", "system/"+installedService)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = 100 * time.Millisecond
	var output boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return errServiceCheck
		}
		code = exit.ExitCode()
	}
	if ctx.Err() != nil {
		return errServiceCheck
	}
	if err := launchdResult(output.data, code); err != nil {
		return err
	}
	return checkInstalledSocket(ctx)
}

func launchdResult(output []byte, code int) error {
	if code == 0 {
		return errInstalledService
	}
	if code == 113 && strings.Contains(string(output), `Could not find service "`+installedService+`"`) {
		return nil
	}
	return errServiceCheck
}

func checkInstalledSocket(ctx context.Context) error {
	info, err := os.Lstat(installedSocket)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errServiceCheck
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode()&os.ModeSocket == 0 {
		return errServiceCheck
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", installedSocket)
	if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errServiceCheck
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return errServiceCheck
	}
	var uid uint32
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		var peer *unix.Xucred
		peer, credentialErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credentialErr == nil {
			uid = peer.Uid
		}
	})
	if err != nil || credentialErr != nil || uid != 0 {
		return errServiceCheck
	}
	return errInstalledService
}
