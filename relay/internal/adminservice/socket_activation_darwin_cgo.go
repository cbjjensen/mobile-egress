//go:build darwin && cgo

package adminservice

/*
#include <errno.h>
#include <launch.h>
#include <stdlib.h>
#include <unistd.h>

static int zfnf_activate_relay_admin_socket(int *out_fd) {
	int *fds = NULL;
	size_t count = 0;
	int result = launch_activate_socket("RelayAdmin", &fds, &count);
	if (result != 0) {
		return result;
	}
	if (fds == NULL || count != 1) {
		for (size_t index = 0; index < count; index++) {
			close(fds[index]);
		}
		free(fds);
		return EPROTO;
	}
	*out_fd = fds[0];
	free(fds);
	return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
)

func OpenDarwinLaunchdAdminSocket(ctx context.Context) (*AdminSocket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var descriptor C.int
	if result := C.zfnf_activate_relay_admin_socket(&descriptor); result != 0 {
		return nil, errors.Join(errAdminSocketUnsafe, syscall.Errno(result))
	}
	file := os.NewFile(uintptr(descriptor), "mobile-egress-relay-admin")
	if file == nil {
		return nil, errAdminSocketUnsafe
	}
	listener, err := net.FileListener(file)
	fileErr := file.Close()
	if err != nil || fileErr != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, errors.Join(errAdminSocketUnsafe, err, fileErr)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok || unixListener == nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, errAdminSocketUnsafe
	}
	unixListener.SetUnlinkOnClose(false)
	return &AdminSocket{listener: unixListener, launchManaged: true}, nil
}
